import { SessionError } from './errors.js';

/**
 * Map a wire FETCH_RESULT delivery item into the host adapter PageItem shape.
 * N1 does not verify server_auth signatures; callers mark `verified: true` only
 * after their own trust decision (test stub / future verifier).
 *
 * @param {any} item wire delivery
 * @returns {import('../host/adapter.js').PageItem}
 */
export function wireDeliveryToPageItem(item) {
  if (!item || item.type !== 'delivery') {
    throw new SessionError('bad_item', 'expected delivery item');
  }
  const m = item.message;
  const src = item.source_context;
  if (!m || !src?.message_digest) {
    throw new SessionError('bad_item', 'delivery missing message/digest');
  }
  const recipients = (item.recipients ?? [])
    .filter((r) => r && r.local !== undefined && r.recipient_id)
    .map((r) => ({
      recipientId: r.recipient_id,
      local: String(r.local),
      type: r.type ?? (String(r.local) === '0' ? 'system' : 'session'),
      attemptId: r.attempt_id ?? r.attemptId,
    }));
  for (const r of recipients) {
    if (!r.attemptId) {
      throw new SessionError(
        'missing_attempt',
        `recipient ${r.recipientId} has no attempt_id`,
      );
    }
  }
  return {
    deliveryId: item.delivery_id,
    mailSeq: String(item.mail_seq),
    messageDigest: src.message_digest,
    message: {
      messageId: m.message_id,
      from: m.from,
      to: m.to,
      replyTo: m.reply_to ?? m.replyTo,
      threadId: m.thread_id,
      threadSequence: String(m.thread_sequence ?? '0'),
      inReplyTo: m.in_reply_to ?? null,
      kind: m.kind,
      intent: m.intent,
      messageClass: m.message_class,
      contactScope: m.contact_scope,
      senderKind: m.sender_kind,
      body: m.body,
    },
    recipients,
  };
}

/**
 * @param {{
 *   tenant: string,
 *   nodeIncarnation: string,
 *   epoch: string,
 *   bootId: string,
 *   items: any[],
 * }} input
 * @returns {import('../host/adapter.js').Page}
 */
export function buildVerifiedPage(input) {
  const items = [];
  for (const raw of input.items ?? []) {
    if (raw?.type === 'delivery') items.push(wireDeliveryToPageItem(raw));
    // terminal / hole items are skipped at the host seam for N1.
  }
  return {
    verified: true,
    tenant: input.tenant,
    nodeIncarnation: input.nodeIncarnation,
    epoch: input.epoch,
    bootId: input.bootId,
    items,
  };
}
