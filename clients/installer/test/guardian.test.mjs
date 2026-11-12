import assert from 'node:assert/strict';
import { test } from 'node:test';
import { createRequire } from 'node:module';
import {
  existsSync,
  mkdirSync,
  readFileSync,
  readdirSync,
  writeFileSync,
} from 'node:fs';
import { join } from 'node:path';
import { spawn } from 'node:child_process';
import { tempBase } from './helpers.mjs';
import { mergeSkillPatch } from '../runtime/skill-config.mjs';
import {
  acquireHostLock,
  durableJson,
  evaluateHostLock,
  hostTime,
} from '../runtime/host-lock.mjs';
import {
  assertUnmanagedStart,
  checkLease,
  DesktopGuardian,
  guardianTransition,
} from '../runtime/guardian.mjs';

const require = createRequire(import.meta.url);
const load = createRequire(require.resolve('eslint/package.json'))(
  'js-yaml',
).load;
// The fixture expression is inert scalar data, never executable code.
const parse = (s) =>
  load(
    s.replace(/!!js ([^\r\n]+)/g, (_, expression) =>
      JSON.stringify(expression),
    ),
  );
const contract = (name) =>
  JSON.parse(
    readFileSync(
      new URL(`../../../contracts/resource/${name}`, import.meta.url),
    ),
  );
const fixtures = contract('cordis-upgrade-fixture.json');
for (const item of fixtures.cases)
  test(`Cordis fixture: ${item.id}`, () => {
    const input = readFileSync(
      new URL(`../../../contracts/resource/${item.inputPath}`, import.meta.url),
      'utf8',
    );
    const expected = readFileSync(
      new URL(
        `../../../contracts/resource/${item.expectedPath}`,
        import.meta.url,
      ),
      'utf8',
    );
    if (!item.expectShared)
      assert.throws(() => mergeSkillPatch(input, parse, process.cwd()));
    else {
      assert.equal(mergeSkillPatch(input, parse, process.cwd()), expected);
      assert.equal(mergeSkillPatch(expected, parse, process.cwd()), expected);
    }
  });

for (const item of contract('host-lock-fixtures.json').cases)
  test(`lock fixture: ${item.id}`, () => {
    assert.deepEqual(evaluateHostLock(item.input), item.expect);
  });

const protocol = contract('guardian-transitions.json');
for (const item of protocol.cases)
  test(`transition: ${item.id}`, () => {
    if (item.expect === 'reject')
      assert.throws(() =>
        guardianTransition(protocol, item.from, item.event, item.facts),
      );
    else
      assert.equal(
        guardianTransition(protocol, item.from, item.event, item.facts).to,
        item.expect,
      );
  });

test('real boot clock is shared across processes', async () => {
  const parent = hostTime();
  const module = new URL('../runtime/host-lock.mjs', import.meta.url).href;
  const child = spawn(process.execPath, [
    '--input-type=module',
    '-e',
    `import {hostTime} from ${JSON.stringify(module)}; console.log(JSON.stringify(hostTime()))`,
  ]);
  let output = '';
  child.stdout.on('data', (b) => {
    output += b;
  });
  const code = await new Promise((resolve, reject) => {
    child.on('error', reject);
    child.on('exit', resolve);
  });
  assert.equal(code, 0);
  const other = JSON.parse(output);
  assert.equal(other.bootId, parent.bootId);
  assert(other.monotonicMillis >= parent.monotonicMillis);
});

test('real concurrent mkdir grants one owner only', async (t) => {
  const home = tempBase(t);
  const module = new URL('../runtime/host-lock.mjs', import.meta.url).href;
  const program = `import {acquireHostLock} from ${JSON.stringify(module)};try { acquireHostLock(process.argv[1]); console.log('winner'); } catch(e) { console.log(e.code); }`;
  const run = () =>
    new Promise((resolve, reject) => {
      const child = spawn(process.execPath, [
        '--input-type=module',
        '-e',
        program,
        home,
      ]);
      let output = '';
      child.stdout.on('data', (b) => {
        output += b;
      });
      child.on('error', reject);
      child.on('exit', (code) =>
        code === 0 ? resolve(output.trim()) : reject(new Error(`exit ${code}`)),
      );
    });
  assert.deepEqual((await Promise.all([run(), run()])).sort(), [
    'EEXIST',
    'winner',
  ]);
});

test('lock refuses expired release and a replaced owner token', (t) => {
  const home = tempBase(t);
  let monotonicMillis = 100;
  const time = () => ({ bootId: 'boot', monotonicMillis });
  const lock = acquireHostLock(home, 'E', time);
  monotonicMillis = 30101;
  assert.throws(() => lock.release());
  monotonicMillis = 100;
  durableJson(join(home, 'auriga/guardian/host.lock.d/owner.json'), {
    ...lock.owner,
    lockId: 'other',
  });
  assert.throws(() => lock.release());
  assert(existsSync(join(home, 'auriga/guardian/host.lock.d')));
});

