import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, rm, readFile, writeFile, readdir } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import {
  openLedger,
  reserveLocal,
  registerReservedLocal,
  loadConnectionLedger,
} from '../../../src/network/host/ledger.js';
import {
  createFileLedgerStore,
  createMemoryLedgerStore,
} from '../../../src/network/host/store.js';
import { CODES } from '../../../src/network/host/errors.js';
import {
  IDENTITY,
  boundSessionWithRelation,
  createFixturePlatform,
  createWorld,
  unicast,
} from './fixtures.js';

async function tempDir(t) {
  const dir = await mkdtemp(join(tmpdir(), 'auriga-host-ledger-'));
  t.after(() => rm(dir, { recursive: true, force: true }));
  return dir;
}

const code = (c) => (e) => e?.code === c;

test('connection ledger loads once under caller handle and consumes missing-ledger initialization once', async () => {
  const store = createMemoryLedgerStore();
  const handle = await store.open();
  const registration = { ...IDENTITY, created: true };
  let loads = 0;
  const locked = await loadConnectionLedger(
    {
      ...handle,
      load: () => {
        loads++;
        return handle.load();
      },
    },
    registration,
  );
  assert.equal(locked.state, undefined);
  await locked.initialize(IDENTITY);
  assert.deepEqual(locked.state.identity, IDENTITY);
  await assert.rejects(
    locked.initialize(IDENTITY),
    code(CODES.ledgerUnavailable),
  );
  await assert.rejects(store.open(), code(CODES.ledgerUnavailable));
  assert.equal(loads, 1);
  await handle.close();
  const next = await store.open();
  const existing = await loadConnectionLedger(next, {
    ...registration,
    created: false,
  });
  assert.equal(
    existing.state.identity.nodeIncarnation,
    IDENTITY.nodeIncarnation,
  );
  await next.close();
});

test('file ledger: explicit init, single writer, durable reopen', async (t) => {
  const dir = await tempDir(t);
  const store = createFileLedgerStore({ dir });
  await assert.rejects(
    openLedger({ store, identity: IDENTITY }),
    code(CODES.ledgerUnavailable),
  );
  const ledger = await openLedger({
    store,
    identity: IDENTITY,
    initialize: true,
  });
  await assert.rejects(
    openLedger({ store, identity: IDENTITY }),
    code(CODES.ledgerUnavailable),
  );
  await assert.rejects(
    openLedger({ store: createFileLedgerStore({ dir }), identity: IDENTITY }),
    code(CODES.ledgerUnavailable),
  );
  await ledger.update((d) => {
    d.audit.push({ at: 'x', recipientId: 'r', reason: 'probe' });
  });
  await ledger.close();
  const again = await openLedger({ store, identity: IDENTITY });
  assert.equal(again.read().audit.length, 1);
  assert.equal(again.read().revision, 1);
  await again.close();
  await assert.rejects(
    openLedger({ store, identity: { ...IDENTITY, nodeIncarnation: 'other' } }),
    code(CODES.ledgerUnavailable),
  );
});

test('file ledger: stale lock of a dead pid is reclaimed, a live one is not', async (t) => {
  const dir = await tempDir(t);
  const init = await openLedger({
    store: createFileLedgerStore({ dir }),
    identity: IDENTITY,
    initialize: true,
  });
  await init.close();
  await writeFile(
    join(dir, 'ledger.lock'),
    JSON.stringify({ pid: 424242, token: 'old' }),
  );
  await assert.rejects(
    openLedger({
      store: createFileLedgerStore({ dir, isAlive: () => true }),
      identity: IDENTITY,
    }),
    code(CODES.ledgerUnavailable),
  );
  const ledger = await openLedger({
    store: createFileLedgerStore({ dir, isAlive: () => false }),
    identity: IDENTITY,
  });
  const lock = JSON.parse(await readFile(join(dir, 'ledger.lock'), 'utf8'));
  assert.equal(lock.pid, process.pid);
  await ledger.close();
});

