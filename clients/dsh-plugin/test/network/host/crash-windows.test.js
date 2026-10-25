// One test per row of protocol §6 "崩溃点 | 恢复动作", driven through the
// fixture transport with injected crash points.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  Crash,
  boundSessionWithRelation,
  createWorld,
  unicast,
} from './fixtures.js';
import { CODES } from '../../../src/network/host/errors.js';

async function setup() {
  const world = await createWorld();
  const local = await boundSessionWithRelation(world);
  return { world, local, item: unicast({ recipientId: 'r1', local }) };
}

async function crashes(promise, point) {
  await assert.rejects(promise, (e) => e instanceof Crash && e.point === point);
}

test('row 1: crash before ledger RECEIVED → platform redelivers, requestId unchanged', async () => {
  const { world, item } = await setup();
  world.crashAt('before_received');
  await crashes(world.platform.pump(world.adapter, [item]), 'before_received');
  assert.equal(world.record('r1'), undefined);
  const adapter = await world.restart({ hostCrash: true });
  const { receipts } = await world.platform.pump(adapter, [item]);
  assert.equal(receipts[0].status, 'host_accepted');
  assert.equal(receipts[0].hostRequestId, 'r1');
  assert.equal(world.host.counts.prompt, 1);
  assert.deepEqual(
    world.host.history('host-a1').map((x) => x.requestId),
    ['r1'],
  );
});

test('row 2: RECEIVED then crash → only the current epoch may admit; attempts never reset', async () => {
  const { world, item } = await setup();
  world.crashAt('after_received');
  await crashes(world.platform.pump(world.adapter, [item]), 'after_received');
  assert.equal(world.record('r1').state, 'RECEIVED');
  const adapter = await world.restart();
  // Takeover: epoch 6 is current; the old epoch's admit is refused.
  world.platform.state.epoch = '6';
  const stale = await world.platform.pump(adapter, [item], { epoch: '5' });
  assert.equal(stale.receipts[0].status, 'retry');
  assert.equal(stale.receipts[0].code, CODES.admissionDenied);
  assert.equal(world.host.counts.prompt, 0);
  const ok = await world.platform.pump(adapter, [item], { epoch: '6' });
  assert.equal(ok.receipts[0].status, 'host_accepted');
  assert.equal(world.host.counts.prompt, 1);
  assert.equal(world.platform.state.attempts.get('r1'), 3);
  assert.equal(world.platform.state.admissions.get('r1').epoch, '6');
});

test('row 3a: admission committed but response lost → no re-admit/prompt, Unresolved', async () => {
  const { world, item } = await setup();
  world.platform.drop.admit = 1;
  const first = await world.platform.pump(world.adapter, [item]);
  assert.equal(first.receipts[0].status, 'retry');
  assert.equal(world.record('r1').state, 'ADMIT_REQUESTED');
  // A clone / new epoch cannot obtain a second permit either.
  world.platform.state.epoch = '6';
  const clone = await world.platform.network.admit({
    recipientId: 'r1',
    connectionEpoch: '6',
    bootId: 'clone',
  });
  assert.equal(clone.status, 'denied');
  const second = await world.platform.pump(world.adapter, [item], {
    epoch: '6',
  });
  assert.equal(second.receipts[0].status, 'outcome_unknown');
  assert.equal(world.host.counts.prompt, 0);
  assert.equal(world.platform.state.calls.admit, 2);
});

test('row 3b: crash after admit response, before invocation_started → Unresolved, zero prompts', async () => {
  const { world, item } = await setup();
  world.crashAt('after_admit_response');
  await crashes(
    world.platform.pump(world.adapter, [item]),
    'after_admit_response',
  );
  const adapter = await world.restart();
  const { receipts } = await world.platform.pump(adapter, [item]);
  assert.equal(receipts[0].status, 'outcome_unknown');
  assert.equal(world.host.counts.prompt, 0);
});

test('row 3c: invocation_started persisted, crash before prompt → Unresolved, zero prompts', async () => {
  const { world, item } = await setup();
  world.crashAt('after_invocation_started');
  await crashes(
    world.platform.pump(world.adapter, [item]),
    'after_invocation_started',
  );
  assert.equal(world.record('r1').state, 'INVOCATION_STARTED');
  const adapter = await world.restart({ hostCrash: true });
  const { receipts } = await world.platform.pump(adapter, [item]);
  assert.equal(receipts[0].status, 'outcome_unknown');
  assert.equal(world.host.counts.prompt, 0);
});

test('row 4: deliver entered, crash before admission → attempt consumed, max 5 then quarantined', async () => {
  const { world, item } = await setup();
  world.crashAt('after_received');
  const page = world.platform.page([item]);
  await crashes(world.adapter.deliver(page), 'after_received');
  let adapter = await world.restart();
  // Re-entering with the same platform attempt is refused.
  const replay = await adapter.deliver(page);
  assert.equal(replay[0].status, 'retry');
  assert.equal(replay[0].code, CODES.attemptConsumed);
  // Attempts 2..5 each crash after ADMIT_REQUESTED but before admit.
  for (let i = 2; i <= 5; i++) {
    world.crashAt('after_admit_requested');
    await crashes(
      world.platform.pump(adapter, [item]),
      'after_admit_requested',
    );
    adapter = await world.restart();
  }
  assert.equal(world.platform.state.attempts.get('r1'), 5);
  const sixth = await world.platform.pump(adapter, [item]);
  assert.deepEqual(sixth.receipts, []);
  assert.ok(world.platform.state.quarantined.has('r1'));
  assert.equal(world.host.counts.prompt, 0);
  assert.equal(world.platform.state.admissions.size, 0);
});