test('clock rollback tolerance cannot extend UTC or monotonic deadlines', () => {
  const clock = {
    bootId: 'boot',
    maxObservedTime: '2026-11-12T00:10:00Z',
    lastTrustedTime: '2026-11-12T00:10:00Z',
    lastMonotonicMillis: 1000,
    leaseDeadlineMonotonicMillis: 11000,
  };
  const lease = { expiresAt: '2026-11-12T00:10:10Z' };
  const sample = {
    bootId: 'boot',
    wallMillis: Date.parse(clock.maxObservedTime) - 120000,
    monotonicMillis: 2000,
  };
  assert(checkLease(clock, lease, sample).valid);
  assert.equal(
    checkLease(clock, lease, { ...sample, wallMillis: sample.wallMillis - 1 })
      .reason,
    'clock_rollback',
  );
  assert.equal(
    checkLease(clock, lease, { ...sample, monotonicMillis: 11000 }).reason,
    'lease_expired',
  );
  assert.equal(
    checkLease(clock, lease, { ...sample, bootId: 'new-boot' }).valid,
    false,
  );
  assert.equal(
    checkLease(clock, lease, { ...sample, monotonicMillis: 999 }).reason,
    'clock_rollback',
  );
});

function fixture(t, stopped = true) {
  const home = tempBase(t);
  const directory = join(home, 'auriga', 'guardian');
  mkdirSync(directory, { recursive: true });
  const identity = '019a0000-0000-7000-8000-000000000001';
  durableJson(join(directory, 'state.json'), {
    schemaVersion: 1,
    revision: 1,
    guardian: {
      tenantId: identity,
      ownerId: identity,
      hostId: identity,
      selectionRevision: 1,
      generation: 1,
      state: 'active',
      reason: 'startup',
      lease: { expiresAt: '2026-11-13T00:00:00Z' },
      processStopped: false,
      viewCleared: false,
      newSessionRequired: false,
    },
    clock: {},
  });
  const calls = [];
  const guardian = new DesktopGuardian(home, {
    time: () => ({ bootId: 'boot', monotonicMillis: 1 }),
    stopTree: async () => {
      calls.push('stop');
    },
    isStopped: async () => stopped,
  });
  mkdirSync(join(guardian.current, 'example'), { recursive: true });
  writeFileSync(join(guardian.current, 'example', 'SKILL.md'), 'content');
  writeFileSync(join(home, 'history.json'), 'history');
  return { guardian, calls, home, directory };
}

test('plugin-free isolation stops first and preserves history and quarantine', async (t) => {
  const f = fixture(t);
  await f.guardian.disable('revoked');
  assert.deepEqual(f.calls, ['stop']);
  assert(f.guardian.cleared());
  const state = f.guardian.read();
  assert.equal(state.guardian.state, 'private_only');
  assert.equal(state.guardian.newSessionRequired, true);
  assert.equal(readFileSync(join(f.home, 'history.json'), 'utf8'), 'history');
  const receipts = readdirSync(join(f.directory, 'disable-receipts'));
  assert.equal(receipts.length, 1);
  const receipt = JSON.parse(
    readFileSync(join(f.directory, 'disable-receipts', receipts[0])),
  );
  assert.equal(receipt.reason, 'revoked');
  assert(
    existsSync(join(f.home, receipt.quarantinePath, 'example', 'SKILL.md')),
  );
  assert.throws(() => assertUnmanagedStart(f.home));
});

test('unconfirmed process stop keeps current and blocks startup', async (t) => {
  const f = fixture(t, false);
  await assert.rejects(f.guardian.disable('lease_expired'));
  assert.equal(f.guardian.read().guardian.state, 'blocked');
  assert(!f.guardian.cleared());
  assert(!existsSync(join(f.directory, 'disable-receipts')));
});

test('restart after rename finishes isolation and durable receipt', async (t) => {
  const f = fixture(t);
  const state = f.guardian.read();
  const transactionId = '019a0000-0000-7000-8000-000000000002';
  state.pendingIsolation = {
    transactionId,
    quarantinePath: `auriga/skills-active/quarantine/${transactionId}`,
    phase: 'rename_pending',
  };
  durableJson(join(f.directory, 'state.json'), state);
  const { renameSync } = await import('node:fs');
  mkdirSync(join(f.home, 'auriga/skills-active/quarantine'), {
    recursive: true,
  });
  renameSync(
    f.guardian.current,
    join(f.home, state.pendingIsolation.quarantinePath),
  );
  await f.guardian.disable('verification_failed');
  assert.equal(f.guardian.read().guardian.state, 'private_only');
  assert(!f.guardian.read().pendingIsolation);
});
