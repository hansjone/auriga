import { SessionError } from './errors.js';
import { digest, verifyEnvelopeWithRefresh } from '../trust/verifier.js';
import { canonicalize } from '../trust/jcs.js';
import { Buffer } from 'node:buffer';
import { FETCH_MAX_BYTES } from './constants.js';

/**
 * Map a wire FETCH_RESULT delivery item into the host adapter PageItem shape.
 * Call only after verifying the original delivery object.
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
  /** @type {any[]} */
  const rawRecipients = item.recipients ?? [];
  const recipients = rawRecipients
    .filter(
      /** @param {any} r */ (r) => r && r.local !== undefined && r.recipient_id,
    )
    .map(
      /** @param {any} r */ (r) => ({
        recipientId: r.recipient_id,
        local: String(r.local),
        type: r.type ?? (String(r.local) === '0' ? 'system' : 'session'),
        attemptId: r.attempt_id ?? r.attemptId,
      }),
    );
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
 *   header: any,
 *   trust: Awaited<ReturnType<import('../trust/store.js').openTrustStore>>,
 *   context: import('../trust/verifier.js').VerifyContext,
 *   now?: () => number,
 *   maxBytes?: number,
 *   prepareDelivery?: (raw:any) => Promise<any>,
 * }} input
 * @returns {Promise<{verified:true,items:any[],rejections:{index:number,code:string,reason:string}[]}>}
 */
export async function verifyFetchPage(input) {
  const { trust, context, now } = input;
  const encoded = canonicalize({
    app_version: input.header?.app_version,
    type: 'fetch_result',
    page: input.header,
    items: input.items,
  });
  if (Buffer.byteLength(encoded) > (input.maxBytes ?? FETCH_MAX_BYTES))
    throw new SessionError(
      'network.protocol_error',
      'FETCH_RESULT exceeds requested max_bytes',
    );
  const verified = await verifyEnvelopeWithRefresh(input.header, {
    trust,
    context,
    now,
  });
  const manifest = input.header.manifest;
  if (
    input.header.type !== 'fetch_page' ||
    !Array.isArray(input.items) ||
    !Array.isArray(manifest) ||
    manifest.length !== input.items.length ||
    manifest.some(
      (m, i) =>
        m.item_type !== input.items[i]?.type ||
        m.mail_seq !== (input.items[i]?.mail_seq ?? null) ||
        m.digest !== digest(input.items[i]),
    )
  ) {
    throw new SessionError(
      'network.signature_invalid',
      'page manifest mismatch',
    );
  }
  const items = [];
  const rejections = [];
  for (const [index, raw] of input.items.entries()) {
    try {
      await verifyEnvelopeWithRefresh(raw, { trust, context, now });
      items.push(raw);
      // Verified terminal / hole items have no host delivery in this seam.
    } catch (error) {
      if (!(error instanceof SessionError)) throw error;
      // Never record untrusted sender/recipient IDs as authoritative audit data.
      rejections.push({ index, code: error.code, reason: error.message });
    }
  }
  return { verified, items, rejections };
}

/** Keep signature acceptance separate from the online attempt required by the host.
 * @param {Parameters<typeof verifyFetchPage>[0]} input
 * @returns {Promise<import('../host/adapter.js').Page & {verifiedItems:any[],rejections:{index:number,code:string,reason:string}[]}>}
 */
export async function buildVerifiedPage(input) {
  const checked = await verifyFetchPage(input);
  const items = [];
  for (const raw of checked.items) {
    if (raw.type !== 'delivery') continue;
    try {
      items.push(
        wireDeliveryToPageItem(
          input.prepareDelivery ? await input.prepareDelivery(raw) : raw,
        ),
      );
    } catch (error) {
      if (!(error instanceof SessionError)) throw error;
      checked.rejections.push({
        index: input.items.indexOf(raw),
        code: error.code,
        reason: error.message,
      });
    }
  }
  return {
    // Only verified members are retained; the page flag attests the header and
    // full manifest, including when no member can enter the host path.
    verified: checked.verified,
    tenant: input.tenant,
    nodeIncarnation: input.nodeIncarnation,
    epoch: input.epoch,
    bootId: input.bootId,
    items,
    rejections: checked.rejections,
    verifiedItems: checked.items,
  };
}
