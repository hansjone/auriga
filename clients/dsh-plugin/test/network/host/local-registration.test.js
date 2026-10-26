import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createLocalAllocator } from '../../../src/network/host/local-allocator.js';
import {
  openLedger,
  reserveLocal,
  registerReservedLocal,
} from '../../../src/network/host/ledger.js';
import {
  createMemoryLedgerStore,
  createFileLedgerStore,
} from '../../../src/network/host/store.js';
import { CODES } from '../../../src/network/host/errors.js';
import { IDENTITY, createFixturePlatform, createWorld } from './fixtures.js';

const input = (opId) => ({
  opId,
  purpose: 'test',
  addressType: 'session',
  mappingDigest: `mapping:${opId}`,
});
const deferred = () => {
  let resolve;
  const promise = new Promise((r) => {
    resolve = r;
  });
  return { promise, resolve };
};

for (const window of ['reserved', 'remote_committed', 'credential_committed']) {
  test(`allocation recovers ${window} without reuse or rollback`, async () => {
    const store = createMemoryLedgerStore();
    const platform = createFixturePlatform();
    let ledger = await openLedger({
      store,
      identity: IDENTITY,
      initialize: true,
    });
    const first = await reserveLocal(ledger, input('one'));
    if (window === 'remote_committed') {
      platform.drop.register = 1;
      await assert.rejects(
        registerReservedLocal(ledger, 'one', platform.network.registerLocal),
      );
    } else if (window === 'credential_committed') {
      await registerReservedLocal(
        ledger,
        'one',
        platform.network.registerLocal,
      );
    }
    ledger.abandon();
    ledger = await openLedger({ store, identity: IDENTITY });
    const allocator = createLocalAllocator({
      ledger,
      registerLocal: platform.network.registerLocal,
    });
    const second = await allocator.allocate(input('two'));
    assert.equal(second.local, '2');
    assert.equal(ledger.read().locals['1'].fingerprint, first.fingerprint);
    assert.equal(ledger.read().locals['1'].state, 'registered');
    assert.equal(ledger.read().localHighWater, '2');
    assert.deepEqual(ledger.read().localQueue, []);
    await ledger.close();
  });
}

test('node queue does not reserve the next local while head registration is pending', async () => {
  const w = await createWorld();
  const gate = deferred();
  const entered = deferred();
  const allocator = createLocalAllocator({
    ledger: w.ledger,
    registerLocal: async (entry) => {
      if (entry.local === '1') {
        entered.resolve();
        await gate.promise;
      }
      return w.platform.network.registerLocal(entry);
    },
  });
  const a = allocator.allocate(input('a'));
  await entered.promise;
  const b = allocator.allocate(input('b'));
  assert.equal(w.ledger.read().localHighWater, '1');
  gate.resolve();
  assert.deepEqual(
    (await Promise.all([a, b])).map((r) => r.local),
    ['1', '2'],
  );
  await w.ledger.close();
});

test('allocation operation cannot be rebound to a different mapping or purpose', async () => {
  const w = await createWorld();
  await reserveLocal(w.ledger, input('one'));
  for (const change of [{ mappingDigest: 'other' }, { purpose: 'other' }]) {
    await assert.rejects(
      reserveLocal(w.ledger, { ...input('one'), ...change }),
      { code: CODES.localAllocationConflict },
    );
  }
  await w.ledger.close();
});

test('unknown registration response holds the queue and does not poison identity', async () => {
  const w = await createWorld();
  await reserveLocal(w.ledger, input('one'));
  await assert.rejects(
    registerReservedLocal(w.ledger, 'one', async () => ({
      status: 'unavailable',
    })),
    { code: CODES.protocolError },
  );
  assert.equal(w.ledger.read().identityConflict, null);
  assert.deepEqual(w.ledger.read().localQueue, ['one']);
  await w.ledger.close();
});

test('registration credential disk failure cannot publish an address or reserve a successor', async () => {
  let fail = true;
  const store = createMemoryLedgerStore({
    beforeCommit(s) {
      if (fail && s.locals['1']?.state === 'registered')
        throw new Error('disk failure');
    },
  });
  const w = await createWorld({ store });
  w.host.addSession('native', { cwd: 'D:/example' });
  const bind = () =>
    w.adapter.bindExistingSession({
      opId: 'bind',
      hostSessionId: 'native',
      cwd: 'D:/example',
    });
  await assert.rejects(bind());
  assert.deepEqual(w.ledger.read().bindings, {});
  await assert.rejects(reserveLocal(w.ledger, input('next')));
  fail = false;
  await w.restart();
  assert.equal(await bind(), '1');
  assert.equal(w.ledger.read().locals['1'].state, 'registered');
  await w.ledger.close();
});

