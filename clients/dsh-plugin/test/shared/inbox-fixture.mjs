/* global structuredClone */
import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { fixture } from './production-message-fixture.mjs';
import { iso } from './message-fixture.mjs';
import { createMemoryLedgerStore } from '../../src/network/host/store.js';
import { openSharedMessageReceiver } from '../../src/shared/message-receiver.js';
import { digest } from '../../src/network/trust/verifier.js';

/** Real production ports + formal v2 signatures + original DurableInbox and
 * NativeHostPort public API fixture. No fake receive engine or owner ledger. */
export function inboxFixture(options = {}) {
  const f = fixture(),
    deliveries = [],
    requests = [],
    chronology = [];
  const store = createMemoryLedgerStore({
    beforeCommit(value) {
      chronology.push(['persist', value.state.durable.inbound]);
      return options.beforeCommit?.(value);
    },
  });
  const mailbox = {
    receiveEnabled: false,
    receiveVersion: '1',
    headSeq: '0',
    committedCursor: '0',
  };
  f.state.change = (response) => {
    response.mailbox = { ...mailbox };
    options.changeContext?.(response);
  };
  let committed = '0';
  f.state.afterReceive = async (_result, body) => {
    const frame = body.frame;
    requests.push(frame);
    chronology.push(['request', frame.type]);
    await options.beforeReceive?.(frame);
    let result;
    if (frame.type === 'receive_set') {
      mailbox.receiveEnabled = frame.enabled;
      mailbox.receiveVersion = (BigInt(frame.expected_version) + 1n).toString();
      result = {
        type: 'receive_set_result',
        registration_id: frame.registration.registration_id,
        enabled: frame.enabled,
        state_version: mailbox.receiveVersion,
      };
    } else if (frame.type === 'attempt_begin')
      result = {
        type: 'attempt_result',
        recipient_id: frame.recipient_id,
        attempt_id: frame.attempt_id,
        result: 'entered',
      };
    else if (frame.type === 'execution_admit')
      result = {
        type: 'admit_result',
        recipient_id: frame.recipient_id,
        result: 'admitted',
        admitted_epoch: f.latest().connection.epoch,
        state_version: '9',
        admission_id: randomUUID(),
      };
    else if (frame.type === 'receipt') {
      const record =
        store.snapshot().state.durable.inbound[frame.results[0].recipient_id];
      assert.equal(
        record.stage,
        'accepted',
        'persisted acceptance must precede wire receipt',
      );
      assert.ok(f.calls.some(([name]) => name === 'persistence-flush'));
      await options.receipt?.(frame);
      committed = frame.mail_seq;
      mailbox.committedCursor = committed;
      result = {
        type: 'receipt_committed',
        operation_id: frame.operation_id,
        delivery_id: frame.delivery_id,
        mail_seq: frame.mail_seq,
        results: [
          {
            recipient_id: frame.results[0].recipient_id,
            transport_status: 'host_accepted',
            state_version: '10',
          },
        ],
        mail_item_closed: true,
        committed_cursor: committed,
      };
    } else if (frame.type === 'fetch') {
      const items = deliveries
        .filter((item) => BigInt(item.mail_seq) > BigInt(frame.resume_cursor))
        .map((item) => f.seal(structuredClone(item), frame.request_id));
      await options.items?.(items);
      const head = deliveries.at(-1)?.mail_seq ?? '0';
      mailbox.headSeq = head;
      const page = f.seal(
        {
          app_version: '1.0',
          type: 'fetch_page',
          request_id: frame.request_id,
          epoch: f.latest().connection.epoch,
          head_seq: head,
          committed_cursor: committed,
          scan_after: items.at(-1)?.mail_seq ?? frame.resume_cursor,
          snapshot_head: head,
          next_continuation: null,
          manifest: items.map((item) => ({
            item_type: item.type,
            mail_seq: item.mail_seq,
            digest: digest(item),
          })),
        },
        frame.request_id,
      );
      return { app_version: '1.0', type: 'fetch_result', page, items };
    } else throw new Error('unexpected frame');
    return f.seal(
      { app_version: '1.0', request_id: frame.request_id, ...result },
      frame.request_id,
    );
  };
  const delivery = () => {
    const connection = f.latest().connection;
    const message = {
      message_id: randomUUID(),
      kind: 'message',
      message_class: 'unicast',
      sender_kind: 'agent',
      from: {
        version: 1,
        realm: connection.realm,
        node: '63',
        local: '1',
        type: 'session',
      },
      to: {
        version: 1,
        realm: connection.realm,
        node: connection.node,
        local: f.registration.local,
        type: 'session',
      },
      body: { subtype: 'text', text: '真实持久接收测试正文' },
      expires_at: iso(f.state.now + 120_000),
    };
    const seq = String(deliveries.length + 1);
    return {
      app_version: '1.0',
      type: 'delivery',
      epoch: connection.epoch,
      delivery_id: randomUUID(),
      mail_seq: seq,
      target_node: { realm: connection.realm, node: connection.node },
      message,
      source_context: {
        realm: message.from.realm,
        node: message.from.node,
        sender_kind: 'agent',
        message_digest: digest(message),
      },
      recipients: [
        {
          recipient_id: randomUUID(),
          local: f.registration.local,
          type: 'session',
          state_version: '9',
        },
      ],
    };
  };
  const receivers = new Set();
  const open = async (assertCurrent) => {
    const scope = await f.ports.openMessageInbox(f.binding);
    const receiver = await openSharedMessageReceiver(f.ctx, {
      scope,
      store,
      assertCurrent,
    });
    receivers.add(receiver);
    return receiver;
  };
  return {
    ...f,
    store,
    deliveries,
    requests,
    chronology,
    delivery,
    openReceiver: open,
    mailbox,
    async close() {
      for (const receiver of receivers) await receiver.close();
      await f.close();
    },
  };
}
