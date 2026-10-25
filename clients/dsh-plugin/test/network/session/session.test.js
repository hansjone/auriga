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
import { openLedger, digestOf } from '../../../src/network/host/ledger.js';
import { createMemoryLedgerStore } from '../../../src/network/host/store.js';
import {
  IDENTITY,
  createFakeHost,
  createFixturePlatform,
  peer,
  self,
} from '../host/fixtures.js';

/**
 * Minimal in-memory broker: READY → READY_RESULT, FETCH → page, RECEIPT → commit.
 * Speaks the same JSON shapes as ai-gateway network frames (unsigned stubs).
 */
function attachFakeBroker(transport, { epoch = '7', deliveries = [] } = {}) {
  let headSeq = deliveries.reduce(
    (m, d) => Math.max(m, Number(d.mail_seq)),
    0,
  );
  let committedCursor = 0;
  const inbox = [...deliveries];

  transport.onMessage(async (payload) => {
    const frame = decodeJson(payload);
    const type = frameType(frame);
    if (type === 'ready') {
      await transport.send(
        encodeJson({
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
        }),
      );
      return;
    }
    if (type === 'fetch') {
      if (frame.epoch !== epoch) {
        await transport.send(
          encodeJson({
            app_version: '1.0',
            type: 'error',
            request_id: frame.request_id,
            code: 'network.stale_epoch',
            scope: 'request',
            retryable: true,
          }),
        );
        return;
      }
      const resume = Number(frame.resume_cursor ?? 0);
      const taken = [];
      const rest = [];
      for (const d of inbox) {
        if (Number(d.mail_seq) > resume && taken.length < (frame.max_items ?? 16))
          taken.push(d);
        else rest.push(d);
      }
      inbox.length = 0;
      inbox.push(...rest);
      const scanAfter = taken.length
        ? Math.max(...taken.map((d) => Number(d.mail_seq)))
        : resume;
      await transport.send(
        encodeJson({
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
        }),
      );
      return;
    }
    if (type === 'receipt') {
      if (frame.epoch !== epoch) {
        await transport.send(
          encodeJson({
            app_version: '1.0',
            type: 'error',
            request_id: frame.request_id,
            code: 'network.stale_epoch',
            scope: 'request',
            retryable: true,
          }),
        );
        return;
      }
      const mailSeq = Number(frame.mail_seq);
      if (mailSeq > committedCursor) committedCursor = mailSeq;
      await transport.send(
        encodeJson({
          app_version: '1.0',
          type: 'receipt_committed',
          request_id: frame.request_id,
          operation_id: frame.operation_id,
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
        }),
      );
    }
  });

  return {
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
  };
  // Host digestOf uses camelCase fields; wire digest is independent attestation.
  const camel = {
    messageId: message.message_id,
    from: message.from,
    to: message.to,
    replyTo: message.reply_to,
    threadId: message.thread_id,
    threadSequence: message.thread_sequence,
    inReplyTo: null,
    kind: message.kind,
    intent: message.intent,
    messageClass: message.message_class,
    contactScope: message.contact_scope,
    senderKind: message.sender_kind,
    body: message.body,
  };
  return {
    app_version: '1.0',
    type: 'delivery',
    epoch: '7',
    delivery_id: 'dlv-fc-1',
    mail_seq: mailSeq,
    recipients: [
      {
        recipient_id: recipientId,
        local: '0',
        type: 'system',
        attempt_id: `att-${recipientId}-1`,
      },
    ],
    source_context: {
      message_digest: digestOf(camel),
      sender_kind: 'agent',
    },
    message,
  };
}

async function openWorld(deliveries) {
  const pipe = createMemoryPipe();
  const broker = attachFakeBroker(pipe.b, { epoch: '7', deliveries });
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

  await session.stop();
});

test('stale epoch FETCH_RESULT is dropped; ledger stays empty', async () => {
  const pipe = createMemoryPipe();
  // Broker answers READY with epoch 7, then we inject a page with epoch 6.
  attachFakeBroker(pipe.b, { epoch: '7', deliveries: [] });
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
