import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  cpSync,
  mkdirSync,
  renameSync,
  readFileSync,
  existsSync,
  writeFileSync,
  statSync,
} from 'node:fs';
import { join } from 'node:path';
import { spawn } from 'node:child_process';
import { setTimeout as delay } from 'node:timers/promises';
import { tempBase } from './helpers.mjs';
import { resourceFixture, id } from './resource-fixture.mjs';
import { Supervisor } from '../runtime/supervisor.mjs';
import { acquireHostLock, durableJson } from '../runtime/host-lock.mjs';
import { superviseDsh } from '../runtime/desktop-supervision.mjs';
import { ProcessTree } from '../runtime/process-tree.mjs';
import { validateResourceRoots } from '../scripts/build.mjs';

function harness(t, age = 0) {
  const f = resourceFixture(tempBase(t), Date.now() - age);
  const tree = {
    record: { launcher: null, root: null, children: [] },
    stopped: true,
    stops: 0,
    async isStopped() {
      return this.stopped;
    },
    async stopTree() {
      this.stopped = true;
      this.stops++;
    },
  };
  const time = () => ({ bootId: id(9), monotonicMillis: 1000 });
  const s = new Supervisor(f.home, tree, f.trust, { time, retryDelay: 1 });
  return { ...f, tree, s, time };
}
function stage(
  f,
  advance = false,
  source = join(f.home, 'auriga/skills-active/current/example-resource'),
) {
  if (advance) {
    f.ledger.revision++;
    f.ledger.selectionRevision++;
    f.ledger.generation++;
  }
  const i = f.ledger.installations[0];
  i.state = 'applying';
  i.selectionRevision = f.ledger.selectionRevision;
  i.generation = f.ledger.generation;
  const tx = id(7),
    parent = join(f.directory, 'staging', tx);
  mkdirSync(parent, { recursive: true });
  cpSync(source, join(parent, 'example-resource'), { recursive: true });
  mkdirSync(join(f.directory, 'transactions'), { recursive: true });
  durableJson(join(f.directory, 'transactions', tx + '.json'), {
    schemaVersion: 1,
    tenantId: f.ledger.tenantId,
    ownerId: f.ledger.ownerId,
    hostId: f.ledger.hostId,
    transactionId: tx,
    writer: 'plugin',
    operation: 'activate',
    phase: 'prepared',
    expectedLedgerRevision: f.ledger.revision,
    selectionRevision: f.ledger.selectionRevision,
    generation: f.ledger.generation,
    receiptId: id(6),
    recordedAt: new Date().toISOString(),
  });
  durableJson(join(f.directory, 'ledger.json'), f.ledger);
}
function emptyCurrent(f) {
  renameSync(
    join(f.home, 'auriga/skills-active/current'),
    join(f.home, 'saved-current'),
  );
}

test('F1 C holds lock and changes ledger between E snapshots without stopping DSH', async (t) => {
  const f = harness(t);
  await f.s.prepare();
  f.tree.stopped = false;
  const c = acquireHostLock(f.home, 'C', f.time);
  assert.equal((await f.s.reconcile()).deferred, true);
  assert.equal(f.tree.stops, 0);
  c.release();
  f.s.lastVerifiedAt = -Infinity;
  const original = f.s.guardian.locked.bind(f.s.guardian);
  let calls = 0;
  f.s.guardian.locked = (action) => {
    if (++calls === 2) {
      const c = acquireHostLock(f.home, 'C', f.time);
      f.ledger.revision++;
      durableJson(join(f.directory, 'ledger.json'), f.ledger);
      c.release();
    }
    return original(action);
  };
  await f.s.reconcile();
  assert.equal(f.tree.stops, 0);
  assert.equal(f.s.guardian.read().guardian.state, 'active');
});
for (const mode of ['roots', 'stale-envelope', 'unknown-ledger'])
  test(`F2 ${mode} permits private Desktop with empty current`, async (t) => {
    const f = harness(t, mode === 'stale-envelope' ? 600000 : 0);
    stage(f);
    emptyCurrent(f);
    if (mode === 'roots') f.s.trust = null;
    if (mode === 'unknown-ledger') {
      f.ledger.schemaVersion = 999;
      durableJson(join(f.directory, 'ledger.json'), f.ledger);
    }
    await f.s.prepare();
    assert(f.s.privateOnly);
    assert.equal(f.s.guardian.read(), null);
    assert(!existsSync(join(f.s.guardian.directory, 'high-watermarks.json')));
  });
