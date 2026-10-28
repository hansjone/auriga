/* global structuredClone */
import test from 'node:test';
import assert from 'node:assert/strict';
import { createDurableInbox } from '../../src/network/durable/inbox.js';
import { createMailboxClient } from '../../src/network/durable/client.js';
import { saveProducerEpoch } from '../../src/network/durable/outbox.js';
import { createNativeHostPort } from '../../src/network/host/native/host-port.js';
import { createSessionRegistration } from '../../src/network/session-registration.js';
import { openLedger } from '../../src/network/host/ledger.js';
import { createMemoryLedgerStore } from '../../src/network/host/store.js';
import { decodeJson, encodeJson } from '../../src/network/session/frames.js';
import { signingFixture } from './trust/fixtures.js';
import { fixture as nativeFixture } from './host/native/fixtures.js';
import { IDENTITY, self, peer } from './host/fixtures.js';
import { digest } from '../../src/network/trust/verifier.js';
import { hasCursorEvidence } from '../../src/network/durable/recovery.js';

async function fixture(t, options = {}) {
  const n = nativeFixture(),
    agent = n.attach('session-a'),
    tools = new Map();
  n.ctx.tools = {
    register(tool) {
      tools.set(tool.name, tool);
      return () => tools.delete(tool.name);
    },
  };
  const signer = await signingFixture();
  const store = createMemoryLedgerStore({ beforeCommit: options.beforeCommit });
  const ledger = await openLedger({
    store,
    identity: IDENTITY,
    initialize: true,
  });
  await saveProducerEpoch(ledger, '42');
  const registration = createSessionRegistration({
    ctx: n.ctx,
    ledger,
    assertCurrent() {},
    async registerLocal(r) {
      return {
        status: 'registered',
        registrationId: 'registration-1',
        opId: r.opId,
        local: r.local,
        addressType: 'session',
        fingerprint: r.fingerprint,
        registrationRevision: '1',
        localHighWater: '1',
      };
    },
  });
  await registration.start();
  assert.equal(
    (await tools.get('auriga_network_join').execute({}, { agent })).status,
    'registered',
  );
  const host = await createNativeHostPort({ ctx: n.ctx });
  const abort = new AbortController();
  t.after(async () => {
    abort.abort();
    registration.close();
    await ledger.close();
  });
  const requests = [],
    listeners = new Set();
  const deliveries = [];
  const transport = {
    epoch: 1,
    state: 'ready',
    async stop() {},
    onMessage(fn) {
      listeners.add(fn);
      return () => listeners.delete(fn);
    },
    async send(bytes) {
      const f = decodeJson(bytes);
      requests.push(f);
      const context = { ...signer.context, requestId: f.request_id };
      let result;
      if (f.type === 'receive_set')
        result = {
          type: 'receive_set_result',
          registration_id: 'registration-1',
          enabled: f.enabled,
          state_version: (BigInt(f.expected_version) + 1n).toString(),
        };
      else if (f.type === 'attempt_begin')
        result = {
          type: 'attempt_result',
          recipient_id: f.recipient_id,
          attempt_id: f.attempt_id,
          result: 'entered',
        };
      else if (f.type === 'execution_admit')
        result = {
          type: 'admit_result',
          recipient_id: f.recipient_id,
          result: options.already ? 'already_admitted' : 'admitted',
          admitted_epoch: '7',
          state_version: '9',
          admission_id: 'admission-1',
        };
      else if (f.type === 'receipt') {
        assert.equal(
          store.snapshot().durable.inbound[f.results[0].recipient_id].stage,
          'accepted',
          'durable host acceptance must precede receipt',
        );
        assert.equal(
          f.results[0].expected_state_version,
          '9',
          'receipt must use server recipient.state_version',
        );
        assert.notEqual(String(ledger.peek().revision), '9');
        if (options.loseReceipt && !options.receiptLost) {
          options.receiptLost = true;
          throw new Error('receipt response lost');
        }
        result = {
          type: 'receipt_committed',
          operation_id: f.operation_id,
          delivery_id: f.delivery_id,
          mail_seq: f.mail_seq,
          results: [
            {
              recipient_id: f.results[0].recipient_id,
              transport_status: 'host_accepted',
              state_version: '10',
            },
          ],
          mail_item_closed: true,
          committed_cursor: f.mail_seq,
        };
      } else if (f.type === 'fetch') {
        const raw = deliveries[f.continuation === null ? 0 : 1];
        const item = raw && signer.seal(structuredClone(raw), context);
        const items = item ? [item] : [];
        const page = signer.seal(
          {
            app_version: '1.0',
            type: 'fetch_page',
            request_id: f.request_id,
            epoch: '7',
            head_seq: String(deliveries.length),
            committed_cursor: options.committedCursor ?? '0',
            scan_after: raw?.mail_seq ?? '0',
            snapshot_head: String(deliveries.length),
            next_continuation:
              deliveries.length > 1 && f.continuation === null
                ? 'opaque-next'
                : null,
            manifest: items.map((v) => ({
              item_type: v.type,
              mail_seq: v.mail_seq,
              digest: digest(v),
            })),
          },
          context,
        );
        options.fetchResult?.(page, items, context, signer);
        for (const listener of listeners)
          listener(
            encodeJson({
              app_version: '1.0',
              type: 'fetch_result',
              page,
              items,
            }),
            1,
          );
        return;
      } else throw new Error(f.type);
      for (const listener of listeners)
        listener(
          encodeJson(
            signer.seal(
              { app_version: '1.0', request_id: f.request_id, ...result },
              context,
            ),
          ),
          1,
        );
    },
  };
  const ready = { epoch: '7', bootId: 'boot-test', channelNonce: 'nonce-test' };
  const client = createMailboxClient({
    transport,
    trust: signer.trust,
    identity: IDENTITY,
    ready,
    signal: abort.signal,
    assertCurrent() {},
    now: signer.now,
  });
  const inbox = createDurableInbox({
    ctx: n.ctx,
    ledger,
    host,
    client,
    trust: signer.trust,
    ready,
    assertCurrent() {},
    now: signer.now,
  });
  function delivery(seq = '1') {
    const message = {
      message_id: 'message-' + seq,
      kind: 'message',
      message_class: 'unicast',
      sender_kind: 'agent',
      from: peer('1'),
      to: self('1'),
      body: { subtype: 'text', text: '真实正文' },
      expires_at: '2026-10-25T00:05:00Z',
    };
    return {
      app_version: '1.0',
      type: 'delivery',
      epoch: '7',
      delivery_id: 'delivery-' + seq,
      mail_seq: seq,
      target_node: { realm: '2', node: '64' },
      message,
      source_context: {
        realm: '2',
        node: '63',
        sender_kind: 'agent',
        message_digest: digest(message),
      },
      recipients: [
        {
          recipient_id: 'recipient-' + seq,
          local: '1',
          type: 'session',
          state_version: '9',
        },
      ],
    };
  }
  function terminal(seq = '1') {
    return {
      app_version: '1.0',
      type: 'terminal',
      epoch: '7',
      delivery_id: 'delivery-' + seq,
      mail_seq: seq,
      target_node: { realm: IDENTITY.realm, node: IDENTITY.node },
      recipients: [
        {
          recipient_id: 'recipient-' + seq,
          transport_status: 'expired',
          state_version: '10',
        },
      ],
    };
  }
  return { n, ledger, store, inbox, requests, deliveries, delivery, terminal };
}

