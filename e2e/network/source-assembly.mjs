// Explicit opt-in integration driver. It is never silently skipped by `test`.
import assert from 'node:assert/strict';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { createHash, randomUUID } from 'node:crypto';
import { access, mkdir, mkdtemp, readFile, writeFile } from 'node:fs/promises';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { createNatsBinaryPreparer } from '../../clients/dsh-plugin/test/network/transport/support/nats-binary.js';
import { childProcess, javaClasspath } from './support/assembly-process.mjs';

const run = promisify(execFile);
const root = resolve(dirname(fileURLToPath(import.meta.url)), '../..');

export async function runAssembly() {
  const head = (
    await run('git', ['rev-parse', 'HEAD'], { cwd: root, windowsHide: true })
  ).stdout.trim();
  assert.match(head, /^[0-9a-f]{40}$/);
  const classpath = await javaClasspath(root);
  const artifactRoot = join(root, 'e2e/network/target');
  await mkdir(artifactRoot, { recursive: true });
  const binary = await createNatsBinaryPreparer({
    stageRoot: join(artifactRoot, 'nats-cache'),
  })();
  const artifacts = await mkdtemp(join(artifactRoot, 'assembly-'));
  const executable = join(
    artifacts,
    process.platform === 'win32' ? 'gateway-assembly.exe' : 'gateway-assembly',
  );
  const build = await run(
    'go',
    ['build', '-o', executable, './cmd/network-source-assembly'],
    {
      cwd: join(root, 'infra/ai-gateway'),
      windowsHide: true,
      maxBuffer: 4 * 1024 * 1024,
    },
  );
  await writeFile(join(artifacts, 'build.log'), build.stdout + build.stderr);
  const manifest = {
    head,
    brokerVersion: '2.15.1',
    node: process.version,
    fixture: 'native-only; no real DSH or SEND',
    stages: [],
    checks: [],
    completed: false,
  };
  const secrets = [];
  let java, gateway, plugin, ready, config, timer;
  const failures = [];
  try {
    const javaBin = process.env.JAVA_HOME
      ? join(
          process.env.JAVA_HOME,
          'bin',
          process.platform === 'win32' ? 'java.exe' : 'java',
        )
      : 'java';
    java = childProcess(
      javaBin,
      ['-cp', classpath, 'auriga.iam.NetworkSourceJavaHost'],
      { cwd: root },
    );
    ready = await java.wait((v) => v.stage === 'ready', 'Java bridge ready');
    assert.equal(ready.pid, java.child.pid);
    assert.equal(ready.parentPid, process.pid);
    manifest.stages.push(ready);
    config = JSON.parse(await readFile(ready.config, 'utf8'));
    assert.equal(
      'sha256:' +
        createHash('sha256')
          .update(await readFile(config.rootPublicFile))
          .digest('hex'),
      config.rootFingerprint,
      'bridge root pin mismatch',
    );
    const collectSecrets = async () => {
      for (const path of [
        config.gatewayToken,
        config.collabToken,
        config.protocolSigning.privateKeyFile,
      ]) {
        const value = await readFile(path, 'utf8');
        secrets.push(value.trim());
        if (value.includes('PRIVATE KEY'))
          secrets.push(value.replace(/-----[^-]+-----|\s/g, ''));
      }
      for (const path of [config.device, config.externalDevice]) {
        const device = JSON.parse(await readFile(path, 'utf8'));
        secrets.push(device.access_token, device.refresh_token);
      }
    };
    await collectSecrets();
    let refreshing = Promise.resolve();
    const refresh = () =>
      (refreshing = refreshing.then(async () => {
        const index = java.events.length;
        java.write('refresh');
        await java.wait(
          (v) => java.events.indexOf(v) >= index && v.stage === 'refreshed',
          'Java refresh',
        );
        await collectSecrets();
      }));
    timer = setInterval(() => {
      void refresh().catch((e) => failures.push(e));
    }, 180_000);
    const readyFile = join(artifacts, 'java-ready.json');
    await writeFile(readyFile, JSON.stringify(ready));
    gateway = childProcess(executable, [], {
      cwd: root,
      env: {
        ...process.env,
        AURIGA_ASSEMBLY_READY: readyFile,
        AURIGA_NATS_SERVER: binary,
      },
    });
    const gw = await gateway.wait(
      (v) => v.stage === 'gateway-ready',
      'gateway/Broker ready',
      30_000,
    );
    manifest.stages.push(gw);
    const combinedCa = join(dirname(ready.config), 'node-ca.pem');
    await writeFile(
      combinedCa,
      (await readFile(ready.ca)) + '\n' + (await readFile(gw.ca)),
      { mode: 0o600, flag: 'wx' },
    );
    const pluginConfig = join(dirname(ready.config), 'plugin-driver.json');
    await writeFile(
      pluginConfig,
      JSON.stringify({ ready, config, gateway: gw, artifacts }),
      { mode: 0o600, flag: 'wx' },
    );
    plugin = childProcess(
      process.execPath,
      [join(root, 'e2e/network/support/assembly-plugin.mjs')],
      {
        cwd: root,
        env: {
          ...process.env,
          NODE_EXTRA_CA_CERTS: combinedCa,
          AURIGA_ASSEMBLY_PLUGIN: pluginConfig,
        },
      },
    );
    const handled = new Set();
    const relayTimer = setInterval(() => {
      for (const event of plugin.events) {
        if (event.stage !== 'control' || handled.has(event.id)) continue;
        handled.add(event.id);
        void gateway
          .request(event.command, 20_000)
          .then((response) => {
            plugin.write({ ...response, id: event.id });
          })
          .catch(() => {
            try {
              plugin.write({
                stage: 'reply',
                id: event.id,
                error: 'gateway control failed',
              });
            } catch {
              /* The owned plugin may already have exited. */
            }
          });
      }
    }, 10);
    try {
      const verdict = await plugin.wait(
        (v) => v.stage === 'verified' || v.stage === 'failed',
        'production plugin matrix',
        360_000,
      );
      if (verdict.stage === 'failed') {
        manifest.businessFailure = verdict;
        throw new Error('plugin matrix failed: ' + verdict.message);
      }
      manifest.checks = verdict.checks;
      await plugin.stop('stop');
      manifest.completed = true;
    } finally {
      clearInterval(relayTimer);
    }
    clearInterval(timer);
    await refreshing;
    if (failures.length) throw failures[0];
  } catch (error) {
    failures.push(error);
    manifest.failure = { name: error.name, message: error.message };
  } finally {
    clearInterval(timer);
    for (const [name, child, command] of [
      ['plugin', plugin, 'stop'],
      ['gateway', gateway, { id: 'cleanup', op: 'stop' }],
      ['java', java, 'stop'],
    ]) {
      if (!child) continue;
      try {
        await child.stop(command);
        manifest.stages.push({ stage: name + '-closed' });
      } catch (error) {
        failures.push(error);
      }
      const captured = child.lines.join('\n') + child.errors.join('');
      if (secrets.some((secret) => secret && captured.includes(secret))) {
        failures.push(new Error(`${name} output leaked private material`));
      } else {
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
    if (java && ready) {
      if (!java.events.some((v) => v.stage === 'closed'))
        failures.push(new Error('Java did not verify lifecycle cleanup'));
      try {
        await access(dirname(ready.config));
        failures.push(new Error('Java private root survived'));
      } catch (e) {
        if (e.code !== 'ENOENT') failures.push(e);
      }
    }
    manifest.completed &&= failures.length === 0;
    manifest.failures = failures.map((e) => ({
      name: e.name,
      message: e.message,
    }));
    manifest.driverSha256 = createHash('sha256')
      .update(await readFile(fileURLToPath(import.meta.url)))
      .digest('hex');
    await writeFile(
      join(artifacts, 'manifest.json'),
      JSON.stringify(manifest, null, 2),
    );
  }
  if (failures.length)
    throw new Error(`source assembly failed; evidence: ${artifacts}`);
  return { artifacts, checks: manifest.checks };
}

if (
  process.argv[1] &&
  resolve(process.argv[1]) === fileURLToPath(import.meta.url)
) {
  if (process.argv.slice(2).join(' ') !== '--run')
    throw new Error(
      'explicit --run required; real isolated PG/Broker are started',
    );
  console.log(JSON.stringify(await runAssembly()));
}
