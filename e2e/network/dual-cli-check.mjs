// Independent host/control precondition only. No platform login or SEND claimed.
import assert from 'node:assert/strict';
import { mkdir, mkdtemp, writeFile, readFile, rm } from 'node:fs/promises';
import { resolve, join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';
import { generateKeyPairSync } from 'node:crypto';
import { launchCli } from './support/dual-cli.mjs';
import { defaultRuntime } from './support/native-install.mjs';
import { createDualProxy } from './support/dual-proxy.mjs';
const root = resolve(dirname(fileURLToPath(import.meta.url)), '../..');
const args = process.argv.slice(2).join(' ');
assert.ok(
  ['--run', '--run --connect', '--run --connect --diagnostic'].includes(args),
);
const diagnostic = args === '--run --connect --diagnostic';
const configured = args.includes('--connect');
const scope = diagnostic
  ? 'real CLI login failure diagnostics: trusted HTTPS upstream 503 versus untrusted TLS; no IAM login, PG or SEND'
  : configured
    ? 'two configured CLI profiles: settings active and login status 200 only; no login, PG or SEND'
    : 'full CLI isolation/control only; no login, PG or SEND';
await mkdir(join(root, 'e2e/network/target'), { recursive: true });
const artifacts = await mkdtemp(
  join(root, 'e2e/network/target/dual-cli-check-'),
);
const hosts = [],
  evidence = [],
  failures = [];
let proxy, untrusted;
try {
  let roots;
  if (configured) {
    // No backend is attached and no login is attempted. This supplies a real
    // random HTTPS installation origin/CA, never fake authenticated responses.
    proxy = await createDualProxy(join(artifacts, 'private/tls'));
    if (diagnostic)
      untrusted = await createDualProxy(join(artifacts, 'private/untrusted'));
    roots = [
      {
        kid: 'profile-check-root',
        public_key_spki: generateKeyPairSync('ed25519')
          .publicKey.export({ type: 'spki', format: 'der' })
          .toString('base64url'),
      },
    ];
  }
  for (const name of ['A', 'B']) {
    const h = await launchCli({
      root,
      state: join(artifacts, 'private', name),
      runtime: process.env.AURIGA_DSH_RUNTIME ?? defaultRuntime,
      artifacts,
      name,
      ...(configured
        ? {
            ca: diagnostic && name === 'B' ? untrusted.ca : proxy.ca,
            connect: {
              platformOrigin: proxy.origin,
              issuer: proxy.origin,
              clientId: 'bridge-device',
              providerId: 'example-provider',
              deviceName: 'Example ' + name,
              network: {
                realm: '9223372036854775810',
                roots,
                wssUrl:
                  proxy.origin.replace('https:', 'wss:') + '/nats/' + name,
              },
            },
          }
        : {}),
    });
    hosts.push(h);
    if (configured) {
      assert.equal(h.readiness.status.available, true);
      assert.equal(h.readiness.status.loggedIn, false);
      assert.equal(h.readiness.settings.activeOrigin, proxy.origin);
      // The shutdown IPC must not weaken production native HTTP admission.
      await assert.rejects(
        h.control({ op: 'capabilities' }),
        /\/send4\/control: 401/,
      );
      evidence.push({
        name,
        pid: h.pid,
        profile: h.profile,
        readiness: h.readiness,
      });
      if (diagnostic) {
        const status = await h.request('/auriga/local-login/status');
        await assert.rejects(
          h.request(
            '/auriga/local-login/login',
            { action: 'begin' },
            status.csrf,
          ),
          /\/auriga\/local-login\/login: 503/,
        );
      }
      continue;
    }
    const created = await h.control({ op: 'create', id: 'send4-target' });
    const record = await h.control({ op: 'inspect', session: 'send4-target' });
    assert.ok(created.current && created.sessionCurrent && created.ordinary);
    assert.equal(record.pid, h.pid);
    assert.equal(record.parentPid, process.pid);
    assert.equal(record.record.meta.cwd, join(h.state, 'session-cwd'));
    evidence.push({ name, pid: h.pid, profile: h.profile, created, record });
  }
  assert.notEqual(hosts[0].pid, hosts[1].pid);
  assert.notEqual(hosts[0].profile, hosts[1].profile);
} catch (error) {
  failures.push(error);
} finally {
  for (const host of hosts.reverse())
    try {
      await host.stop();
    } catch (error) {
      failures.push(error);
    }
  try {
    await proxy?.close();
    await untrusted?.close();
  } catch (error) {
    failures.push(error);
  }
  if (diagnostic)
    for (const entry of evidence) {
      try {
        const log = await readFile(
          join(artifacts, entry.name + '-host.log'),
          'utf8',
        );
        const lines = [...log.matchAll(/local route failure (\{[^\r\n]+\})/g)];
        assert.equal(lines.length, 1);
        const value = JSON.parse(lines[0][1]);
        assert.equal(value.operation, 'begin');
        assert.equal(
          value.category,
          entry.name === 'A' ? 'platform_http' : 'tls',
        );
        if (entry.name === 'A') assert.equal(value.status, 503);
        else assert.equal(value.code, 'DEPTH_ZERO_SELF_SIGNED_CERT');
        entry.diagnostic = value;
      } catch (error) {
        failures.push(error);
      }
    }
  await writeFile(
    join(artifacts, 'evidence.json'),
    JSON.stringify(
      {
        scope,
        evidence,
        failures: failures.map((v) => v.message),
      },
      null,
      2,
    ),
  );
  const owned = resolve(artifacts, 'private');
  assert.equal(dirname(owned), resolve(artifacts));
  await rm(owned, { recursive: true, force: true });
}
if (failures.length)
  throw new AggregateError(
    failures,
    'CLI precondition failed; evidence: ' + artifacts,
  );
console.log(
  JSON.stringify({
    artifacts,
    hosts: 2,
    scope,
  }),
);
