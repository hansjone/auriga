import assert from 'node:assert/strict';
import {
  mkdtemp,
  mkdir,
  readFile,
  writeFile,
  access,
  rm,
} from 'node:fs/promises';
import { join, dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { createHash } from 'node:crypto';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { setInterval, clearInterval } from 'node:timers';
import {
  childProcess,
  stopChild,
  waitUntil,
} from './support/assembly-process.mjs';
import { prepareDual } from './support/dual-prepare.mjs';
import { createDualProxy } from './support/dual-proxy.mjs';
import { launchCli } from './support/dual-cli.mjs';
import { installNativeRuntime, hostPin } from './support/native-install.mjs';
import { createNatsBinaryPreparer } from '../../clients/dsh-plugin/test/network/transport/support/nats-binary.js';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '../..');
const run = promisify(execFile);
const save = (file, value) => writeFile(file, JSON.stringify(value, null, 2));

export async function runDualDsh() {
  const head = (
    await run('git', ['rev-parse', 'HEAD'], { cwd: root, windowsHide: true })
  ).stdout.trim();
  const target = join(root, 'e2e/network/target');
  await mkdir(target, { recursive: true });
  const artifacts = await mkdtemp(join(target, 'dual-dsh-'));
  const manifest = {
    head,
    host: hostPin,
    scope:
      'two independent full CLI processes, normal native login and receive control; no model conversation',
    stages: [],
    checks: [],
    completed: false,
    failures: [],
  };
  const hosts = [],
    secrets = [],
    failures = [];
  let java,
    gateway,
    proxy,
    ready,
    timer,
    phase = 'prepare';
  let refresh = Promise.resolve();
  const check = () => {
    if (proxy?.failures.length) throw proxy.failures[0];
  };
  try {
    const built = await prepareDual(root, artifacts);
    const runtime =
      process.env.AURIGA_DSH_RUNTIME ??
      (await installNativeRuntime(join(artifacts, 'native-runtime')));
    const binary = await createNatsBinaryPreparer({
      stageRoot: join(target, 'nats-cache'),
    })();
    proxy = await createDualProxy(join(artifacts, 'proxy-private'));
    const publicPort = new URL(proxy.origin).port;
    const entry = JSON.parse(
      await readFile(
        join(root, 'contracts/deployment/fixtures/ip-http.json'),
        'utf8',
      ),
    );
    entry.portalOrigin = {
      scheme: 'https',
      host: '127.0.0.1',
      port: Number(publicPort),
    };
    entry.issuerOrigin = entry.portalOrigin;
    entry.adminOrigin = { scheme: 'https', host: '127.0.0.1', port: 1 };
    entry.listeners.portal = {
      address: '127.0.0.1',
      port: Number(publicPort),
      tls: true,
    };
    entry.listeners.admin = { address: '127.0.0.1', port: 1, tls: true };
    entry.listeners.instanceControl = {
      address: '127.0.0.1',
      port: 2,
      tls: false,
    };
    entry.instanceControl.origin.port = 2;
    const accessFile = join(artifacts, 'access.json');
    await save(accessFile, entry);
    phase = 'Java normal-login assembly';
    const javaBin = join(
      process.env.JAVA_HOME,
      'bin',
      process.platform === 'win32' ? 'java.exe' : 'java',
    );
    const javaTemporary = join(artifacts, 'java-private');
    await mkdir(javaTemporary);
    java = childProcess(
      javaBin,
      [
        '-Djava.io.tmpdir=' + javaTemporary,
        '-cp',
        built.classpath,
        'auriga.iam.DualDshLogin',
      ],
      {
        cwd: root,
        env: {
          ...process.env,
          AURIGA_SEND4_ORIGIN: proxy.origin,
          AURIGA_SEND4_ACCESS: accessFile,
        },
      },
    );
    ready = await java.wait((v) => v.stage === 'ready', 'SEND-4 Java ready');
    manifest.stages.push(ready);
    assert.equal(ready.pid, java.child.pid);
    assert.equal(ready.parentPid, process.pid);
    const config = JSON.parse(await readFile(ready.config, 'utf8'));
    const login = JSON.parse(
      await readFile(join(dirname(ready.config), 'login.json'), 'utf8'),
    );
    secrets.push(login.password);
    for (const file of [
      config.gatewayToken,
      config.collabToken,
      config.protocolSigning.privateKeyFile,
    ])
      secrets.push((await readFile(file, 'utf8')).trim());
    const readyFile = join(artifacts, 'java-ready.json');
    await save(readyFile, ready);
    gateway = childProcess(built.executable, [], {
      cwd: root,
      env: {
        ...process.env,
        AURIGA_ASSEMBLY_READY: readyFile,
        AURIGA_NATS_SERVER: binary,
        AURIGA_SEND4_TEAM_CONTRACT: join(
          root,
          'contracts/plugin/team-mcp.schema.json',
        ),
      },
    });
    const team = await gateway.wait(
      (v) => v.stage === 'team-ready',
      'real Team ready',
      30000,
    );
    const gw = await gateway.wait(
      (v) => v.stage === 'gateway-ready',
      'real gateway/Broker ready',
      30000,
    );
    manifest.stages.push(team, gw);
    await proxy.attach({ backend: ready.https, gateway: gw, team: team.url });
    const ca = join(dirname(ready.config), 'send4-ca.pem');
    await writeFile(
      ca,
      (await readFile(proxy.ca)) + '\n' + (await readFile(gw.ca)),
    );
    timer = setInterval(() => {
      refresh = refresh
        .then(async () => {
          const start = java.events.length;
          java.write('refresh');
          await java.wait(
            (v) => java.events.indexOf(v) >= start && v.stage === 'refreshed',
            'service JWT refresh',
          );
        })
        .catch((error) => failures.push(error));
    }, 180000);
    const roots = [
      {
        kid: config.rootKid,
        public_key_spki: (await readFile(config.rootPublicFile)).toString(
          'base64url',
        ),
      },
    ];
    phase = 'full CLI normal authentication';
    for (const [name, username] of [
      ['A', 'alice'],
      ['B', 'bob'],
    ]) {
      const state = join(dirname(ready.config), 'dsh-' + name);
      const host = await launchCli({
        root,
        state,
        runtime,
        ca,
        artifacts,
        name,
        connect: {
          platformOrigin: proxy.origin,
          issuer: proxy.origin,
          clientId: 'bridge-device',
          providerId: login.providerId,
          deviceName: 'Example ' + name,
          network: {
            realm: config.realm,
            roots,
            wssUrl: proxy.origin.replace('https:', 'wss:') + '/nats/' + name,
          },
        },
      });
      hosts.push(host);
      const authenticated = await host.login(username, login.password);
      await waitUntil(async () => {
        check();
        const c = await host.control({ op: 'capabilities' });
        return c.join && c.send;
      }, name + ' native production tools missing');
      const ordinary = await host.control({ op: 'create', id: 'send4-target' });
      const other = await host.control({
        op: 'create',
        id: 'send4-other',
        other: true,
      });
      assert.ok(
        ordinary.current && ordinary.sessionCurrent && ordinary.ordinary,
      );
      assert.notEqual(ordinary.cwd, other.cwd);
      const joined = await host.tool('send4-target', 'auriga_network_join');
      assert.equal(joined.isError, false);
      assert.equal(joined.value.status, 'registered');
      const ledger = await waitUntil(async () => {
        check();
        const s = await host.ledger();
        return s.sourceControl?.entries['1']?.observation?.source.state ===
          'active'
          ? s
          : null;
      }, name + ' source Get missing');
      assert.equal(ledger.bindings['1'].hostSessionId, 'send4-target');
      assert.equal(ledger.bindings['1'].cwd, ordinary.cwd);
      proxy.endpoints.set(name, {
        host,
        binding: ledger.sourceControl.entries['1'].head,
      });
      const processEvidence = await host.control({
        op: 'inspect',
        session: 'send4-target',
      });
      assert.equal(processEvidence.pid, host.pid);
      assert.equal(processEvidence.parentPid, process.pid);
      manifest.stages.push({
        stage: 'dsh-ready',
        name,
        pid: host.pid,
        processIdentity: {
          pid: processEvidence.pid,
          parentPid: processEvidence.parentPid,
          creationTime: processEvidence.creationTime,
          bootId: processEvidence.bootId,
          startTicks: processEvidence.startTicks,
        },
        profile: host.profile,
        dshHome: join(state, 'dsh'),
        cwd: join(state, 'cwd'),
        native: ordinary,
        other,
        authenticated,
      });
    }
    const [A, B] = hosts;
    assert.notEqual(A.pid, B.pid);
    assert.notEqual(A.profile, B.profile);
    assert.notEqual(A.state, B.state);
    const identities = await Promise.all(hosts.map((h) => h.ledger()));
    assert.equal(identities[0].identity.tenant, identities[1].identity.tenant);
    assert.notEqual(identities[0].identity.node, identities[1].identity.node);
    manifest.checks.push(
      'SEND-4: independent full rc.3 CLI profiles, normal production login, native ToolRuntime join and authenticated source Get',
    );
    const snapshot = async (label) => {
      check();
      const s = (await gateway.request({ op: 'snapshot' }, 20000)).tables;
      await save(join(artifacts, label + '.json'), s);
      return s;
    };
    const address = async (h) =>
      (await h.request('/auriga/local-login/messaging/snapshot')).sessions.find(
        (v) => v.local === '1',
      ).address;
    const addresses = await Promise.all(hosts.map(address));
    for (const h of hosts) {
      const enabled = await h.receive('1', true);
      assert.equal(enabled.enabled, true);
    }
    const evidence = [];
    async function exchange(
      sender,
      recipient,
      targetAddress,
      text,
      { loseSubmit = false, loseReceipt = false } = {},
    ) {
      phase = 'SEND-4 ' + sender.name + ' -> ' + recipient.name;
      const before = await snapshot(sender.name + '-before');
      const body = { subtype: 'text', text };
      if (loseSubmit) proxy.dropSubmit();
      if (loseReceipt) proxy.dropReceipt();
      const first = await sender.tool('send4-target', 'auriga_send_message', {
        target: targetAddress,
        body,
      });
      check();
      assert.equal(first.isError, false);
      const original = Object.values(
        (await sender.ledger()).durable.submissions,
      ).find((v) => v.request.body.text === text);
      assert.ok(original, 'SEND-4 original submission required');
      if (loseSubmit) {
        assert.equal(first.value.status, 'error');
        assert.equal(original.result, null);
        assert.ok(
          proxy.drops.some(
            (v) =>
              v.kind === 'submit-response' &&
              v.request.idempotency.key === original.request.idempotency.key,
          ),
        );
        const accepted = await snapshot('lost-submit-response');
        assert.equal(
          accepted['collab.network_message'].length,
          before['collab.network_message'].length + 1,
        );
        const retry = await sender.tool('send4-target', 'auriga_send_message', {
          target: targetAddress,
          body,
        });
        check();
        assert.equal(retry.value.status, 'request_submitted');
        const attempts = proxy.submissions.filter(
          (v) => v.request.idempotency.key === original.request.idempotency.key,
        );
        assert.equal(attempts.length, 2);
        assert.equal(attempts[0].bytes, attempts[1].bytes);
      } else assert.equal(first.value.status, 'request_submitted');
      const saved = Object.values(
        (await sender.ledger()).durable.submissions,
      ).find(
        (v) => v.request.idempotency.key === original.request.idempotency.key,
      );
      const result = JSON.parse(saved.result);
      assert.equal(result.status, 'platform_stored');
      const received = await waitUntil(async () => {
        check();
        const s = await recipient.ledger();
        return Object.values(s.durable.inbound).find(
          (v) =>
            v.delivery.message.message_id === result.message_id &&
            v.stage === 'committed',
        );
      }, 'SEND-4 genuine RECEIPT_COMMITTED missing');
      const native = await recipient.control({
        op: 'inspect',
        session: 'send4-target',
      });
      assert.ok(JSON.stringify(native.record).includes(text));
      assert.ok(JSON.stringify(native.persisted).includes(text));
      assert.equal(
        native.prompts.filter(
          (v) =>
            v.sessionId === 'send4-target' &&
            v.requestId === received.recipientId,
        ).length,
        1,
        'SEND-4 accepted recipient must never prompt twice',
      );
      if (loseReceipt) {
        const drop = proxy.drops.find(
          (v) =>
            v.kind === 'receipt' &&
            v.frame.results[0].recipient_id === received.recipientId,
        );
        assert.equal(drop?.durableStage, 'accepted');
      }
      const committed = await snapshot(sender.name + '-committed');
      for (const table of [
        'network_submit',
        'network_message',
        'network_mail_item',
        'network_recipient',
      ])
        assert.equal(
          committed['collab.' + table].length,
          before['collab.' + table].length + 1,
          'SEND-4 single durable ' + table,
        );
      const row = committed['collab.network_recipient'].find(
        (v) => v.id === received.recipientId,
      );
      assert.equal(row.transport_status, 'host_accepted');
      assert.equal(row.host_request_id, received.recipientId);
      const receipt = proxy.frames.find(
        (v) =>
          v.name === recipient.name &&
          v.direction === 'down' &&
          v.frame.type === 'receipt_committed' &&
          v.frame.operation_id === received.receipt.operation_id,
      )?.frame;
      assert.ok(receipt, 'SEND-4 signed receipt commit frame');
      const proof = {
        from: sender.name,
        to: recipient.name,
        messageId: result.message_id,
        recipientId: received.recipientId,
        deliveryId: received.delivery.delivery_id,
        digest: received.delivery.source_context.message_digest,
        hostRequestId: row.host_request_id,
        request: original.request,
        result: saved.result,
        receipt,
        loseSubmit,
        loseReceipt,
      };
      if (loseSubmit)
        assert.equal(
          proxy.drops.find(
            (v) =>
              v.kind === 'submit-response' &&
              v.request.idempotency.key === original.request.idempotency.key,
          ).result,
          saved.result,
        );
      evidence.push(proof);
    }
    await exchange(
      A,
      B,
      addresses[1],
      'SEND-4 A to B: durable native acceptance before dropped receipt',
      { loseReceipt: true },
    );
    manifest.checks.push(
      'SEND-4: A to B real WSS/FETCH/attempt/native provider/PG receipt, lost receipt without a second prompt',
    );
    await exchange(
      B,
      A,
      addresses[0],
      'SEND-4 B to A: committed submit response lost and original same-key retry',
      { loseSubmit: true },
    );
    manifest.checks.push(
      'SEND-4: B to A real ToolRuntime/HTTPS/PG/native receipt, lost submit response recovers identical key/result and single rows',
    );
    phase = 'receive closed and non-target zero input';
    await B.receive('1', false);
    const before = await snapshot('receive-off-before');
    const oldPrompts = (
      await B.control({ op: 'inspect', session: 'send4-target' })
    ).prompts.length;
    const oldFetch = proxy.frames.filter(
      (v) =>
        v.name === 'B' &&
        v.direction === 'down' &&
        v.frame.type === 'fetch_result',
    ).length;
    const rejected = await A.tool('send4-target', 'auriga_send_message', {
      target: addresses[1],
      body: {
        subtype: 'text',
        text: 'SEND-4 receiving disabled must not deliver',
      },
    });
    check();
    assert.equal(rejected.value.status, 'error');
    await waitUntil(() => {
      check();
      return (
        proxy.frames.filter(
          (v) =>
            v.name === 'B' &&
            v.direction === 'down' &&
            v.frame.type === 'fetch_result',
        ).length >=
        oldFetch + 2
      );
    }, 'SEND-4 fresh FETCH proof missing');
    const after = await snapshot('receive-off-after');
    for (const table of [
      'network_message',
      'network_mail_item',
      'network_recipient',
    ])
      assert.equal(
        after['collab.' + table].length,
        before['collab.' + table].length,
        'SEND-4 receive-off must not create ' + table,
      );
    assert.equal(
      (await B.control({ op: 'inspect', session: 'send4-target' })).prompts
        .length,
      oldPrompts,
      'SEND-4 disabled session must receive zero inputs',
    );
    for (const h of hosts) {
      const other = await h.control({ op: 'inspect', session: 'send4-other' });
      assert.equal(
        other.prompts.filter((v) => v.sessionId === 'send4-other').length,
        0,
      );
      assert.ok(!JSON.stringify(other.persisted).includes('SEND-4'));
      const native = await h.control({
        op: 'inspect',
        session: 'send4-target',
      });
      for (const event of native.events.filter((v) => v.event === 'execute'))
        assert.ok(event.current && event.sessionCurrent);
      assert.equal(native.record.meta.cwd, join(h.state, 'session-cwd'));
      await save(join(artifacts, h.name + '-native.json'), native);
      await save(join(artifacts, h.name + '-other.json'), other);
    }
    manifest.checks.push(
      'SEND-4: explicit receive-off rejects submission, fresh FETCH and non-target native sessions prove zero extra inputs; cwd retained',
    );
    await save(join(artifacts, 'messages.json'), evidence);
    check();
    manifest.completed = true;
  } catch (error) {
    failures.push(error);
    manifest.businessFailure = {
      phase,
      name: error.name,
      code: error.code,
      message: error.message,
      completedChecks: [...manifest.checks],
    };
  } finally {
    clearInterval(timer);
    await refresh;
    for (const host of hosts.reverse())
      try {
        await host.stop();
        manifest.stages.push({
          stage: 'dsh-closed',
          name: host.name,
          exitCode: 0,
          signal: null,
        });
      } catch (error) {
        failures.push(error);
      }
    try {
      await proxy?.close();
    } catch (error) {
      failures.push(error);
    }
    for (const [name, child, command] of [
      ['gateway', gateway, { id: 'cleanup', op: 'stop' }],
      ['java', java, 'stop'],
    ]) {
      if (!child) continue;
      try {
        await stopChild(name, child, command, manifest.stages);
      } catch (error) {
        failures.push(error);
      }
      const logs = child.lines.join('\n') + child.errors.join('');
      if (secrets.some((secret) => secret && logs.includes(secret)))
        failures.push(
          new Error('private material in ' + name + ' diagnostics'),
        );
      else {
        await writeFile(
          join(artifacts, name + '.stdout.log'),
          child.lines.join('\n'),
        );
        await writeFile(
          join(artifacts, name + '.stderr.log'),
          child.errors.join(''),
        );
      }
    }
    if (ready) {
      try {
        await access(dirname(ready.config));
        failures.push(new Error('Java private root survived'));
      } catch (error) {
        if (error.code !== 'ENOENT') failures.push(error);
      }
    }
    if (proxy) {
      await save(join(artifacts, 'frames.json'), proxy.frames);
      await save(join(artifacts, 'faults.json'), proxy.drops);
    }
    for (const name of ['proxy-private', 'java-private']) {
      const owned = resolve(artifacts, name);
      assert.equal(dirname(owned), resolve(artifacts));
      try {
        await rm(owned, { recursive: true, force: true });
      } catch (error) {
        failures.push(error);
      }
    }
    manifest.completed &&= failures.length === 0;
    manifest.failures = failures.map((e) => ({
      name: e.name,
      code: e.code,
      message: e.message,
    }));
    manifest.driverSha256 = createHash('sha256')
      .update(await readFile(fileURLToPath(import.meta.url)))
      .digest('hex');
    await save(join(artifacts, 'manifest.json'), manifest);
  }
  if (failures.length) throw new Error('SEND-4 failed; evidence: ' + artifacts);
  return { artifacts, checks: manifest.checks };
}
if (
  process.argv[1] &&
  resolve(process.argv[1]) === fileURLToPath(import.meta.url)
) {
  assert.equal(
    process.argv.slice(2).join(' '),
    '--run',
    'explicit --run required',
  );
  console.log(JSON.stringify(await runDualDsh()));
}
