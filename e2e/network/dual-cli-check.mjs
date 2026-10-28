// Independent host/control precondition only. No platform login or SEND claimed.
import assert from 'node:assert/strict';
import { mkdir, mkdtemp, writeFile, rm } from 'node:fs/promises';
import { resolve, join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';
import { launchCli } from './support/dual-cli.mjs';
import { defaultRuntime } from './support/native-install.mjs';
const root = resolve(dirname(fileURLToPath(import.meta.url)), '../..');
assert.equal(process.argv.slice(2).join(' '), '--run');
await mkdir(join(root, 'e2e/network/target'), { recursive: true });
const artifacts = await mkdtemp(
  join(root, 'e2e/network/target/dual-cli-check-'),
);
const hosts = [],
  evidence = [],
  failures = [];
try {
  for (const name of ['A', 'B']) {
    const h = await launchCli({
      root,
      state: join(artifacts, 'private', name),
      runtime: process.env.AURIGA_DSH_RUNTIME ?? defaultRuntime,
      artifacts,
      name,
    });
    hosts.push(h);
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
  await writeFile(
    join(artifacts, 'evidence.json'),
    JSON.stringify(
      {
        scope: 'full CLI isolation/control only; no login, PG or SEND',
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
    scope: 'CLI only, not SEND acceptance',
  }),
);
