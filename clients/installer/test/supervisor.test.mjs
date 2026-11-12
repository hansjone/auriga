import { test } from 'node:test';
import assert from 'node:assert/strict';
import { join } from 'node:path';
import {
  readFileSync,
  writeFileSync,
  existsSync,
  mkdirSync,
  renameSync,
} from 'node:fs';
import { resourceFixture, id } from './resource-fixture.mjs';
import { tempBase } from './helpers.mjs';
import { Supervisor } from '../runtime/supervisor.mjs';
import {
  observeLedgers,
  verifySnapshot,
  recheckBytes,
} from '../runtime/resource-observer.mjs';
import { durableJson, acquireHostLock } from '../runtime/host-lock.mjs';

const marks = () => ({
  schemaVersion: 1,
  revision: 1,
  entries: [],
  migrationMarkers: [],
  consumedDowngradeAuthorizationIds: [],
});
test('independent observer verifies signed bytes, receipts and materialized view', (t) => {
  const f = resourceFixture(tempBase(t));
  const snapshot = observeLedgers(f.home)[0];
  const result = verifySnapshot(
    snapshot,
    f.trust,
    marks(),
    new Date().toISOString(),
  );
  recheckBytes(result);
  writeFileSync(
    join(f.home, 'auriga/skills-active/current/example-resource/SKILL.md'),
    'tampered',
  );
  assert.throws(() => recheckBytes(result));
  assert.throws(() =>
    verifySnapshot(snapshot, f.trust, marks(), new Date().toISOString()),
  );
});
test('full schema rejects nested unknown fields and installation receipt identity substitution', (t) => {
  const f = resourceFixture(tempBase(t));
  f.ledger.installations[0].resource.lease.unknown = true;
  durableJson(join(f.directory, 'ledger.json'), f.ledger);
  assert.throws(() => observeLedgers(f.home));
  delete f.ledger.installations[0].resource.lease.unknown;
  durableJson(join(f.directory, 'ledger.json'), f.ledger);
  const path = join(f.directory, 'receipts', id(2), id(4) + '.json');
  const r = JSON.parse(readFileSync(path));
  r.ownerId = id(8);
  durableJson(path, r);
  assert.throws(() =>
    verifySnapshot(
      observeLedgers(f.home)[0],
      f.trust,
      marks(),
      new Date().toISOString(),
    ),
  );
});
test('supervisor persists high-water marks and earliest lease, then independently disables revoked bytes', async (t) => {
  const f = resourceFixture(tempBase(t));
  let now = Date.now();
  let stopped = true;
  let stops = 0;
  const tree = {
    record: { launcher: null, root: null, children: [] },
    isStopped: async () => stopped,
    stopTree: async () => {
      stopped = true;
      stops++;
    },
  };
  const supervisor = new Supervisor(f.home, tree, f.trust, {
    now: () => now,
    time: () => ({ bootId: id(9), monotonicMillis: now }),
  });
  await supervisor.prepare();
  assert.equal(supervisor.guardian.read().guardian.state, 'active');
  assert.equal(supervisor.marks().entries[0].releaseSequence, 7);
  stopped = false;
  f.cache(
    JSON.stringify(
      f.revocations(2, [
        {
          packageId: id(2),
          versionId: id(4),
          digest: f.ref.identity.manifestDigest,
        },
      ]),
    ),
  );
  await supervisor.reconcile();
  assert.equal(supervisor.guardian.read().guardian.state, 'private_only');
  assert.equal(stops, 1);
  assert.equal(supervisor.marks().entries[0].releaseSequence, 7);
  assert(supervisor.guardian.cleared());
  await supervisor.reconcile(true); // Offline private startup survives stale C installation records.
  assert.equal(supervisor.guardian.read().guardian.state, 'private_only');
  stopped = false;
  await supervisor.reconcile();
  assert.equal(stopped, false);
});
for (const mode of ['clock_rollback', 'lease_expired', 'identity_changed'])
  test(`supervisor stops before isolation: ${mode}`, async (t) => {
    const f = resourceFixture(tempBase(t));
    let now = Date.now(),
      mono = 1000;
    let stopped = true;
    const tree = {
      record: { launcher: null, root: null, children: [] },
      isStopped: async () => stopped,
      stopTree: async () => {
        stopped = true;
      },
    };
    const supervisor = new Supervisor(f.home, tree, f.trust, {
      now: () => now,
      time: () => ({ bootId: id(9), monotonicMillis: mono }),
    });
    await supervisor.prepare();
    stopped = false;
    if (mode === 'clock_rollback') now -= 120001;
    if (mode === 'lease_expired') mono += 3600001;
    if (mode === 'identity_changed') {
      f.ledger.generation++;
      durableJson(join(f.directory, 'ledger.json'), f.ledger);
    }
    await supervisor.reconcile();
    assert(stopped);
    assert.equal(supervisor.guardian.read().guardian.state, 'private_only');
    assert.equal(supervisor.guardian.read().guardian.reason, mode);
  });