test('file ledger: crash mid-commit keeps the previous snapshot; ENOSPC never half-applies', async (t) => {
  const dir = await tempDir(t);
  let failAt = null;
  const store = createFileLedgerStore({
    dir,
    beforeStep(step) {
      if (step === failAt) {
        failAt = null;
        throw Object.assign(new Error('injected'), { code: 'ENOSPC' });
      }
    },
  });
  const ledger = await openLedger({
    store,
    identity: IDENTITY,
    initialize: true,
  });
  await reserveLocal(ledger, {
    opId: 'a',
    purpose: 't',
    mappingDigest: 'm',
    addressType: 'session',
  });
  failAt = 'rename';
  await assert.rejects(
    ledger.update((d) => {
      d.localHighWater = '9';
    }),
    code('ENOSPC'),
  );
  // In-memory view did not move; disk still holds revision with local 1.
  assert.equal(ledger.read().localHighWater, '1');
  ledger.abandon();
  assert.ok((await readdir(dir)).includes('ledger.json.tmp'));
  const reopened = await openLedger({
    store: createFileLedgerStore({ dir, isAlive: () => false }),
    identity: IDENTITY,
  });
  assert.equal(reopened.read().localHighWater, '1');
  assert.ok(!(await readdir(dir)).includes('ledger.json.tmp'));
  await reopened.close();
  await writeFile(join(dir, 'ledger.json'), '{"version":1,');
  await assert.rejects(
    openLedger({ store: createFileLedgerStore({ dir }), identity: IDENTITY }),
    code(CODES.ledgerUnavailable),
  );
});

test('local counter: starts at 1, persists before exposure, burns on crash, never goes back', async (t) => {
  const dir = await tempDir(t);
  const platform = createFixturePlatform();
  const open = (initialize = false) =>
    openLedger({
      store: createFileLedgerStore({ dir, isAlive: () => false }),
      identity: IDENTITY,
      initialize,
    });
  let ledger = await open(true);
  const first = await reserveLocal(ledger, {
    opId: 'op1',
    purpose: 't',
    mappingDigest: 'm',
    addressType: 'session',
  });
  assert.equal(first.local, '1');
  await registerReservedLocal(ledger, 'op1', platform.network.registerLocal);
  const second = await reserveLocal(ledger, {
    opId: 'op2',
    purpose: 't',
    mappingDigest: 'm',
    addressType: 'session',
  });
  assert.equal(second.local, '2');
  // Queue head unsettled: a later op cannot reserve a bigger number.
  await assert.rejects(
    reserveLocal(ledger, {
      opId: 'op3',
      purpose: 't',
      mappingDigest: 'm',
      addressType: 'session',
    }),
    code(CODES.localQueueBusy),
  );
  // LOCAL_REGISTER response lost, then the process dies.
  platform.drop.register = 1;
  await assert.rejects(
    registerReservedLocal(ledger, 'op2', platform.network.registerLocal),
  );
  ledger.abandon();
  ledger = await open();
  const resumed = await reserveLocal(ledger, {
    opId: 'op2',
    purpose: 't',
    mappingDigest: 'm',
    addressType: 'session',
  });
  assert.equal(resumed.local, '2');
  assert.equal(resumed.fingerprint, second.fingerprint);
  const reg = await registerReservedLocal(
    ledger,
    'op2',
    platform.network.registerLocal,
  );
  assert.equal(reg.state, 'registered');
  const third = await reserveLocal(ledger, {
    opId: 'op3',
    purpose: 't',
    mappingDigest: 'm',
    addressType: 'session',
  });
  assert.equal(third.local, '3');
  // Crash after reserve, before register: 3 is reserved and stays the head.
  ledger.abandon();
  ledger = await open();
  assert.equal(ledger.read().localHighWater, '3');
  assert.deepEqual(ledger.read().localQueue, ['op3']);
  await ledger.close();
});

test('rolled-back ledger snapshot collides with the platform high-water → recovery conflict', async () => {
  const store = createMemoryLedgerStore();
  const world = await createWorld({ store });
  await boundSessionWithRelation(world, { hostSessionId: 'h1' });
  const old = store.snapshot();
  world.host.addSession('h2', { cwd: 'D:/h2' });
  await world.adapter.bindExistingSession({
    opId: 'b2',
    hostSessionId: 'h2',
    cwd: 'D:/h2',
  });
  // Whole-ledger rollback to before local 2 was registered.
  world.ledger.abandon();
  store.crash();
  store.restore(old);
  const adapter = await world.boot(false);
  world.host.addSession('h3', { cwd: 'D:/h3' });
  await assert.rejects(
    adapter.bindExistingSession({
      opId: 'b3',
      hostSessionId: 'h3',
      cwd: 'D:/h3',
    }),
    code(CODES.localAllocationConflict),
  );
  assert.equal(
    world.ledger.read().identityConflict,
    CODES.localAllocationConflict,
  );
  await assert.rejects(
    reserveLocal(world.ledger, {
      opId: 'b4',
      purpose: 't',
      mappingDigest: 'm',
      addressType: 'session',
    }),
    code(CODES.recoveryConflict),
  );
  const { receipts } = await world.platform.pump(adapter, [
    unicast({ recipientId: 'z1', local: '1' }),
  ]);
  assert.equal(receipts[0].status, 'retry');
  assert.equal(receipts[0].code, CODES.recoveryConflict);
  assert.equal(world.host.counts.prompt, 0);
});
