import test from 'node:test';
import assert from 'node:assert/strict';
import { inboxFixture } from './inbox-fixture.mjs';

test('default closed: bootstrap does not FETCH, setReceive or call native prompt', async (t) => {
  const f = inboxFixture();
  t.after(() => f.close());
  const receiver = await f.openReceiver();
  assert.equal(receiver.snapshot().enabled, false);
  assert.deepEqual(f.requests, []);
  assert.equal(
    f.calls.some(([name]) => name === 'prompt'),
    false,
  );
});

test('explicit receiveSet -> signed page/item -> original public prompt -> durable acceptance -> actual receipt', async (t) => {
  const f = inboxFixture();
  t.after(() => f.close());
  const receiver = await f.openReceiver();
  await receiver.reconcile(true);
  const item = f.delivery();
  f.deliveries.push(item);
  await receiver.pull();
  assert.deepEqual(receiver.failures(), []);
  assert.deepEqual(
    f.requests.map((frame) => frame.type),
    ['receive_set', 'fetch', 'attempt_begin', 'execution_admit', 'receipt'],
  );
  const prompts = f.calls.filter(([name]) => name === 'prompt');
  assert.equal(prompts.length, 1);
  assert.equal(prompts[0][1].sessionId, f.binding.sessionId);
  assert.equal(prompts[0][1].requestId, item.recipients[0].recipient_id);
  assert.match(prompts[0][1].content[0].text, /真实持久接收测试正文/);
  const durable = f.store.snapshot().state.durable;
  assert.equal(
    durable.inbound[item.recipients[0].recipient_id].stage,
    'committed',
  );
  assert.equal(durable.resumeCursor, '1');
  await receiver.reconcile(true);
  assert.equal(
    f.requests.filter((frame) => frame.type === 'receive_set').length,
    1,
    'polling cannot bump receive version',
  );
});

test('lost receipt response survives reopening original inbox cache without another native prompt', async (t) => {
  let lost = false;
  const f = inboxFixture({
    receipt() {
      if (!lost) {
        lost = true;
        throw new Error('lost response');
      }
    },
  });
  t.after(() => f.close());
  let receiver = await f.openReceiver();
  await receiver.reconcile(true);
  const item = f.delivery();
  f.deliveries.push(item);
  await receiver.pull();
  assert.equal(
    f.store.snapshot().state.durable.inbound[item.recipients[0].recipient_id]
      .stage,
    'accepted',
  );
  assert.equal(receiver.failures().length, 1);
  const first = f.requests.find((frame) => frame.type === 'receipt');
  await receiver.close();
  receiver = await f.openReceiver();
  await receiver.reconcile(true);
  assert.equal(f.calls.filter(([name]) => name === 'prompt').length, 1);
  assert.equal(
    f.store.snapshot().state.durable.inbound[item.recipients[0].recipient_id]
      .stage,
    'committed',
  );
  assert.deepEqual(
    f.requests
      .filter((frame) => frame.type === 'receipt')
      .map((frame) => frame.operation_id),
    [first.operation_id, first.operation_id],
  );
});

test('public provider flush failure keeps invocation unaccepted and never sends receipt', async (t) => {
  const f = inboxFixture();
  t.after(() => f.close());
  const receiver = await f.openReceiver();
  await receiver.reconcile(true);
  const item = f.delivery();
  f.deliveries.push(item);
  f.ctx.sessions.flush = async () => false;
  await receiver.pull();
  assert.equal(f.calls.filter(([name]) => name === 'prompt').length, 1);
  assert.equal(
    f.store.snapshot().state.durable.inbound[item.recipients[0].recipient_id]
      .stage,
    'invoking',
  );
  assert.equal(
    f.requests.some((frame) => frame.type === 'receipt'),
    false,
  );
  await receiver.pull();
  assert.equal(f.calls.filter(([name]) => name === 'prompt').length, 1);
});

test('local explicit disable fences delivery while remote receiveSet is unavailable', async (t) => {
  const f = inboxFixture({
    beforeReceive(frame) {
      if (frame.type === 'receive_set' && !frame.enabled)
        throw new Error('offline');
    },
  });
  t.after(() => f.close());
  const receiver = await f.openReceiver();
  await receiver.reconcile(true);
  await receiver.fence();
  await assert.rejects(receiver.reconcile(false));
  f.deliveries.push(f.delivery());
  await receiver.pull();
  assert.equal(
    f.calls.some(([name]) => name === 'prompt'),
    false,
  );
  assert.equal(
    f.requests.some((frame) => frame.type === 'attempt_begin'),
    false,
  );
});

for (const tamper of ['bad-signature', 'foreign-target', 'old-epoch'])
  test(`${tamper} cannot become native prompt or receipt`, async (t) => {
    const f = inboxFixture({
      items(items) {
        if (tamper === 'bad-signature')
          items[0].server_auth.signature = 'A'.repeat(86);
      },
    });
    t.after(() => f.close());
    const receiver = await f.openReceiver();
    await receiver.reconcile(true);
    const item = f.delivery();
    if (tamper === 'foreign-target') item.message.to.node = '65';
    if (tamper === 'old-epoch') item.epoch = '2';
    f.deliveries.push(item);
    try {
      await receiver.pull();
    } catch {
      /* Signature failures fail the whole fetch. */
    }
    assert.equal(
      f.calls.some(([name]) => name === 'prompt'),
      false,
    );
    assert.equal(
      f.requests.some((frame) => frame.type === 'receipt'),
      false,
    );
  });

test('revocation after fetch blocks admission and actual prompt', async (t) => {
  let revoked = false;
  const f = inboxFixture({
    items() {
      revoked = true;
    },
  });
  t.after(() => f.close());
  const receiver = await f.openReceiver(() => {
    if (revoked) throw new Error('revoked');
  });
  await receiver.reconcile(true);
  f.deliveries.push(f.delivery());
  await assert.rejects(receiver.pull());
  assert.equal(
    f.calls.some(([name]) => name === 'prompt'),
    false,
  );
  assert.equal(
    f.requests.some((frame) => frame.type === 'receipt'),
    false,
  );
});