test('row 5: prompt in host memory, crash before flush', async (t) => {
  await t.test(
    'host also died → outcome_unknown, never re-prompted',
    async () => {
      const { world, item } = await setup();
      world.crashAt('after_prompt');
      await crashes(world.platform.pump(world.adapter, [item]), 'after_prompt');
      const adapter = await world.restart({ hostCrash: true });
      const { receipts } = await world.platform.pump(adapter, [item]);
      assert.equal(receipts[0].status, 'outcome_unknown');
      assert.equal(world.host.counts.prompt, 1);
    },
  );
  await t.test(
    'host still holds it → flush then accept, one prompt',
    async () => {
      const { world, item } = await setup();
      world.crashAt('after_prompt');
      await crashes(world.platform.pump(world.adapter, [item]), 'after_prompt');
      const adapter = await world.restart();
      const { receipts } = await world.platform.pump(adapter, [item]);
      assert.equal(receipts[0].status, 'host_accepted');
      assert.equal(world.host.counts.prompt, 1);
    },
  );
  await t.test(
    'flush=false is never an ACK; next round accepts without re-prompt',
    async () => {
      const { world, item } = await setup();
      world.host.faults.flushReturns = false;
      const first = await world.platform.pump(world.adapter, [item]);
      assert.equal(first.receipts[0].status, 'retry');
      assert.equal(first.receipts[0].code, CODES.flushPending);
      const second = await world.platform.pump(world.adapter, [item]);
      assert.equal(second.receipts[0].status, 'host_accepted');
      assert.equal(world.host.counts.prompt, 1);
    },
  );
});

test('row 6: host flushed, crash before ledger HOST_ACCEPTED → rebuild from history', async () => {
  const { world, item } = await setup();
  world.crashAt('after_flush');
  await crashes(world.platform.pump(world.adapter, [item]), 'after_flush');
  const adapter = await world.restart({ hostCrash: true });
  const { receipts } = await world.platform.pump(adapter, [item]);
  assert.equal(receipts[0].status, 'host_accepted');
  assert.equal(receipts[0].hostRequestId, 'r1');
  assert.equal(world.host.counts.prompt, 1);
});

test('row 7: HOST_ACCEPTED persisted, crash before platform ACK → resend same receipt', async () => {
  const { world, item } = await setup();
  world.crashAt('after_host_accepted');
  await crashes(
    world.platform.pump(world.adapter, [item]),
    'after_host_accepted',
  );
  const adapter = await world.restart({ hostCrash: true });
  const pending = adapter.pendingReceipts();
  assert.equal(pending.length, 1);
  assert.equal(pending[0].status, 'host_accepted');
  assert.deepEqual(world.platform.commitReceipts(pending), ['r1']);
  await adapter.markCommitted(['r1']);
  assert.equal(adapter.pendingReceipts().length, 0);
  const again = await world.platform.pump(adapter, [item]);
  assert.equal(again.receipts[0].status, 'host_accepted');
  assert.equal(world.host.counts.prompt, 1);
});

test('row 8: platform ACK committed, response lost → re-send/redeliver is idempotent', async () => {
  const { world, item } = await setup();
  world.platform.drop.receipt = 1;
  const first = await world.platform.pump(world.adapter, [item]);
  assert.equal(first.receipts[0].status, 'host_accepted');
  assert.equal(world.adapter.pendingReceipts().length, 1);
  assert.deepEqual(
    world.platform.commitReceipts(world.adapter.pendingReceipts()),
    ['r1'],
  );
  const again = await world.platform.pump(world.adapter, [item], {
    duplicate: true,
  });
  assert.ok(again.receipts.every((r) => r.status === 'host_accepted'));
  assert.equal(world.host.counts.prompt, 1);
  assert.equal(world.adapter.pendingReceipts().length, 0);
});

test('row 9: execution result event lost → durable outbox replays the same result ID', async () => {
  const { world, item } = await setup();
  await world.platform.pump(world.adapter, [item]);
  await world.adapter.recordResult({
    resultId: 'res-1',
    recipientId: 'r1',
    status: 'completed',
  });
  // The result send is lost with the process.
  const adapter = await world.restart({ hostCrash: true });
  assert.deepEqual(
    adapter.pendingResults().map((r) => r.resultId),
    ['res-1'],
  );
  await adapter.recordResult({
    resultId: 'res-1',
    recipientId: 'r1',
    status: 'completed',
  });
  await assert.rejects(
    adapter.recordResult({
      resultId: 'res-1',
      recipientId: 'r1',
      status: 'failed',
    }),
  );
  await adapter.markResultSent('res-1');
  assert.equal(adapter.pendingResults().length, 0);
  assert.equal(world.host.counts.prompt, 1);
});
