import assert from 'node:assert/strict';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { createHash } from 'node:crypto';
import { readFile, writeFile, mkdir, mkdtemp, access } from 'node:fs/promises';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import net from 'node:net';
import {
  childProcess,
  javaClasspath,
  stopChild,
} from './support/assembly-process.mjs';
import { createNatsBinaryPreparer } from '../../clients/dsh-plugin/test/network/transport/support/nats-binary.js';
import { buildEdgeWSS } from './support/edge-wss-build.mjs';
import { assertBoundary } from './support/edge-wss-verdict.mjs';
const run = promisify(execFile);
const root = resolve(dirname(fileURLToPath(import.meta.url)), '../..');
async function freePort() {
  const server = net.createServer();
  await new Promise((ok, fail) => {
    server.once('error', fail);
    server.listen(0, '127.0.0.1', ok);
  });
  const port = server.address().port;
  await new Promise((ok, fail) => server.close((e) => (e ? fail(e) : ok())));
  return port;
}
export async function runEdgeWSS() {
  const target = join(root, 'e2e/network/target');
  await mkdir(target, { recursive: true });
  const artifacts = await mkdtemp(join(target, 'edge-wss-'));
  const manifest = {
    head: (
      await run('git', ['rev-parse', 'HEAD'], { cwd: root })
    ).stdout.trim(),
    stages: [],
    checks: [],
    failures: [],
    completed: false,
  };
  const children = [];
  let java, gateway, client, ready;
  let phase = 'initialization';
  const secrets = [];
  const record = (e) => ({
    name: e.name,
    message: secrets.reduce(
      (s, secret) => (secret ? s.split(secret).join('[REDACTED]') : s),
      e.message,
    ),
  });
  try {
    const binaries = await buildEdgeWSS(root, join(artifacts, 'bin'));
    const broker = await createNatsBinaryPreparer({
      stageRoot: join(target, 'nats-cache'),
    })();
    const javaBin = process.env.JAVA_HOME
      ? join(
          process.env.JAVA_HOME,
          'bin',
          process.platform === 'win32' ? 'java.exe' : 'java',
        )
      : 'java';
    java = childProcess(
      javaBin,
      ['-cp', await javaClasspath(root), 'auriga.iam.NetworkSourceJavaHost'],
      { cwd: root },
    );
    children.push(['java', java, 'stop']);
    ready = await java.wait((v) => v.stage === 'ready', 'Java IAM/PG ready');
    assert.equal(ready.pid, java.child.pid);
    assert.equal(ready.parentPid, process.pid);
    manifest.stages.push(ready);
    const config = JSON.parse(await readFile(ready.config, 'utf8'));
    const privateRoot = dirname(ready.config);
    for (const path of [
      config.gatewayToken,
      config.collabToken,
      config.device,
      config.externalDevice,
    ]) {
      const value = await readFile(path, 'utf8');
      secrets.push(value.trim());
      if (path.endsWith('.json')) {
        const json = JSON.parse(value);
        secrets.push(json.access_token, json.refresh_token);
      }
    }
    assert.equal(
      'sha256:' +
        createHash('sha256')
          .update(await readFile(config.rootPublicFile))
          .digest('hex'),
      config.rootFingerprint,
    );
    const readyFile = join(artifacts, 'ready.json');
    await writeFile(readyFile, JSON.stringify(ready));
    gateway = childProcess(binaries.gateway, [], {
      cwd: root,
      env: {
        ...process.env,
        AURIGA_ASSEMBLY_READY: readyFile,
        AURIGA_NATS_SERVER: broker,
      },
    });
    children.push(['gateway', gateway, { id: 'cleanup', op: 'stop' }]);
    const gw = await gateway.wait(
      (v) => v.stage === 'gateway-ready',
      'Broker/callout ready',
      30000,
    );
    manifest.stages.push(gw);
    const combinedCA = join(privateRoot, 'edge-client-ca.pem');
    await writeFile(
      combinedCA,
      (await readFile(ready.ca)) + '\n' + (await readFile(gw.ca)),
      { mode: 0o600 },
    );
    const clientSpec = join(privateRoot, 'edge-client.json');
    async function startEdge(mode) {
      const document = JSON.parse(
        await readFile(
          join(root, 'contracts/deployment/fixtures/ip-http.json'),
          'utf8',
        ),
      );
      const ports = {};
      for (const role of ['portal', 'admin', 'instanceControl'])
        ports[role] = await freePort();
      for (const role of ['portal', 'admin'])
        document[role + 'Origin'] = {
          scheme: 'https',
          host: '127.0.0.1',
          port: ports[role],
        };
      document.issuerOrigin = document.portalOrigin;
      document.insecureHttpAcknowledged = true;
      for (const role of Object.keys(ports))
        document.listeners[role] = {
          address: '127.0.0.1',
          port: ports[role],
          tls: true,
        };
      document.instanceControl.origin.port = ports.instanceControl;
      document.instanceControl.origin.scheme = 'https';
      const accessPath = join(privateRoot, mode + '-access.json');
      await writeFile(accessPath, JSON.stringify(document), { mode: 0o600 });
      const spec = join(privateRoot, mode + '-edge.json');
      await writeFile(
        spec,
        JSON.stringify({
          Upstream: gw.wss,
          CA: mode === 'wrong-ca' ? ready.ca : gw.ca,
          Name: mode === 'wrong-name' ? 'wrong.example.com' : '',
          Cert: gw.ca,
          Key: join(privateRoot, 'broker-key.pem'),
          Access: accessPath,
          IAM: ready.https,
          HopCA: ready.ca,
          HopCert: config.edgeHopCertFile,
          HopKey: config.edgeHopKeyFile,
        }),
        { mode: 0o600 },
      );
      const edge = childProcess(binaries.edge, [], {
        cwd: root,
        env: { ...process.env, AURIGA_EDGE_WSS: spec },
      });
      children.push([mode, edge, 'stop']);
      const event = await edge.wait(
        (v) => v.stage === 'edge-ready',
        'production edge listeners',
        10000,
      );
      manifest.stages.push(event);
      return { edge, event };
    }
    const trace = async () =>
      (await gateway.request({ op: 'trace' })).records.filter(
        (v) => v.method === 'RedeemNetworkTicket',
      );
    async function probe(name, edge, options, expected) {
      phase = name;
      const decisions = () =>
        gateway.events.filter((v) => v.stage === 'callout-decision');
      const before = {
        edge: await edge.edge.request({ op: 'stats' }),
        redeems: await trace(),
        decisions: decisions(),
      };
      const response = await client.request(
        {
          op: 'connect',
          url: edge.event.portal.replace('https:', 'wss:') + '/net/v1/ws',
          ...options,
        },
        25000,
      );
      const after = {
        edge: await edge.edge.request({ op: 'stats' }),
        redeems: await trace(),
        decisions: decisions(),
      };
      const proof = { name, before, response, after };
      await writeFile(
        join(artifacts, name + '.json'),
        JSON.stringify(proof, null, 2),
      );
      assertBoundary(proof, expected);
      manifest.checks.push(name);
      return proof;
    }
    const valid = await startEdge('valid');
    await writeFile(
      clientSpec,
      JSON.stringify({ config, platformOrigin: valid.event.portal }),
      { mode: 0o600 },
    );
    client = childProcess(
      process.execPath,
      [join(root, 'e2e/network/support/edge-wss-client.mjs')],
      {
        cwd: root,
        env: {
          ...process.env,
          NODE_EXTRA_CA_CERTS: combinedCA,
          AURIGA_EDGE_WSS_CLIENT: clientSpec,
        },
      },
    );
    children.push(['client', client, 'stop']);
    manifest.stages.push(
      await client.wait(
        (v) => v.stage === 'client-ready',
        'native device registration',
      ),
    );
    const ticket = await client.request({ op: 'ticket' });
    assert.ok(ticket.handle);
    await probe(
      'wrong-origin',
      valid,
      { handle: ticket.handle, origin: 'https://wrong.example.com' },
      'origin',
    );
    await probe(
      'admin-entry',
      valid,
      {
        handle: ticket.handle,
        url: valid.event.admin.replace('https:', 'wss:') + '/net/v1/ws',
        origin: valid.event.admin,
      },
      'admin',
    );
    for (const mode of ['wrong-ca', 'wrong-name']) {
      const edge = await startEdge(mode);
      await probe(
        mode,
        edge,
        { handle: ticket.handle, origin: edge.event.portal },
        mode,
      );
      await stopChild(mode, edge.edge, 'stop', manifest.stages);
    }
    await probe(
      'invalid-ticket',
      valid,
      { handle: ticket.handle, invalid: true },
      'auth',
    );
    const positive = await probe(
      'verified-ready',
      valid,
      { handle: ticket.handle, ready: true },
      'ready',
    );
    const snapshot = (await gateway.request({ op: 'snapshot' })).tables;
    const anchor = snapshot['iam.network_connection_anchor'].filter(
      (v) => v.ingress_key === ticket.ingress,
    );
    assert.equal(anchor.length, 1);
    assert.equal(anchor[0].connection_decoded.ingress, ticket.ingress);
    assert.equal(positive.after.decisions.at(-1).clientType, 'websocket');
    assert.equal(
      anchor[0].connection_decoded.server_id,
      positive.after.decisions.at(-1).serverId,
    );
    assert.equal(
      anchor[0].connection_decoded.client_id,
      String(positive.after.decisions.at(-1).clientId),
    );
    assert.equal(positive.after.redeems.at(-1).ingress, ticket.ingress);
    await writeFile(
      join(artifacts, 'binding.json'),
      JSON.stringify({ anchor, ready: positive.response.readyResult }, null, 2),
    );
    manifest.checks.push('websocket-ingress-binding');
    await probe('replayed-ticket', valid, { handle: ticket.handle }, 'auth');
    const replaySnapshot = (await gateway.request({ op: 'snapshot' })).tables;
    assert.deepEqual(
      replaySnapshot['iam.network_connection_anchor'],
      snapshot['iam.network_connection_anchor'],
    );
    manifest.completed = true;
  } catch (error) {
    manifest.failurePhase = phase;
    manifest.failures.push(record(error));
  } finally {
    for (const [name, child, command] of [...children].reverse()) {
      if (child.exit) continue;
      try {
        await stopChild(name, child, command, manifest.stages);
      } catch (e) {
        manifest.failures.push(record(e));
      }
    }
    if (java && ready) {
      if (!java.events.some((v) => v.stage === 'closed'))
        manifest.failures.push({ message: 'Java lifecycle closure missing' });
      try {
        await access(dirname(ready.config));
        manifest.failures.push({ message: 'private root survived' });
      } catch (e) {
        if (e.code !== 'ENOENT') manifest.failures.push(record(e));
      }
    }
    for (const [name, child] of children) {
      const captured = child.lines.join('\n') + child.errors.join('');
      if (secrets.some((s) => s && captured.includes(s)))
        manifest.failures.push({ message: name + ' leaked private material' });
      else await writeFile(join(artifacts, name + '.log'), captured);
    }
    manifest.completed &&= manifest.failures.length === 0;
    manifest.driverSha256 = createHash('sha256')
      .update(await readFile(fileURLToPath(import.meta.url)))
      .digest('hex');
    await writeFile(
      join(artifacts, 'manifest.json'),
      JSON.stringify(manifest, null, 2),
    );
  }
  if (!manifest.completed)
    throw new Error('edge WSS failed; evidence: ' + artifacts);
  return { artifacts, checks: manifest.checks };
}
if (
  process.argv[1] &&
  resolve(process.argv[1]) === fileURLToPath(import.meta.url)
) {
  if (process.argv.slice(2).join(' ') !== '--run')
    throw new Error('explicit --run required; real PG/Broker/edge');
  console.log(JSON.stringify(await runEdgeWSS()));
}
