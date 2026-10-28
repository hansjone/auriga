import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { waitUntil } from './assembly-process.mjs';

export const persistenceMarker =
  'SEND-3 original submission must be durable before HTTPS';
export const receiptMarker =
  'SEND-3 receipt must use server recipient.state_version';

/** Same real services and PG as T3b/T4/SEND-2. External sends to member so the
 * original SEND-2 target's receive_version=1 assertion remains intact. */
export async function pluginSend({
  member: target,
  external: sender,
  snapshot,
  checks,
  artifacts,
}) {
  await waitUntil(
    () =>
      sender.tools.get('auriga_send_message') &&
      target.runtime.diagnostics.messaging.available,
    'SEND-3 production capability missing',
  );
  const sourceBefore = await sender.ledger();
  const targetBefore = await target.ledger();
  const binding = targetBefore.bindings['1'];
  assert.equal(binding.hostSessionId, target.agent.id);
  assert.equal(binding.receiveMode, 'receive_none');
  const sourceBinding = sourceBefore.sourceControl.entries['1'].head;
  const ui = target.runtime.messagingUI(target.localControl);
  const initial = await ui.snapshot();
  assert.equal(initial.sessions.find((v) => v.local === '1').enabled, false);
  const receive = await ui.setReceive({ local: '1', enabled: true });
  assert.equal(receive.enabled, true);
  assert.equal(receive.version, '2');
  const targetAddress = initial.sessions.find((v) => v.local === '1').address;
  const body = {
    subtype: 'text',
    text: 'SEND-3 production ToolRuntime → HTTPS → PG → native durable session',
  };
  const before = await snapshot('plugin-send-before');
  const callId = randomUUID();
  const dispatched = await sender.native.ctx.tools.execute({
    callId,
    name: 'auriga_send_message',
    arguments: { target: targetAddress, body },
    agent: sender.agent,
    signal: new AbortController().signal,
  });
  const observed = sender.submissions.at(-1);
  assert.ok(observed?.durable, persistenceMarker);
  assert.equal(dispatched.isError, false, 'SEND-3 ToolRuntime dispatch failed');
  assert.equal(
    dispatched.value.status,
    'request_submitted',
    'SEND-3 production sending failed',
  );
  const saved = await sender.ledger();
  const submission = Object.values(saved.durable.submissions).find(
    (v) => v.request.idempotency.key === observed.key,
  );
  assert.ok(submission?.result, 'SEND-3 original SendResult must be durable');
  assert.equal(
    submission.request.idempotency.producer_epoch,
    sourceBefore.durable.producerEpoch,
  );
  assert.equal(submission.request.source.source_binding, sourceBinding);
  const result = JSON.parse(submission.result);
  assert.equal(result.status, 'platform_stored');
  assert.equal(result.from.node, sourceBefore.identity.node);
  assert.equal(result.to.node, targetBefore.identity.node);
  const sent = sender.native.events.find(
    (v) => v.event === 'tools/execute' && v.callId === callId,
  );
  assert.ok(
    sent?.current && sent.sessionCurrent,
    'SEND-3 genuine native ToolRuntime execution required',
  );
  const received = await waitUntil(
    async () => {
      const delivery = target.frames
        .flatMap((v) =>
          v.direction === 'down' && v.frame.type === 'fetch_result'
            ? v.frame.items
            : [],
        )
        .find((item) => item.message?.message_id === result.message_id);
      const receipt = target.frames.find(
        (v) =>
          v.direction === 'up' &&
          v.frame.type === 'receipt' &&
          v.frame.delivery_id === delivery?.delivery_id,
      )?.frame;
      if (receipt)
        assert.equal(
          receipt.results[0].expected_state_version,
          delivery.recipients[0].state_version,
          receiptMarker,
        );
      const s = await target.ledger();
      return Object.values(s.durable.inbound).find(
        (v) =>
          v.delivery.message.message_id === result.message_id &&
          v.stage === 'committed',
      );
    },
    'SEND-3 native acceptance and RECEIPT_COMMITTED missing',
    30000,
  );
  const delivery = target.frames
    .find(
      (v) =>
        v.direction === 'down' &&
        v.frame.type === 'fetch_result' &&
        v.frame.items.some(
          (item) => item.message?.message_id === result.message_id,
        ),
    )
    .frame.items.find((item) => item.message?.message_id === result.message_id);
  const request = target.frames.find(
    (v) =>
      v.direction === 'up' &&
      v.frame.type === 'receipt' &&
      v.frame.results.some((r) => r.recipient_id === received.recipientId),
  ).frame;
  assert.equal(
    request.results[0].expected_state_version,
    delivery.recipients[0].state_version,
    receiptMarker,
  );
  const committed = target.frames.find(
    (v) =>
      v.direction === 'down' &&
      v.frame.type === 'receipt_committed' &&
      v.frame.operation_id === request.operation_id,
  )?.frame;
  assert.ok(committed, 'SEND-3 authentic receipt commit missing');
  const record = await target.native.ctx.sessionController.inspect(
    target.agent.id,
  );
  assert.ok(
    JSON.stringify(record).includes(body.text),
    'SEND-3 target native session must contain the message body',
  );
  const reader = await target.native.ctx.sessionPersistence.open(
    target.agent.id,
    'read',
  );
  let persisted;
  try {
    persisted = await reader.read();
  } finally {
    await reader.close();
  }
  assert.ok(
    JSON.stringify(persisted).includes(body.text),
    'SEND-3 native provider must durably retain the message body',
  );
  const db = await snapshot('plugin-send-committed');
  for (const name of [
    'network_submit',
    'network_message',
    'network_mail_item',
    'network_recipient',
  ])
    assert.equal(
      db['collab.' + name].length,
      before['collab.' + name].length + 1,
      'SEND-3 single durable ' + name,
    );
  const server = db['collab.network_recipient'].find(
    (v) => v.id === received.recipientId,
  );
  assert.equal(server.transport_status, 'host_accepted');
  assert.equal(server.host_request_id, received.recipientId);
  // The original production provider, same stored request/key; no driver key
  // allocation or fabricated business result during the reconciliation check.
  const replay = await sender.prepared.submit(
    submission.request,
    new AbortController().signal,
  );
  assert.equal(
    Buffer.from(replay).toString(),
    submission.result,
    'SEND-3 original same-key result bytes',
  );
  const afterReplay = await snapshot('plugin-send-replay');
  assert.equal(
    afterReplay['collab.network_message'].length,
    db['collab.network_message'].length,
  );
  await ui.setReceive({ local: '1', enabled: false });
  assert.equal(
    (await target.ledger()).bindings['1'].receiveMode,
    'receive_none',
  );
  await writeFile(
    join(artifacts, 'plugin-send.json'),
    JSON.stringify(
      {
        callId,
        message: result.message_id,
        recipient: received.recipientId,
        producerEpoch: saved.durable.producerEpoch,
        durableBeforeHTTPS: observed.durable,
        binding: binding.bindingId,
        serverStateVersion: delivery.recipients[0].state_version,
        receiptVersion: request.results[0].expected_state_version,
        committedCursor: committed.committed_cursor,
        nativeBodyPersisted: true,
        replayIdentical: true,
      },
      null,
      2,
    ),
  );
  checks.push(
    'SEND-3: real ToolRuntime production send, durable original request before device HTTPS, PG single message and same-key original result',
  );
  checks.push(
    'SEND-3: explicit session receive control, WSS FETCH/attempt/admit, real native HostPort/provider body, server-version RECEIPT_COMMITTED',
  );
}
