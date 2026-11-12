// Run from repository root after npm ci using deploy/compose/real-dsh's exact lock.
// Boots disposable real CLI processes on OS-assigned loopback ports and terminates them.
import assert from 'node:assert/strict';
import { createServer } from 'node:net';
import { createServer as createHttpServer } from 'node:http';
import { generateKeyPairSync } from 'node:crypto';
import { spawn } from 'node:child_process';
import { readFile, writeFile, mkdir, rm } from 'node:fs/promises';
import { resolve, join } from 'node:path';
import { pathToFileURL } from 'node:url';
import { setTimeout as delay } from 'node:timers/promises';
const evidence = resolve(process.argv[2] ?? '.agent-evidence/base-tools-r2');
const installation = resolve(process.argv[3] ?? join(evidence, 'host-install'));
const cli = join(installation, 'node_modules/@deepseek-ai/dsh/lib/bin.js');
const plugin = pathToFileURL(resolve('clients/dsh-plugin/src/plugin.js')).href;
const probe = pathToFileURL(
  resolve('clients/dsh-plugin/test/real-host/base-tools-probe.mjs'),
).href;
await mkdir(evidence, { recursive: true });
const pkg = JSON.parse(
  await readFile(
    join(installation, 'node_modules/@deepseek-ai/dsh/package.json'),
    'utf8',
  ),
);
assert.equal(pkg.version, '0.2.1-rc.3');
const reachableCalls = [];
const localHq = createHttpServer((req, res) => {
  reachableCalls.push({
    method: req.method,
    path: req.url,
    hasAuth: !!req.headers.authorization,
    hasCookie: !!req.headers.cookie,
  });
  if (req.url !== '/api/iam/v1/login/providers') {
    res.writeHead(404);
    res.end();
    return;
  }
  res.writeHead(200, { 'content-type': 'application/json' });
  res.end(JSON.stringify({ items: [] }));
});
await new Promise((done) => localHq.listen(0, '127.0.0.1', done));
const reachableOrigin = `http://127.0.0.1:${localHq.address().port}`;
try {
  const cases = [];
  const unused = createServer();
  await new Promise((done) => unused.listen(0, '127.0.0.1', done));
  const unreachableOrigin = `http://127.0.0.1:${unused.address().port}`;
  await new Promise((done) => unused.close(done));
  for (const scenario of [
    'unconfigured',
    'unreachable',
    'reachable',
    'managed-no-device',
  ]) {
    const home = join(evidence, `home-${scenario}`),
      resultPath = join(evidence, `${scenario}.json`),
      logPath = join(evidence, `${scenario}.raw.log`);
    await mkdir(home, { recursive: true });
    await rm(resultPath, { force: true });
    const entries = [
      {
        id: 'auriga-dsh',
        name: plugin,
        config: ['unreachable', 'reachable'].includes(scenario)
          ? {
              connect: {
                platformOrigin:
                  scenario === 'reachable'
                    ? reachableOrigin
                    : unreachableOrigin,
                issuer:
                  scenario === 'reachable'
                    ? reachableOrigin
                    : unreachableOrigin,
                providerId: 'team',
                clientId: 'auriga-dsh',
                allowLocalHttp: true,
              },
            }
          : scenario === 'managed-no-device'
            ? { browserOrigin: { scheme: 'http', host: '127.0.0.1', port: 80 } }
            : {},
      },
      { id: 'base-probe', name: probe },
    ];
    const patch = join(home, 'probe.patch.yml');
    await writeFile(patch, JSON.stringify([{ insert: entries }], null, 2));
    const isolatedEnv = Object.fromEntries(
      Object.entries(process.env).filter(([key]) => !key.startsWith('AURIGA_')),
    );
    if (scenario === 'managed-no-device') {
      const { publicKey } = generateKeyPairSync('ed25519');
      const keyFile = join(home, 'identity-public.json'),
        tokenFile = join(home, 'test-instance-token');
      await writeFile(
        keyFile,
        JSON.stringify({
          keys: [
            {
              ...publicKey.export({ format: 'jwk' }),
              kid: 'test',
              alg: 'EdDSA',
              use: 'sig',
            },
          ],
        }),
      );
      await writeFile(tokenFile, 'test-only-instance-token');
      Object.assign(isolatedEnv, {
        AURIGA_IDENTITY_KEYS_FILE: keyFile,
        AURIGA_INSTANCE_TOKEN_FILE: tokenFile,
        AURIGA_INSTANCE_ID: '01936b1e-0000-7000-8000-000000000001',
        AURIGA_OWNER_ID: '01936b1e-0000-7000-8000-0000000000a1',
        AURIGA_TENANT_ID: '01936b1e-0000-7000-8000-0000000000b1',
        AURIGA_PLATFORM_URL: unreachableOrigin,
        AURIGA_WORKSPACE_ROOT: home,
      });
    }
    let log = '';
    const child = spawn(
      process.execPath,
      [
        cli,
        'web',
        '--patch',
        patch,
        '--no-open',
        '--host',
        '127.0.0.1',
        '--port',
        '0',
      ],
      {
        cwd: process.cwd(),
        env: {
          ...isolatedEnv,
          DSH_HOME: home,
          AURIGA_MODE: scenario === 'managed-no-device' ? 'managed' : 'connect',
          AURIGA_BASE_PROBE_RESULT: resultPath,
          AURIGA_BASE_PROBE_WORKSPACE: home,
        },
        windowsHide: true,
        stdio: ['ignore', 'pipe', 'pipe'],
      },
    );
    child.stdout.on('data', (chunk) => {
      log += chunk;
    });
    child.stderr.on('data', (chunk) => {
      log += chunk;
    });
    let exit;
    child.on('exit', (code, signal) => {
      exit = { code, signal };
    });
    try {
      const deadline = Date.now() + 60000;
      let result;
      for (;;) {
        try {
          result = JSON.parse(await readFile(resultPath, 'utf8'));
          break;
        } catch (error) {
          if (error.code !== 'ENOENT' && !(error instanceof SyntaxError))
            throw error;
        }
        if (exit) throw new Error(`host exited: ${JSON.stringify(exit)}`);
        if (Date.now() > deadline)
          throw new Error(`host probe timed out: ${scenario}`);
        await delay(100);
      }
      assert.equal(result.ok, true, JSON.stringify(result));
      assert.deepEqual(result.modelVisibleTools.sort(), [
        'auriga_net_diagnose',
        'auriga_net_identity',
        'auriga_net_inbox',
        'auriga_net_status',
      ]);
      assert.equal(result.identity.loggedIn, false);
      if (scenario === 'reachable') {
        assert.equal(result.after.headquarters.reachable, true);
        assert.equal(result.after.headquarters.reason, null);
        assert.ok(result.after.headquarters.lastSuccessAt);
        assert.deepEqual(
          result.diagnose.segments.slice(0, 3).map((s) => s.status),
          ['ready', 'ready', 'ready'],
        );
        assert.equal(reachableCalls.length, 2);
        assert.ok(
          reachableCalls.every(
            (r) =>
              r.method === 'GET' &&
              r.path === '/api/iam/v1/login/providers' &&
              !r.hasAuth &&
              !r.hasCookie,
          ),
        );
      } else if (scenario !== 'unreachable') {
        assert.equal(result.initial.headquarters.reason, 'unconfigured');
        assert.equal(result.diagnose.segments[1].reason, 'unconfigured');
      } else {
        assert.equal(result.after.headquarters.reason, 'hq_unreachable');
        assert.equal(result.after.sending.reason, 'hq_unreachable');
        assert.equal(result.diagnose.firstFailure, 'ingress');
        assert.equal(result.diagnose.segments[1].reason, 'hq_unreachable');
      }
      cases.push({ scenario, ...result });
    } finally {
      if (!exit) {
        child.kill();
        await Promise.race([
          new Promise((done) => child.once('exit', done)),
          delay(5000),
        ]);
      }
      if (!exit) child.kill('SIGKILL');
      await writeFile(logPath, log);
    }
  }
  await writeFile(
    join(evidence, 'summary.json'),
    JSON.stringify({ ok: true, version: pkg.version, cases }, null, 2),
  );
  console.log(
    JSON.stringify({
      ok: true,
      version: pkg.version,
      scenarios: cases.map((c) => c.scenario),
    }),
  );
} finally {
  localHq.closeAllConnections();
  await new Promise((done) => localHq.close(done));
}
