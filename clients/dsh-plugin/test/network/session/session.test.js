import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  createNetworkSession,
  createMemoryPipe,
  decodeJson,
  encodeJson,
  frameType,
  SessionError,
} from '../../../src/network/session/index.js';
import { TransportError } from '../../../src/network/transport/errors.js';
import { createHostAdapter } from '../../../src/network/host/adapter.js';
import { openLedger } from '../../../src/network/host/ledger.js';
import { signingFixture } from '../trust/fixtures.js';
import { digest } from '../../../src/network/trust/verifier.js';
import { createMemoryLedgerStore } from '../../../src/network/host/store.js';
import {
  IDENTITY,
  createFakeHost,
  createFixturePlatform,
  peer,
  self,
  boundSessionWithRelation,
} from '../host/fixtures.js';

/**
 * Minimal in-memory broker: READY → READY_RESULT, FETCH → page, RECEIPT → commit.
 * Uses temporary Ed25519 keys, including independently signed page entries.
 */
async function attachFakeBroker(
  transport,
  { epoch = '7', deliveries = [], corrupt = () => {} } = {},
) {
  const signer = await signingFixture();
  let channelNonce;
  function signed(value, requestId) {
    const ctx = { ...signer.context, epoch, channelNonce, requestId };
    if (value.type === 'fetch_result') {
      for (const item of value.items) {
        signer.seal(item, ctx);
        corrupt(item);
      }
      value.page.manifest = value.items.map((item) => ({
        item_type: item.type,
        mail_seq: item.mail_seq ?? null,
        digest: digest(item),
      }));
      signer.seal(value.page, ctx);
    } else signer.seal(value, ctx);
    return value;
  }
  const send = async (value, requestId) =>
    transport.send(encodeJson(signed(value, requestId)));
  let headSeq = deliveries.reduce((m, d) => Math.max(m, Number(d.mail_seq)), 0);
  let committedCursor = 0;
  const inbox = [...deliveries];

  transport.onMessage(async (payload) => {
    const frame = decodeJson(payload);
    const type = frameType(frame);
    if (type === 'ready') {
      channelNonce = frame.channel_nonce;
      await send(
        {
          app_version: '1.0',
          type: 'ready_result',
          request_id: frame.request_id,
          status: 'ready',
          epoch,
          head_seq: String(headSeq),
          committed_cursor: String(committedCursor),
          local_high_water: '0',
          registration_revision: '0',
          server_auth: { kid: 'stub-unsigned' },
        },
        frame.request_id,
      );
      return;
    }
    if (type === 'fetch') {
      if (frame.epoch !== epoch) {
        await send(
          {
            app_version: '1.0',
            type: 'error',
            request_id: frame.request_id,
            code: 'network.stale_epoch',
            scope: 'request',
            retryable: true,
          },
          frame.request_id,
        );
        return;
      }
      const resume = Number(frame.resume_cursor ?? 0);
      const taken = [];
      const rest = [];
      for (const d of inbox) {
        if (
          Number(d.mail_seq) > resume &&
          taken.length < (frame.max_items ?? 16)
        )
          taken.push(d);
        else rest.push(d);
      }
      inbox.length = 0;
      inbox.push(...rest);
      const scanAfter = taken.length
        ? Math.max(...taken.map((d) => Number(d.mail_seq)))
        : resume;
      await send(
        {
          app_version: '1.0',
          type: 'fetch_result',
          page: {
            app_version: '1.0',
            type: 'fetch_page',
            request_id: frame.request_id,
            epoch,
            head_seq: String(headSeq),
            committed_cursor: String(committedCursor),
            scan_after: String(scanAfter),
            snapshot_head: String(headSeq),
            next_continuation: null,
            manifest: taken.map((d) => ({
              item_type: 'delivery',
              mail_seq: d.mail_seq,
              digest: d.source_context.message_digest,
            })),
            server_auth: { kid: 'stub-unsigned' },
          },
          items: taken,
        },
        frame.request_id,
      );
      return;
    }
    if (type === 'receipt') {
      if (frame.epoch !== epoch) {
        await send(
          {
            app_version: '1.0',
            type: 'error',
            request_id: frame.request_id,
            code: 'network.stale_epoch',
            scope: 'request',
            retryable: true,
          },
          frame.request_id,
        );
        return;
      }
      const mailSeq = Number(frame.mail_seq);
      if (mailSeq > committedCursor) committedCursor = mailSeq;
      await send(
        {
          app_version: '1.0',
          type: 'receipt_committed',
          request_id: frame.request_id,
          operation_id: frame.operation_id,
          epoch,
          delivery_id: frame.delivery_id,
          mail_seq: frame.mail_seq,
          results: frame.results.map((r) => ({
            recipient_id: r.recipient_id,
            transport_status:
              r.status === 'host_accepted' ? 'host_accepted' : r.status,
            state_version: r.expected_state_version,
          })),
          mail_item_closed: true,
          committed_cursor: String(committedCursor),
          server_auth: { kid: 'stub-unsigned' },
        },
        frame.request_id,
      );
    }
  });

  return {
    signer,
    signed,
    get committedCursor() {
      return committedCursor;
    },
    push(delivery) {
      inbox.push(delivery);
      headSeq = Math.max(headSeq, Number(delivery.mail_seq));
    },
  };
}