test('F2 unowned nonempty current still fails closed', async (t) => {
  const f = harness(t);
  f.s.trust = null;
  await assert.rejects(f.s.prepare());
});
test('F3 prepared revision/generation advancement reauthorizes while keeping old signed view alive', async (t) => {
  const f = harness(t);
  await f.s.prepare();
  f.tree.stopped = false;
  stage(f, true);
  await f.s.reconcile();
  assert.equal(f.tree.stops, 0);
  assert.equal(f.s.guardian.read().guardian.state, 'active');
  assert.equal(f.s.guardian.read().guardian.generation, 2);
  // Restart E between permission and C switch: persisted signed authorization still proves old view.
  const resumed = new Supervisor(f.home, f.tree, f.trust, { time: f.time });
  await resumed.reconcile();
  assert.equal(f.tree.stops, 0);
  f.ledger.selectionRevision = 1;
  durableJson(join(f.directory, 'ledger.json'), f.ledger);
  await resumed.reconcile();
  assert(f.tree.stops > 0);
});
test('F4 real child is relaunched in the same session after private-only shared recovery', async (t) => {
  const f = harness(t);
  await f.s.prepare();
  await f.s.guardian.disable('revoked');
  const state = f.s.guardian.read(),
    receipt = JSON.parse(
      readFileSync(
        join(
          f.s.guardian.directory,
          'disable-receipts',
          state.lastDisableReceiptId + '.json',
        ),
      ),
    );
  f.ledger.processedDisableReceiptIds.push(state.lastDisableReceiptId);
  stage(f, true, join(f.home, receipt.quarantinePath, 'example-resource'));
  f.s.startedAt = Date.now() - 10000;
  let child,
    starts = 0,
    notices = 0;
  const exited = () => child.exitCode !== null || child.signalCode !== null;
  f.tree.attach = () => {
    f.tree.stopped = false;
  };
  f.tree.refresh = () => {};
  f.tree.isStopped = async () => !child || exited();
  f.tree.stopTree = async () => {
    if (child && !exited()) {
      const done = new Promise((ok) => child.once('exit', ok));
      child.kill();
      await done;
    }
    f.tree.stopped = true;
  };
  t.after(() => child?.kill());
  await superviseDsh({
    tree: f.tree,
    supervisor: f.s,
    session: { child() {} },
    notify: () => {
      notices++;
    },
    poll: () => delay(10),
    start: () => {
      starts++;
      child = spawn(
        process.execPath,
        [
          '-e',
          starts === 1
            ? 'setInterval(()=>{},1000)'
            : 'setTimeout(()=>process.exit(0),80)',
        ],
        { stdio: 'ignore' },
      );
      return child;
    },
  });
  assert.equal(starts, 2);
  assert.equal(notices, 1);
  assert.equal(f.s.guardian.read().guardian.state, 'active');
});
for (const desired of ['DISABLED', 'REMOVED', 'ENABLED'])
  test(`M4 K2 ${desired} takes precedence over enabled`, async (t) => {
    const f = harness(t);
    stage(f);
    emptyCurrent(f);
    f.ref.desiredState = desired;
    f.ref.enabled = desired !== 'ENABLED';
    durableJson(join(f.directory, 'ledger.json'), f.ledger);
    await f.s.prepare();
    assert.equal(
      f.s.guardian.read()?.guardian.state === 'active',
      desired === 'ENABLED',
    );
  });
test('M2 persisted effective time beyond lease never writes active', async (t) => {
  const f = harness(t);
  await f.s.prepare();
  await f.s.guardian.disable('revoked');
  const state = f.s.guardian.read(),
    receipt = JSON.parse(
      readFileSync(
        join(
          f.s.guardian.directory,
          'disable-receipts',
          state.lastDisableReceiptId + '.json',
        ),
      ),
    );
  f.ledger.processedDisableReceiptIds.push(state.lastDisableReceiptId);
  stage(f, true, join(f.home, receipt.quarantinePath, 'example-resource'));
  state.clock.maxObservedTime = new Date(
    Date.parse(f.ref.lease.expiresAt) + 1000,
  ).toISOString();
  durableJson(f.s.guardian.file, state);
  f.s.startedAt = Date.now() - 10000;
  await f.s.reconcile();
  assert.equal(f.s.guardian.read().guardian.state, 'private_only');
});
test('M5 empty and temp-only orphan locks recover only after confirmed stop', async (t) => {
  const f = harness(t),
    path = join(f.s.guardian.directory, 'host.lock.d');
  mkdirSync(path, { recursive: true });
  await f.s.recoverLock();
  assert(!existsSync(path));
  mkdirSync(path);
  writeFileSync(join(path, `owner.json.${id(11)}.tmp`), 'partial');
  await f.s.recoverLock();
  assert(!existsSync(path));
  mkdirSync(path);
  writeFileSync(join(path, 'unknown'), 'leave');
  await assert.rejects(f.s.recoverLock());
  assert(existsSync(path));
});
test('M6 unchanged tick does not rewrite state/high water or enumerate processes each second', async (t) => {
  const f = harness(t);
  await f.s.prepare();
  const file = f.s.guardian.file,
    marks = f.s.watermarks;
  const before = [statSync(file).mtimeMs, statSync(marks).mtimeMs];
  await f.s.reconcile();
  assert.deepEqual([statSync(file).mtimeMs, statSync(marks).mtimeMs], before);
});
test('M7 recycled parent PID never adopts older unrelated processes', (t) => {
  const root = { pid: 100, parent: 1, createdAt: '2026-11-13T00:00:10Z' },
    right = { pid: 101, parent: 100, createdAt: '2026-11-13T00:00:11Z' },
    wrong = { pid: 102, parent: 100, createdAt: '2026-11-13T00:00:09Z' };
  const tree = new ProcessTree(tempBase(t), () => [root, right, wrong]);
  tree.record.root = root;
  assert.deepEqual(
    tree
      .refresh()
      .map((p) => p.pid)
      .sort(),
    [100, 101],
  );
});
test('L5 deployment rejects all public vector keys regardless of kid', () => {
  const trust = JSON.parse(
    readFileSync(
      new URL(
        '../../../contracts/resource/unified-vectors.json',
        import.meta.url,
      ),
    ),
  ).trust;
  for (const root of Object.values(trust)) root.kid = 'renamed';
  assert.throws(() => validateResourceRoots(trust), /test resource key/);
});

test('L4 extra empty directories do not match signed Skill bytes', async (t) => {
  const f = harness(t);
  await f.s.prepare();
  mkdirSync(
    join(f.home, 'auriga/skills-active/current/example-resource/extra-empty'),
  );
  await f.s.reconcile();
  assert.equal(f.s.guardian.read().guardian.state, 'private_only');
});