test('经验签的expired终态保存原证据并推进游标，不调用宿主或生成回执', async (t) => {
  const f = await fixture(t, { committedCursor: '1' });
  f.deliveries.push(f.terminal());
  await f.inbox.pull();
  const saved = f.store.snapshot().durable;
  assert.equal(saved.resumeCursor, '1');
  assert.equal(saved.terminal['1'].type, 'terminal');
  assert.equal(saved.terminal['1'].recipients[0].transport_status, 'expired');
  assert.ok(saved.terminal['1'].server_auth.signature);
  assert.deepEqual(saved.inbound, {});
  assert.equal(f.n.calls.filter((v) => v[0] === 'prompt').length, 0);
  assert.deepEqual(
    f.requests.map((v) => v.type),
    ['fetch'],
  );
  // Complete retained evidence may repair a lost cursor commit after reconnect.
  const lagged = f.ledger.read();
  lagged.durable.resumeCursor = '0';
  assert.equal(hasCursorEvidence(lagged, '1'), true);
  assert.equal(hasCursorEvidence(lagged, '2'), false);
  await f.ledger.close();
  const reopened = await openLedger({ store: f.store, identity: IDENTITY });
  assert.equal(reopened.peek().durable.resumeCursor, '1');
  await reopened.close();
});

test('终态分页保留原resume且连续证据完成后才推进，不采用scan_after', async (t) => {
  const f = await fixture(t, { committedCursor: '2' });
  f.deliveries.push(f.terminal('1'), f.terminal('2'));
  await f.inbox.pull();
  assert.deepEqual(
    f.requests.map((v) => v.resume_cursor),
    ['0', '0'],
  );
  assert.equal(f.store.snapshot().durable.resumeCursor, '2');
  assert.equal(Object.keys(f.store.snapshot().durable.terminal).length, 2);
});