function firstContactDelivery({
  recipientId = 'rcp-fc-sys',
  mailSeq = '10',
  text = '请求建立对话',
} = {}) {
  const message = {
    message_id: 'msg-fc-0001',
    from: peer('418'),
    to: self('0'),
    reply_to: peer('418'),
    thread_id: 'thread-fc',
    thread_sequence: '1',
    in_reply_to: null,
    kind: 'first_contact',
    message_class: 'unicast',
    sender_kind: 'agent',
    intent: 'request',
    contact_scope: 'conversation',
    body: { text },
    expires_at: '2026-10-26T00:00:00Z',
  };
  return {
    app_version: '1.0',
    type: 'delivery',
    epoch: '7',
    delivery_id: 'dlv-fc-1',
    mail_seq: mailSeq,
    target_node: { realm: '2', node: '64' },
    recipients: [
      {
        recipient_id: recipientId,
        local: '0',
        type: 'system',
        attempt_id: `att-${recipientId}-1`,
      },
    ],
    source_context: {
      message_digest: digest(message),
      sender_kind: 'agent',
      realm: '2',
      node: '63',
    },
    message,
  };
}

async function openWorld(deliveries, corrupt) {
  const pipe = createMemoryPipe();
  const broker = await attachFakeBroker(pipe.b, {
    epoch: '7',
    deliveries,
    corrupt,
  });
  const store = createMemoryLedgerStore();
  const ledger = await openLedger({
    store,
    identity: IDENTITY,
    initialize: true,
  });
  const host = createFakeHost();
  const platform = createFixturePlatform();
  platform.state.epoch = '7';
  const adapter = createHostAdapter({
    ledger,
    host,
    network: platform.network,
  });
  const session = createNetworkSession({
    transport: pipe.a,
    adapter,
    identity: IDENTITY,
    bootId: 'boot-test',
    trust: broker.signer.trust,
    now: broker.signer.now,
  });
  return { session, broker, host, ledger, adapter, platform };
}