test('prepared staging authorizes before applied receipt', (t) => {
  const f = resourceFixture(tempBase(t));
  f.ledger.installations[0].state = 'applying';
  durableJson(join(f.directory, 'ledger.json'), f.ledger);
  const transactionId = id(7);
  mkdirSync(join(f.directory, 'staging', transactionId), { recursive: true });
  renameSync(
    join(f.home, 'auriga/skills-active/current/example-resource'),
    join(f.directory, 'staging', transactionId, 'example-resource'),
  );
  mkdirSync(join(f.directory, 'transactions'), { recursive: true });
  durableJson(join(f.directory, 'transactions', transactionId + '.json'), {
    schemaVersion: 1,
    tenantId: f.ledger.tenantId,
    ownerId: f.ledger.ownerId,
    hostId: f.ledger.hostId,
    transactionId,
    writer: 'plugin',
    operation: 'activate',
    phase: 'prepared',
    expectedLedgerRevision: 1,
    selectionRevision: 1,
    generation: 1,
    receiptId: id(6),
    recordedAt: new Date().toISOString(),
  });
  const verified = verifySnapshot(
    observeLedgers(f.home)[0],
    f.trust,
    marks(),
    new Date().toISOString(),
  );
  recheckBytes(verified);
  writeFileSync(
    join(
      f.directory,
      'staging',
      transactionId,
      'example-resource',
      'extra.txt',
    ),
    'unverified',
  );
  assert.throws(() => recheckBytes(verified));
});
test('unknown stale lock retained, known quiesced writer lock reclaimed', async (t) => {
  const home = tempBase(t);
  const lock = acquireHostLock(home, 'C', () => ({
    bootId: id(9),
    monotonicMillis: 1000,
  }));
  const tree = {
    record: { launcher: null, root: null, children: [] },
    isStopped: async () => true,
    stopTree: async () => {},
  };
  const supervisor = new Supervisor(home, tree, null);
  await assert.rejects(supervisor.recoverLock());
  assert(existsSync(join(home, 'auriga/guardian/host.lock.d/owner.json')));
  supervisor.previousLauncher = {
    pid: lock.owner.pid,
    createdAt: lock.owner.processStartedAt,
  };
  await supervisor.recoverLock();
  assert(!existsSync(join(home, 'auriga/guardian/host.lock.d')));
});
test('different boot cannot reuse cached active trust', async (t) => {
  const f = resourceFixture(tempBase(t));
  let bootId = id(9);
  const tree = {
    record: { launcher: null, root: null, children: [] },
    isStopped: async () => true,
    stopTree: async () => {},
  };
  const supervisor = new Supervisor(f.home, tree, f.trust, {
    time: () => ({ bootId, monotonicMillis: 1000 }),
  });
  await supervisor.prepare();
  bootId = id(10);
  await supervisor.reconcile(true);
  assert.equal(supervisor.guardian.read().guardian.state, 'private_only');
  assert(supervisor.guardian.cleared());
});