test('签名游标不能跨越缺失终态或本机接受证据', async (t) => {
  const f = await fixture(t, { committedCursor: '2' });
  f.deliveries.push(f.terminal('2'));
  await assert.rejects(f.inbox.pull(), /cursor evidence/);
  assert.equal(f.store.snapshot().durable.resumeCursor, '0');
});

test('终态与本机持久接受可共同证明连续前缀，终态不变成宿主回执', async (t) => {
  const f = await fixture(t, { committedCursor: '1' });
  await f.inbox.setReceive('1', true);
  f.deliveries.push(f.terminal('1'), f.delivery('2'));
  await f.inbox.pull();
  assert.equal(f.store.snapshot().durable.resumeCursor, '2');
  assert.deepEqual(Object.keys(f.store.snapshot().durable.inbound), [
    'recipient-2',
  ]);
  const receipts = f.requests.filter((v) => v.type === 'receipt');
  assert.equal(receipts.length, 1);
  assert.equal(receipts[0].results[0].recipient_id, 'recipient-2');
  assert.equal(f.n.calls.filter((v) => v[0] === 'prompt').length, 1);
});

for (const bad of ['signature', 'target', 'status', 'duplicate'])
  test(`终态${bad}非法不能推进游标或保存证据`, async (t) => {
    const f = await fixture(t, {
      committedCursor: '1',
      fetchResult(page, items, context, signer) {
        if (bad === 'signature')
          items[0].server_auth.signature = 'A'.repeat(86);
        else {
          if (bad === 'target') items[0].target_node.node = '65';
          if (bad === 'status')
            items[0].recipients[0].transport_status = 'pending';
          if (bad === 'duplicate')
            items[0].recipients.push(items[0].recipients[0]);
          signer.seal(items[0], context);
        }
        page.manifest[0].digest = digest(items[0]);
        signer.seal(page, context);
      },
    });
    f.deliveries.push(f.terminal());
    await assert.rejects(f.inbox.pull());
    assert.equal(f.store.snapshot().durable.resumeCursor, '0');
    assert.deepEqual(f.store.snapshot().durable.terminal ?? {}, {});
  });

test('已保存终态不可被删除或换成其他delivery，晚到不同终态不能改写原证据', async (t) => {
  const f = await fixture(t, { committedCursor: '1' });
  f.deliveries.push(f.terminal());
  await f.inbox.pull();
  const original = f.store.snapshot().durable.terminal['1'];
  for (const replace of [false, true])
    await assert.rejects(
      f.ledger.update((draft) => {
        if (replace) draft.durable.terminal['1'].delivery_id = 'other';
        else delete draft.durable.terminal['1'];
      }),
    );
  f.deliveries[0].delivery_id = 'other';
  await assert.rejects(f.inbox.pull(), /terminal conflict/);
  assert.deepEqual(f.store.snapshot().durable.terminal['1'], original);
});

