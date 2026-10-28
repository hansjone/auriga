import test from 'node:test';
import assert from 'node:assert/strict';
import { openLedger } from '../../src/network/host/ledger.js';
import { createMemoryLedgerStore } from '../../src/network/host/store.js';
import { IDENTITY } from './host/fixtures.js';
import { canonicalize } from '../../src/network/trust/jcs.js';
import {
  createDurableOutbox,
  saveProducerEpoch,
} from '../../src/network/durable/outbox.js';

const request = () => ({
  kind: 'message',
  intent: 'information',
  durability: 'durable',
  dispatch_mode: 'atomic',
  ttl: 60,
  to: { version: 1, realm: '2', node: '65', local: '1', type: 'session' },
  body: { subtype: 'text', text: '耐久消息' },
  source: { source_binding: 'source-1' },
  idempotency: { producer_epoch: '42', key: 'original-key' },
});
async function fixture(t) {
  let fail = false;
  const store = createMemoryLedgerStore({
    beforeCommit() {
      if (fail) throw new Error('storage failed');
    },
  });
  const ledger = await openLedger({
    store,
    identity: IDENTITY,
    initialize: true,
  });
  t.after(() => ledger.close());
  await saveProducerEpoch(ledger, '42');
  return {
    store,
    ledger,
    fail() {
      fail = true;
    },
  };
}
test('提交前耐久保存完整原请求与来源；同键返回完整原结果且异体拒绝', async (t) => {
  const { store, ledger } = await fixture(t);
  let calls = 0;
  const original = request();
  const raw =
    '{"status":"platform_stored","message_id":"message-1","optional":{"kept":true}}';
  const outbox = createDurableOutbox({
    ledger,
    assertCurrent() {},
    async submit(value) {
      calls++;
      const saved = Object.values(store.snapshot().durable.submissions)[0];
      assert.deepEqual(
        saved.request,
        original,
        'original SubmitRequest must be durable before HTTPS',
      );
      assert.equal(saved.bindingId, 'binding-1');
      assert.deepEqual(value, original);
      return Buffer.from(raw);
    },
  });
  assert.equal(
    canonicalize(await outbox.send(original, 'binding-1')),
    canonicalize(JSON.parse(raw)),
  );
  assert.equal(
    canonicalize(await outbox.send(original, 'binding-1')),
    canonicalize(JSON.parse(raw)),
  );
  assert.equal(calls, 1);
  await assert.rejects(
    outbox.send(
      { ...original, body: { subtype: 'text', text: 'changed' } },
      'binding-1',
    ),
    /conflict/,
  );
});
test('确认丢失后重建 outbox 复用原请求身份；producer_epoch 跨重连不可替换', async (t) => {
  const { ledger } = await fixture(t);
  const original = request();
  const broken = createDurableOutbox({
    ledger,
    assertCurrent() {},
    async submit() {
      throw new Error('reply lost');
    },
  });
  await assert.rejects(broken.send(original, 'binding-1'), /reply lost/);
  await saveProducerEpoch(ledger, '42');
  await assert.rejects(saveProducerEpoch(ledger, '43'), /producer epoch/);
  const seen = [];
  const recovered = createDurableOutbox({
    ledger,
    assertCurrent() {},
    async submit(value) {
      seen.push(value);
      return Buffer.from(
        '{"status":"platform_stored","message_id":"original"}',
      );
    },
  });
  await recovered.recover();
  assert.deepEqual(seen, [original]);
});
test('持久保存失败禁止联网，不能称已提交', async (t) => {
  const { ledger, fail } = await fixture(t);
  fail();
  let called = false;
  const outbox = createDurableOutbox({
    ledger,
    assertCurrent() {},
    async submit() {
      called = true;
      return Buffer.from('{}');
    },
  });
  await assert.rejects(outbox.send(request(), 'binding-1'));
  assert.equal(called, false);
});