test('file rename followed by sync failure freezes writes until durable reservation is recovered', async (t) => {
  const dir = await mkdtemp(join(tmpdir(), 'auriga-localreg-'));
  t.after(() => rm(dir, { recursive: true, force: true }));
  let failSync = false;
  const store = createFileLedgerStore({
    dir,
    beforeStep(step) {
      if (failSync && step === 'sync_dir')
        throw new Error('directory sync failed after rename');
    },
  });
  let ledger = await openLedger({
    store,
    identity: IDENTITY,
    initialize: true,
  });
  failSync = true;
  await assert.rejects(reserveLocal(ledger, input('one')));
  failSync = false;
  await assert.rejects(reserveLocal(ledger, input('two')), {
    code: CODES.ledgerUnavailable,
  });
  ledger.abandon();
  ledger = await openLedger({ store, identity: IDENTITY });
  assert.equal(ledger.read().localHighWater, '1');
  assert.deepEqual(ledger.read().localQueue, ['one']);
  const platform = createFixturePlatform();
  const allocator = createLocalAllocator({
    ledger,
    registerLocal: platform.network.registerLocal,
  });
  assert.equal((await allocator.allocate(input('two'))).local, '2');
  assert.equal(platform.state.registrations.size, 2);
  await ledger.close();
});

test('source address requires durable registration and binding; the operation cannot change native mapping', async () => {
  const w = await createWorld();
  w.host.addSession('native', { cwd: 'D:/example' });
  const bind = () =>
    w.adapter.bindExistingSession({
      opId: 'source',
      hostSessionId: 'native',
      cwd: 'D:/example',
    });
  w.platform.drop.register = 1;
  await assert.rejects(bind());
  assert.throws(() => w.adapter.localAddress('1'), {
    code: CODES.targetUnavailable,
  });
  assert.equal(await bind(), '1');
  assert.equal(w.adapter.localAddress('1').local, '1');
  w.host.addSession('other', { cwd: 'D:/example' });
  await assert.rejects(
    w.adapter.bindExistingSession({
      opId: 'source',
      hostSessionId: 'other',
      cwd: 'D:/example',
    }),
    { code: CODES.localAllocationConflict },
  );
  assert.equal(w.ledger.read().bindings['1'].hostSessionId, 'native');
  await w.adapter.updateBinding('1', { lifecycle: 'archived' });
  assert.throws(() => w.adapter.localAddress('1'), {
    code: CODES.targetUnavailable,
  });
  assert.equal(w.adapter.localAddress('0').type, 'system');
  assert.equal(w.ledger.read().localHighWater, '1');
  await w.ledger.close();
});

test('concurrent registration callers share one node lock and receive detached credentials', async () => {
  const w = await createWorld();
  const reserved = await reserveLocal(w.ledger, input('one'));
  reserved.state = 'registered';
  assert.equal(w.ledger.read().locals['1'].state, 'reserved');
  const results = await Promise.all(
    Array.from({ length: 8 }, () =>
      registerReservedLocal(w.ledger, 'one', w.platform.network.registerLocal),
    ),
  );
  assert.equal(w.platform.state.calls.register, 1);
  results[0].registrationId = 'tampered';
  assert.equal(w.ledger.read().locals['1'].registrationId, 'reg-1');
  assert.equal(results[1].registrationId, 'reg-1');
  await w.ledger.close();
});

test('uint64 exhaustion never wraps to system local zero', async () => {
  const w = await createWorld();
  await w.ledger.update((draft) => {
    draft.localHighWater = '18446744073709551615';
  });
  await assert.rejects(reserveLocal(w.ledger, input('overflow')), {
    code: CODES.localAllocationConflict,
  });
  assert.equal(w.ledger.read().localHighWater, '18446744073709551615');
  assert.equal(w.ledger.read().locals['0'], undefined);
  await w.ledger.close();
});

for (const corruption of ['counter', 'queue', 'mapping']) {
  test(`incomplete allocation ${corruption} log fails closed on recovery`, async () => {
    const store = createMemoryLedgerStore();
    const w = await createWorld({ store });
    await reserveLocal(w.ledger, input('one'));
    const snapshot = store.snapshot();
    if (corruption === 'counter') snapshot.localHighWater = '0';
    if (corruption === 'queue') snapshot.localQueue = [];
    if (corruption === 'mapping') delete snapshot.locals['1'].mappingDigest;
    w.ledger.abandon();
    store.restore(snapshot);
    await assert.rejects(openLedger({ store, identity: IDENTITY }), {
      code: CODES.ledgerUnavailable,
    });
  });
}