test('终态证据或游标存储失败不能在耐久快照中跳过邮件', async (t) => {
  for (const failure of ['evidence', 'cursor']) {
    const f = await fixture(t, {
      committedCursor: '1',
      beforeCommit(state) {
        if (
          failure === 'evidence'
            ? state.durable?.terminal?.['1']
            : state.durable?.resumeCursor === '1'
        )
          throw new Error('terminal storage failed');
      },
    });
    f.deliveries.push(f.terminal());
    await assert.rejects(f.inbox.pull(), /terminal storage failed/);
    assert.equal(f.store.snapshot().durable.resumeCursor, '0');
    assert.deepEqual(f.store.snapshot().durable.inbound, {});
    assert.equal(f.requests.filter((v) => v.type === 'receipt').length, 0);
  }
});
test('默认 receive_none 拒收；正常控制开启后 HostPort 持久接受再 RECEIPT', async (t) => {
  const f = await fixture(t);
  await assert.rejects(f.inbox.deliver(f.delivery()), /receive_none/);
  assert.equal(f.requests.length, 0);
  await f.inbox.setReceive('1', true);
  await f.inbox.deliver(f.delivery());
  assert.equal(
    f.ledger.peek().durable.inbound['recipient-1'].stage,
    'committed',
  );
  assert.ok(f.n.calls.some((v) => v[0] === 'prompt'));
  assert.ok(f.n.calls.some((v) => v[0] === 'persistence-flush'));
  await f.inbox.setReceive('1', false);
  await assert.rejects(f.inbox.deliver(f.delivery('2')), /receive_none/);
});
test('FETCH continuation 保留原 resume，逐页真实宿主 seam 收件', async (t) => {
  const f = await fixture(t);
  await f.inbox.setReceive('1', true);
  f.deliveries.push(f.delivery('1'), f.delivery('2'));
  await f.inbox.pull();
  const fetches = f.requests.filter((v) => v.type === 'fetch');
  assert.deepEqual(
    fetches.map((v) => v.continuation),
    [null, 'opaque-next'],
  );
  assert.deepEqual(
    fetches.map((v) => v.resume_cursor),
    ['0', '0'],
  );
  assert.equal(f.ledger.peek().durable.resumeCursor, '2');
});
test('provider flush 失败无回执；已有调用在恢复时只 flush，不再次 prompt', async (t) => {
  const f = await fixture(t);
  await f.inbox.setReceive('1', true);
  f.n.state.flush = false;
  await assert.rejects(f.inbox.deliver(f.delivery()), /durable acceptance/);
  assert.equal(f.requests.filter((v) => v.type === 'receipt').length, 0);
  f.n.state.flush = true;
  await f.inbox.deliver(f.delivery());
  assert.equal(f.n.calls.filter((v) => v[0] === 'prompt').length, 1);
});
test('回执确认丢失复用原 operation_id，不再次 prompt', async (t) => {
  const f = await fixture(t, { loseReceipt: true });
  await f.inbox.setReceive('1', true);
  await assert.rejects(f.inbox.deliver(f.delivery()), /receipt response lost/);
  assert.equal(hasCursorEvidence(f.ledger.peek(), '1'), true);
  assert.equal(hasCursorEvidence(f.ledger.peek(), '2'), false);
  const incomplete = f.ledger.read();
  incomplete.durable.inbound['recipient-1'].receipt = null;
  assert.equal(hasCursorEvidence(incomplete, '1'), false);
  // A later receive-off does not erase an already durable acceptance. The
  // reconciliation path must never re-enter HostPort.prompt.
  await f.inbox.setReceive('1', false);
  await f.inbox.recover();
  const receipts = f.requests.filter((v) => v.type === 'receipt');
  assert.equal(receipts.length, 2);
  assert.equal(receipts[0].operation_id, receipts[1].operation_id);
  assert.equal(f.n.calls.filter((v) => v[0] === 'prompt').length, 1);
});
test('already_admitted 不授予第二次宿主调用许可', async (t) => {
  const f = await fixture(t, { already: true });
  await f.inbox.setReceive('1', true);
  await assert.rejects(f.inbox.deliver(f.delivery()), /second invocation/);
  assert.equal(f.n.calls.filter((v) => v[0] === 'prompt').length, 0);
});
