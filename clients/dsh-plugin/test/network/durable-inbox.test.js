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
import { createDeviceMessaging } from '../../src/network/durable/capability.js';
import { setImmediate } from 'node:timers/promises';

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
        registrationId: 'registration-' + r.local,
        opId: r.opId,
        local: r.local,
        addressType: 'session',
        fingerprint: r.fingerprint,
        registrationRevision: r.local,
        localHighWater: r.local,
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
      if (f.type === 'receive_set') {
        await options.receiveSet?.(f);
        result = {
          type: 'receive_set_result',
          registration_id: f.registration.registration_id,
          enabled: f.enabled,
          state_version: (BigInt(f.expected_version) + 1n).toString(),
        };
      } else if (f.type === 'attempt_begin')
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
          committed_cursor: options.receiptCursor ?? f.mail_seq,
        };
      } else if (f.type === 'fetch') {
        await options.beforeFetch?.(f);
        const first = options.respectResume
          ? deliveries.findIndex(
              (item) => BigInt(item.mail_seq) > BigInt(f.resume_cursor),
            )
          : 0;
        const offset =
          f.continuation === null
            ? first < 0
              ? deliveries.length
              : first
            : f.continuation === 'opaque-next'
              ? 1
              : Number(f.continuation.slice(12));
        const raw = deliveries.slice(offset, offset + (options.pageSize ?? 1));
        const items = raw.map((item) =>
          signer.seal(structuredClone(item), context),
        );
        const nextOffset = offset + raw.length;
        const page = signer.seal(
          {
            app_version: '1.0',
            type: 'fetch_page',
            request_id: f.request_id,
            epoch: '7',
            head_seq: deliveries.at(-1)?.mail_seq ?? '0',
            committed_cursor: options.committedCursor ?? '0',
            scan_after: raw.at(-1)?.mail_seq ?? f.resume_cursor,
            snapshot_head: deliveries.at(-1)?.mail_seq ?? '0',
            next_continuation:
              nextOffset < deliveries.length
                ? nextOffset === 1
                  ? 'opaque-next'
                  : 'opaque-next-' + nextOffset
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
  function delivery(seq = '1', local = '1') {
    const message = {
      message_id: 'message-' + seq,
      kind: 'message',
      message_class: 'unicast',
      sender_kind: 'agent',
      from: peer('1'),
      to: self(local),
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
          local,
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
  async function join(id) {
    n.seed(id);
    const agent = n.attach(id);
    assert.equal(
      (await tools.get('auriga_network_join').execute({}, { agent })).status,
      'registered',
    );
    return agent;
  }
  return {
    n,
    ledger,
    store,
    inbox,
    requests,
    deliveries,
    delivery,
    terminal,
    join,
    tools,
    transport,
    ready,
    trust: signer.trust,
    now: signer.now,
    signal: abort.signal,
    head() {
      const head = signer.seal(
        { app_version: '1.0', type: 'head', epoch: '7', head_seq: '0' },
        { ...signer.context, requestId: null },
      );
      for (const listener of listeners) listener(encodeJson(head), 1);
    },
  };
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

test('关闭接收的A不阻塞同页B及continuation，保留A原邮件且游标不跨缺口', async (t) => {
  const options = { pageSize: 2 };
  const f = await fixture(t, options);
  await f.join('session-b');
  await f.inbox.setReceive('1', true);
  await f.inbox.setReceive('2', true);
  await f.inbox.setReceive('1', false);
  const blocked = f.delivery('1');
  f.deliveries.push(blocked, f.delivery('2', '2'), f.delivery('3', '2'));
  await f.inbox.pull();
  const saved = f.store.snapshot().durable;
  assert.deepEqual(
    saved.inbound['recipient-1'].delivery.message,
    blocked.message,
  );
  assert.equal(saved.inbound['recipient-1'].stage, 'received');
  assert.equal(saved.inbound['recipient-2'].stage, 'committed');
  assert.equal(saved.inbound['recipient-3'].stage, 'committed');
  assert.equal(saved.resumeCursor, '0');
  assert.deepEqual(
    f.requests.filter((v) => v.type === 'fetch').map((v) => v.continuation),
    [null, 'opaque-next-2'],
  );
  assert.equal(f.n.calls.filter((v) => v[0] === 'prompt').length, 2);
  assert.deepEqual(f.inbox.failures(), [
    { scope: 'recipient', id: 'recipient-1', code: 'receive_none' },
  ]);
  const original = saved.inbound['recipient-1'];
  await f.inbox.setReceive('1', true);
  options.receiptCursor = '3';
  options.committedCursor = '3';
  await f.inbox.pull();
  assert.equal(f.store.snapshot().durable.resumeCursor, '3');
  assert.deepEqual(
    f.store.snapshot().durable.inbound['recipient-1'].delivery,
    original.delivery,
  );
  assert.equal(
    f.store.snapshot().durable.inbound['recipient-1'].attemptId,
    original.attemptId,
  );
  assert.equal(f.n.calls.filter((v) => v[0] === 'prompt').length, 3);
  assert.deepEqual(f.inbox.failures(), []);
});

test('单会话provider失败不阻塞其他会话，恢复仅flush且不重复prompt', async (t) => {
  const options = { pageSize: 2 };
  const f = await fixture(t, options);
  await f.join('session-b');
  await f.inbox.setReceive('1', true);
  await f.inbox.setReceive('2', true);
  const flush = f.n.ctx.sessions.flush;
  f.n.ctx.sessions.flush = async (session) =>
    session.id === 'session-a' ? false : flush(session);
  f.deliveries.push(f.delivery('1'), f.delivery('2', '2'));
  await f.inbox.pull();
  assert.equal(
    f.store.snapshot().durable.inbound['recipient-1'].stage,
    'invoking',
  );
  assert.equal(
    f.store.snapshot().durable.inbound['recipient-2'].stage,
    'committed',
  );
  assert.equal(f.store.snapshot().durable.resumeCursor, '0');
  f.n.ctx.sessions.flush = flush;
  options.receiptCursor = '2';
  options.committedCursor = '2';
  await f.inbox.pull();
  assert.equal(f.store.snapshot().durable.resumeCursor, '2');
  assert.equal(f.n.calls.filter((v) => v[0] === 'prompt').length, 2);
});

test('129条合法签名终态分页能跨128边界推进且再次拉取不再卡住', async (t) => {
  const f = await fixture(t, { pageSize: 32, committedCursor: '129' });
  for (let seq = 1; seq <= 129; seq++)
    f.deliveries.push(f.terminal(String(seq)));
  await f.inbox.pull();
  assert.equal(Object.keys(f.store.snapshot().durable.terminal).length, 129);
  assert.equal(f.store.snapshot().durable.resumeCursor, '129');
  assert.equal(f.requests.filter((v) => v.type === 'fetch').length, 5);
  assert.equal(f.requests.filter((v) => v.type !== 'fetch').length, 0);
  const lagged = f.ledger.read();
  lagged.durable.resumeCursor = '0';
  assert.equal(hasCursorEvidence(lagged, '129'), true);
  delete lagged.durable.terminal['128'];
  assert.equal(hasCursorEvidence(lagged, '129'), false);
  assert.equal(hasCursorEvidence(lagged, '18446744073709551615'), false);
  await f.inbox.pull();
  assert.equal(
    f.requests.filter((v) => v.type === 'fetch').at(-1).resume_cursor,
    '129',
  );
});

test('单项隔离不吞账本持久化失败，不向后续健康会话投递或推进游标', async (t) => {
  const f = await fixture(t, {
    pageSize: 2,
    beforeCommit(state) {
      if (state.durable?.inbound['recipient-1'])
        throw new Error('intake storage failed');
    },
  });
  await f.join('session-b');
  await f.inbox.setReceive('1', true);
  await f.inbox.setReceive('2', true);
  f.deliveries.push(f.delivery('1'), f.delivery('2', '2'));
  await assert.rejects(f.inbox.pull(), { code: 'host.ledger_unavailable' });
  assert.equal(f.store.snapshot().durable.resumeCursor, '0');
  assert.deepEqual(f.store.snapshot().durable.inbound, {});
  assert.equal(f.n.calls.filter((v) => v[0] === 'prompt').length, 0);
  assert.equal(f.requests.filter((v) => v.type === 'receipt').length, 0);
});

test('129页终态积压跨多轮pull持久推进，保留续页原resume且后续正常邮件仍可处理', async (t) => {
  const savedCursors = [];
  const options = {
    respectResume: true,
    committedCursor: '129',
    receiptCursor: '130',
    beforeFetch() {
      savedCursors.push(f.store.snapshot().durable.resumeCursor);
    },
  };
  const f = await fixture(t, options);
  await f.inbox.setReceive('1', true);
  for (let seq = 1; seq <= 129; seq++)
    f.deliveries.push(f.terminal(String(seq)));
  f.deliveries.push(f.delivery('130'));
  await assert.rejects(f.inbox.pull(), /FETCH page bound reached/);
  const first = f.store.snapshot().durable;
  assert.equal(first.resumeCursor, '128');
  assert.equal(Object.keys(first.terminal).length, 128);
  assert.deepEqual(
    savedCursors,
    Array.from({ length: 128 }, (_, i) => String(i)),
  );
  assert.equal(f.n.calls.filter((v) => v[0] === 'prompt').length, 0);
  await f.inbox.pull();
  const after = f.store.snapshot().durable;
  assert.equal(after.resumeCursor, '130');
  assert.equal(Object.keys(after.terminal).length, 129);
  assert.deepEqual(after.terminal['1'], first.terminal['1']);
  assert.equal(after.inbound['recipient-130'].stage, 'committed');
  assert.equal(f.n.calls.filter((v) => v[0] === 'prompt').length, 1);
  const fetches = f.requests.filter((v) => v.type === 'fetch');
  assert.equal(fetches.length, 130);
  assert.ok(fetches.slice(0, 128).every((v) => v.resume_cursor === '0'));
  assert.ok(fetches.slice(128).every((v) => v.resume_cursor === '128'));
  assert.equal(
    fetches[128].continuation,
    null,
    'new pull must start a fresh continuation for its new resume',
  );
  options.committedCursor = '130';
  await f.inbox.pull();
  assert.equal(
    f.requests.filter((v) => v.type === 'fetch').at(-1).resume_cursor,
    '130',
  );
  assert.equal(f.store.snapshot().durable.resumeCursor, '130');
  assert.equal(f.n.calls.filter((v) => v[0] === 'prompt').length, 1);
});

test('逐页推进只保存缺口前完整证据，补齐缺口后从原游标恢复', async (t) => {
  const f = await fixture(t, { respectResume: true, committedCursor: '3' });
  f.deliveries.push(f.terminal('1'), f.terminal('3'));
  await assert.rejects(f.inbox.pull(), /cursor evidence missing/);
  assert.equal(f.store.snapshot().durable.resumeCursor, '1');
  assert.deepEqual(Object.keys(f.store.snapshot().durable.terminal), [
    '1',
    '3',
  ]);
  assert.ok(f.requests.every((v) => v.resume_cursor === '0'));
  f.deliveries.splice(1, 0, f.terminal('2'));
  await f.inbox.pull();
  assert.equal(f.store.snapshot().durable.resumeCursor, '3');
  assert.ok(f.requests.slice(2).every((v) => v.resume_cursor === '1'));
});

test('逐页推进仍拒绝重复continuation，不确认非法续页后的游标', async (t) => {
  let repeat = true;
  const f = await fixture(t, {
    respectResume: true,
    committedCursor: '3',
    fetchResult(page, items, context, signer) {
      if (repeat && items[0]?.mail_seq === '2') {
        page.next_continuation = 'opaque-next';
        signer.seal(page, context);
      }
    },
  });
  f.deliveries.push(f.terminal('1'), f.terminal('2'), f.terminal('3'));
  await assert.rejects(f.inbox.pull(), /invalid FETCH continuation/);
  assert.equal(f.store.snapshot().durable.resumeCursor, '1');
  assert.equal(f.store.snapshot().durable.terminal['3'], undefined);
  repeat = false;
  await f.inbox.pull();
  assert.equal(f.store.snapshot().durable.resumeCursor, '3');
});

test('后续页游标持久化失败保留上页前缀，不请求下一页', async (t) => {
  const f = await fixture(t, {
    respectResume: true,
    committedCursor: '3',
    beforeCommit(state) {
      if (state.durable?.resumeCursor === '2')
        throw new Error('cursor storage failed');
    },
  });
  f.deliveries.push(f.terminal('1'), f.terminal('2'), f.terminal('3'));
  await assert.rejects(f.inbox.pull(), /cursor storage failed/);
  assert.equal(f.store.snapshot().durable.resumeCursor, '1');
  assert.equal(f.requests.length, 2);
  assert.deepEqual(Object.keys(f.store.snapshot().durable.terminal), [
    '1',
    '2',
  ]);
});

for (const invalid of ['signature', 'manifest'])
  test(`后续页${invalid}非法不能越过已保存前缀`, async (t) => {
    const f = await fixture(t, {
      respectResume: true,
      committedCursor: '3',
      fetchResult(page, items, context, signer) {
        if (items[0]?.mail_seq !== '2') return;
        if (invalid === 'signature')
          page.server_auth.signature = 'A'.repeat(86);
        else {
          page.manifest[0].digest = 'sha256:' + '0'.repeat(64);
          signer.seal(page, context);
        }
      },
    });
    f.deliveries.push(f.terminal('1'), f.terminal('2'), f.terminal('3'));
    await assert.rejects(f.inbox.pull());
    assert.equal(f.store.snapshot().durable.resumeCursor, '1');
    assert.deepEqual(Object.keys(f.store.snapshot().durable.terminal), ['1']);
    assert.equal(f.requests.length, 2);
  });

test('128边界后的缺口仅推进完整前缀，补回原证据后继续且不越界', async (t) => {
  const f = await fixture(t, { pageSize: 32, committedCursor: '130' });
  for (let seq = 1; seq <= 130; seq++)
    if (seq !== 129) f.deliveries.push(f.terminal(String(seq)));
  await assert.rejects(f.inbox.pull(), /cursor evidence missing/);
  assert.equal(f.store.snapshot().durable.resumeCursor, '128');
  f.deliveries.splice(128, 0, f.terminal('129'));
  await f.inbox.pull();
  assert.equal(f.store.snapshot().durable.resumeCursor, '130');
});

test('失效会话pending恢复保留原身份并继续健康会话设置与已接受回执对账', async (t) => {
  let unavailable = true;
  const f = await fixture(t, {
    loseReceipt: true,
    async receiveSet() {
      if (unavailable) throw new Error('ack lost');
    },
  });
  await f.join('session-b');
  await assert.rejects(f.inbox.setReceive('1', true), /ack lost/);
  await assert.rejects(f.inbox.setReceive('2', true), /ack lost/);
  const pending = f.store.snapshot().durable.receive['1'].pending;
  f.n.agents.delete('session-a');
  unavailable = false;
  await f.inbox.recover();
  assert.deepEqual(f.store.snapshot().durable.receive['1'].pending, pending);
  assert.equal(f.store.snapshot().durable.receive['2'].enabled, true);
  assert.deepEqual(f.inbox.failures(), [
    { scope: 'receive', id: '1', code: 'network.local_registration_required' },
  ]);
  await assert.rejects(
    f.inbox.deliver(f.delivery('1', '2')),
    /receipt response lost/,
  );
  await f.inbox.recover();
  assert.equal(
    f.store.snapshot().durable.inbound['recipient-1'].stage,
    'committed',
  );
  f.n.attach('session-a');
  await f.inbox.recover();
  const retried = f.requests.filter(
    (v) => v.type === 'receive_set' && v.registration.local === '1',
  );
  assert.equal(retried.length, 2);
  assert.equal(retried[1].request_id, pending.request_id);
  assert.deepEqual(
    structuredClone(retried[1].registration),
    pending.registration,
  );
  assert.equal(f.store.snapshot().durable.receive['1'].pending, null);
  assert.deepEqual(f.inbox.failures(), []);
});

test('pending设置确认丢失且原会话卸载，messenger仍提供工具、健康会话收件与设置对账入口', async (t) => {
  let unavailable = true;
  const f = await fixture(t, {
    async receiveSet() {
      if (unavailable) throw new Error('ack lost');
    },
  });
  await f.join('session-b');
  await assert.rejects(f.inbox.setReceive('1', true), /ack lost/);
  const pending = f.store.snapshot().durable.receive['1'].pending;
  f.n.agents.delete('session-a');
  unavailable = false;
  const messaging = createDeviceMessaging({
    ctx: f.n.ctx,
    ledger: f.ledger,
    prepared: {
      submit: async () => {
        throw new Error('unused');
      },
    },
    source: {},
    transport: f.transport,
    ready: f.ready,
    trust: f.trust,
    signal: f.signal,
    assertCurrent() {},
    now: f.now,
  });
  t.after(async () => {
    messaging.close();
    await messaging.idle();
  });
  await messaging.start();
  await messaging.idle();
  assert.equal(messaging.status.available, true);
  assert.equal(f.tools.has('auriga_send_message'), true);
  assert.deepEqual((await messaging.snapshot()).failures, [
    { scope: 'receive', id: '1', code: 'network.local_registration_required' },
  ]);
  assert.deepEqual(f.store.snapshot().durable.receive['1'].pending, pending);
  f.deliveries.push(f.delivery('1', '2'));
  await messaging.setReceive({ local: '2', enabled: true });
  await messaging.idle();
  assert.equal(
    f.store.snapshot().durable.inbound['recipient-1'].stage,
    'committed',
  );
  assert.equal(f.n.calls.filter((v) => v[0] === 'prompt').length, 1);
  f.n.attach('session-a');
  await messaging.setReceive({ local: '1', enabled: true });
  const retried = f.requests
    .filter((v) => v.type === 'receive_set' && v.registration.local === '1')
    .at(-1);
  assert.equal(retried.request_id, pending.request_id);
  assert.equal(f.store.snapshot().durable.receive['1'].pending, null);
  assert.equal(messaging.status.available, true);
  assert.equal(f.tools.has('auriga_send_message'), true);
  assert.deepEqual((await messaging.snapshot()).failures, []);
});

test('同连接HEAD、定时及接收控制同时触发时生产FETCH不重入也不积压轮询', async (t) => {
  t.mock.timers.enable({ apis: ['setInterval'] });
  let release, entered;
  const gate = new Promise((resolve) => {
    release = resolve;
  });
  const first = new Promise((resolve) => {
    entered = resolve;
  });
  let active = 0,
    peak = 0,
    count = 0;
  const f = await fixture(t, {
    async beforeFetch() {
      active++;
      peak = Math.max(peak, active);
      if (++count === 1) {
        entered();
        await gate;
      }
      active--;
    },
  });
  const messaging = createDeviceMessaging({
    ctx: f.n.ctx,
    ledger: f.ledger,
    prepared: {
      submit: async () => {
        throw new Error('unused');
      },
    },
    source: {},
    transport: f.transport,
    ready: f.ready,
    trust: f.trust,
    signal: f.signal,
    assertCurrent() {},
    now: f.now,
  });
  t.after(async () => {
    release();
    messaging.close();
    await messaging.idle();
  });
  await messaging.start();
  await first;
  for (let i = 0; i < 32; i++) f.head();
  t.mock.timers.tick(5000);
  await messaging.setReceive({ local: '1', enabled: true });
  await setImmediate();
  assert.equal(count, 1);
  assert.equal(
    messaging.status.reason,
    'ready',
    'HEAD signatures must be accepted while FETCH is blocked',
  );
  release();
  await messaging.idle();
  assert.equal(
    count,
    1,
    'busy triggers must coalesce instead of queueing extra FETCH',
  );
  t.mock.timers.tick(1000);
  await messaging.idle();
  assert.equal(count, 2, 'poll resumes after the previous FETCH completes');
  assert.equal(peak, 1, 'only one production FETCH can be in flight per C');
  assert.equal(f.requests.filter((v) => v.type === 'fetch').length, 2);
  assert.equal(messaging.status.reason, 'ready');
  messaging.close();
  t.mock.timers.tick(5000);
  await setImmediate();
  assert.equal(count, 2, 'closing removes the poll timer');
});

test('同连接重复pull与continuation共用FETCH锁，服务端单并发槽始终不超限', async (t) => {
  let release, entered;
  const gate = new Promise((resolve) => {
    release = resolve;
  });
  const first = new Promise((resolve) => {
    entered = resolve;
  });
  let active = 0,
    peak = 0,
    count = 0;
  const f = await fixture(t, {
    committedCursor: '2',
    async beforeFetch() {
      active++;
      peak = Math.max(peak, active);
      if (++count === 1) {
        entered();
        await gate;
      }
      await setImmediate();
      active--;
    },
  });
  f.deliveries.push(f.terminal('1'), f.terminal('2'));
  const pulling = f.inbox.pull();
  await first;
  const again = f.inbox.pull();
  await setImmediate();
  assert.equal(count, 1);
  release();
  await Promise.all([pulling, again]);
  assert.equal(peak, 1);
  assert.equal(count, 4);
  assert.deepEqual(
    f.requests.map((v) => v.continuation),
    [null, 'opaque-next', null, 'opaque-next'],
  );
  assert.equal(f.store.snapshot().durable.resumeCursor, '2');
});