test('FETCH page → ledger inbox + RECEIPT commit for system first_contact', async () => {
  const delivery = firstContactDelivery();
  const { session, broker, host, ledger } = await openWorld([delivery]);
  const prompts = [];
  session.systemInbox.subscribe((p) => prompts.push(p));

  await session.start();
  assert.equal(session.epoch, '7');
  assert.equal(session.ready, true);

  const { receipts, committed } = await session.pull();
  assert.equal(receipts.length, 1);
  assert.equal(receipts[0].status, 'host_accepted');
  assert.equal(receipts[0].acceptedBy, 'system');
  assert.equal(receipts[0].acceptanceMode, 'inbox_only');
  assert.deepEqual(committed, ['rcp-fc-sys']);

  const state = ledger.read();
  assert.ok(state.inbox['rcp-fc-sys']);
  assert.equal(state.contacts['msg-fc-0001'].state, 'awaiting_session');
  // Must not touch model / create session.
  assert.equal(host.counts.prompt + host.counts.create, 0);
  assert.equal(prompts.length, 1);
  assert.equal(prompts[0].contactId, 'msg-fc-0001');
  assert.match(prompts[0].previewText, /请求建立对话/);
  assert.ok(broker.committedCursor >= 10);
  // resume follows receipt commit prefix; scan_after is the page tip.
  assert.equal(session.resumeCursor, '10');
  assert.equal(session.scanAfter, '10');

  await session.stop();
});

test('scan_after advances without raising resume_cursor past committed prefix', async () => {
  const { session, broker } = await openWorld([]);
  await session.start();
  assert.equal(session.resumeCursor, '0');

  await session.applyFetchResult(
    broker.signed(
      {
        app_version: '1.0',
        type: 'fetch_result',
        page: {
          app_version: '1.0',
          type: 'fetch_page',
          request_id: 'x',
          epoch: '7',
          scan_after: '102',
          committed_cursor: '100',
        },
        items: [],
      },
      'x',
    ),
    'x',
  );
  assert.equal(session.scanAfter, '102');
  assert.equal(session.resumeCursor, '100');
  await session.stop();
});

test('mixed FETCH isolates bad signatures before ledger/admit/model while good delivery completes', async () => {
  const world = await openWorld([], (item) => {
    if (item.delivery_id === 'dlv-bad') item.message.body.text = 'forged';
  });
  const local = await boundSessionWithRelation(world, {
    threadId: 'thread-reply',
    requestId: 'outbound-1',
    peerLocal: '418',
  });
  function reply(id, seq) {
    const d = firstContactDelivery({ recipientId: `rcp-${id}`, mailSeq: seq });
    d.delivery_id = `dlv-${id}`;
    d.message.message_id = `msg-${id}`;
    d.message.kind = 'reply';
    d.message.to = self(local);
    d.message.thread_id = 'thread-reply';
    d.message.in_reply_to = 'outbound-1';
    delete d.message.contact_scope;
    d.recipients[0].local = local;
    d.recipients[0].type = 'session';
    d.source_context.message_digest = digest(d.message);
    return d;
  }
  world.broker.push(reply('bad', '10'));
  world.broker.push(reply('good', '11'));
  try {
    await world.session.start();
    const result = await world.session.pull();
    assert.deepEqual(result.committed, ['rcp-good']);
    const state = world.ledger.read();
    assert.equal(state.recipients['rcp-bad'], undefined);
    assert.equal(state.bodies['msg-bad'], undefined);
    assert.ok(state.recipients['rcp-good']);
    assert.equal(world.platform.state.calls.admit, 1);
    assert.equal(world.host.counts.prompt, 1);
    assert.equal(world.host.history('host-a1').length, 1);
    assert.equal(
      world.session.diagnostics[0].code,
      'network.signature_invalid',
    );
    assert.equal(world.session.diagnostics[0].index, 0);
    assert.equal(
      JSON.stringify(world.session.diagnostics).includes('rcp-bad'),
      false,
    );
  } finally {
    await world.session.stop();
  }
});

