import { test } from 'node:test';
import assert from 'node:assert/strict';
import { openPendingBuffer } from '../../src/network/pending-buffer.js';
import { createMemoryLedgerStore } from '../../src/network/host/store.js';

test('pending records apply per-owner backpressure without poisoning storage and expire across restart', async () => {
  const store = createMemoryLedgerStore();
  let now = 1000,
    failures = 0;
  let handle = await store.open();
  const options = {
    now: () => now,
    limit: 4,
    perOwner: 2,
    ttlMs: 100,
    onCommitFailure: () => failures++,
  };
  let pool = await openPendingBuffer({ ...options, handle });
  assert.equal(await pool.put('a', { id: 'a' }, 'owner-a'), true);
  assert.equal(await pool.put('b', { id: 'b' }, 'owner-a'), true);
  assert.equal(await pool.put('c', { id: 'c' }, 'owner-a'), false);
  assert.equal(await pool.put('d', { id: 'd' }, 'owner-b'), true);
  assert.equal(failures, 0);
  await handle.close();
  now += 101;
  handle = await store.open();
  pool = await openPendingBuffer({ ...options, handle });
  assert.equal(pool.size, 0);
  assert.equal(await pool.put('e', { id: 'e' }, 'owner-a'), true);
  await handle.close();
});

test('uncertain commits alone notify fatal storage failure; reconciliation attempts are bounded', async () => {
  let fail = false,
    failures = 0;
  const handle = await createMemoryLedgerStore({
    beforeCommit() {
      if (fail) throw new Error('disk failed');
    },
  }).open();
  const pool = await openPendingBuffer({
    handle,
    now: () => 1000,
    limit: 4,
    perOwner: 2,
    ttlMs: 100,
    onCommitFailure: () => failures++,
  });
  await pool.put('a', {}, 'owner');
  assert.equal(await pool.claim('a', 2), true);
  assert.equal(await pool.claim('a', 2), true);
  assert.equal(await pool.claim('a', 2), false);
  assert.equal(pool.size, 0);
  assert.equal(failures, 0);
  fail = true;
  await assert.rejects(pool.put('b', {}, 'owner'), /disk failed/);
  assert.equal(failures, 1);
  await handle.close();
});