test('bad signed page header cannot advance cursors or reach ledger', async () => {
  const { session, broker, ledger } = await openWorld([]);
  try {
    await session.start();
    const value = broker.signed(
      {
        app_version: '1.0',
        type: 'fetch_result',
        page: {
          app_version: '1.0',
          type: 'fetch_page',
          epoch: '7',
          request_id: 'x',
          scan_after: '100',
          committed_cursor: '99',
        },
        items: [],
      },
      'x',
    );
    value.page.committed_cursor = '100';
    await assert.rejects(
      session.applyFetchResult(value, 'x'),
      (e) => e.code === 'network.signature_invalid',
    );
    assert.equal(session.resumeCursor, '0');
    assert.equal(session.scanAfter, '0');
    assert.deepEqual(Object.keys(ledger.read().recipients), []);
  } finally {
    await session.stop();
  }
});

test('unsigned READY cannot establish a session', async () => {
  const pipe = createMemoryPipe();
  const f = await signingFixture();
  pipe.b.onMessage(async (payload) => {
    const frame = decodeJson(payload);
    await pipe.b.send(
      encodeJson({
        app_version: '1.0',
        type: 'ready_result',
        request_id: frame.request_id,
        epoch: '7',
        status: 'ready',
      }),
    );
  });
  const session = createNetworkSession({
    transport: pipe.a,
    adapter: {},
    identity: IDENTITY,
    trust: f.trust,
    now: f.now,
  });
  try {
    await assert.rejects(
      session.start(),
      (e) => e.code === 'network.signature_required',
    );
    assert.equal(session.ready, false);
  } finally {
    await session.stop();
  }
});

test('FETCH_RESULT missing epoch is rejected by assertFrameEpoch', async () => {
  const { session } = await openWorld([]);
  await session.start();
  await assert.rejects(
    () =>
      session.applyFetchResult({
        app_version: '1.0',
        type: 'fetch_result',
        page: {
          request_id: 'x',
          scan_after: '10',
          committed_cursor: '0',
        },
        items: [],
      }),
    (e) => e instanceof SessionError && e.code === 'missing_epoch',
  );
  await session.stop();
});

test('stale epoch FETCH_RESULT is dropped; ledger stays empty', async () => {
  const pipe = createMemoryPipe();
  // Broker answers READY with epoch 7, then we inject a page with epoch 6.
  const broker = await attachFakeBroker(pipe.b, { epoch: '7', deliveries: [] });
  const store = createMemoryLedgerStore();
  const ledger = await openLedger({
    store,
    identity: IDENTITY,
    initialize: true,
  });
  const host = createFakeHost();
  const platform = createFixturePlatform();
  platform.state.epoch = '7';
  const adapter = createHostAdapter({
    ledger,
    host,
    network: platform.network,
  });
  const session = createNetworkSession({
    transport: pipe.a,
    adapter,
    identity: IDENTITY,
    trust: broker.signer.trust,
    now: broker.signer.now,
  });
  await session.start();

  const stale = firstContactDelivery({ recipientId: 'rcp-stale' });
  stale.epoch = '6';
  // Directly apply would check epoch inside applyFetchResult.
  await assert.rejects(
    () =>
      session.applyFetchResult({
        app_version: '1.0',
        type: 'fetch_result',
        page: {
          request_id: 'x',
          epoch: '6',
          scan_after: '10',
          committed_cursor: '0',
        },
        items: [stale],
      }),
    (e) => e instanceof SessionError && e.code === 'stale_epoch',
  );
  assert.equal(Object.keys(ledger.read().inbox).length, 0);
  await session.stop();
});

test('memory pipe refuses beyond hard queue (does not widen #128 ceilings)', async () => {
  const pipe = createMemoryPipe({
    queueMaxMessages: 2,
    queueMaxBytes: 200,
    subjectBytes: 24,
  });
  // No consumer on b → queue fills on a.
  const payload = new TextEncoder().encode('{"type":"ping"}');
  await pipe.a.send(payload);
  await pipe.a.send(payload);
  await assert.rejects(
    () => pipe.a.send(payload),
    (e) => e instanceof TransportError && e.code === 'queue_full',
  );
});
