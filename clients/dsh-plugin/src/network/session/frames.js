import { parseStrictJson } from '../trust/jcs.js';
import {
  APP_VERSION,
  FETCH_MAX_BYTES,
  FETCH_MAX_ITEMS,
  FETCH_MIN_BYTES,
  MAX_PAYLOAD_BYTES,
} from './constants.js';
import { SessionError } from './errors.js';

const enc = new TextEncoder();

/** @param {unknown} value */
export function encodeJson(value) {
  const bytes = enc.encode(JSON.stringify(value));
  if (bytes.byteLength > MAX_PAYLOAD_BYTES) {
    throw new SessionError('payload_too_large', 'frame exceeds 64KiB');
  }
  return bytes;
}

/** @param {Uint8Array} bytes */
export function decodeJson(bytes) {
  if (
    !bytes ||
    bytes.byteLength === 0 ||
    bytes.byteLength > MAX_PAYLOAD_BYTES
  ) {
    throw new SessionError('payload_size', 'invalid payload size');
  }
  try {
    return parseStrictJson(bytes);
  } catch (cause) {
    throw new SessionError('not_json', 'payload is not JSON', { cause });
  }
}

/**
 * @param {{
 *   requestId: string,
 *   channelNonce: string,
 *   resumeCursor: string,
 *   localHighWater: string,
 *   registrationRevision: string,
 *   receiptCheckpoint: string,
 *   adapterCapabilities?: string[],
 * }} input
 */
export function encodeReady(input) {
  return encodeJson({
    app_version: APP_VERSION,
    type: 'ready',
    request_id: input.requestId,
    channel_nonce: input.channelNonce,
    resume_cursor: input.resumeCursor,
    local_high_water: input.localHighWater,
    registration_revision: input.registrationRevision,
    receipt_checkpoint: input.receiptCheckpoint,
    adapter_capabilities: input.adapterCapabilities ?? [
      'server_auth_v1',
      'source_attestation_v1',
      'inbox_only_v1',
    ],
  });
}

/**
 * @param {{
 *   requestId: string,
 *   epoch: string,
 *   resumeCursor: string,
 *   maxItems?: number,
 *   maxBytes?: number,
 *   heldIds?: string[],
 *   continuation?: unknown,
 * }} input
 */
export function encodeFetch(input) {
  const maxItems = input.maxItems ?? 16;
  const maxBytes = input.maxBytes ?? FETCH_MAX_BYTES;
  if (maxItems < 1 || maxItems > FETCH_MAX_ITEMS) {
    throw new SessionError('bad_fetch', 'max_items out of range');
  }
  if (maxBytes < FETCH_MIN_BYTES || maxBytes > FETCH_MAX_BYTES) {
    throw new SessionError('bad_fetch', 'max_bytes out of range');
  }
  return encodeJson({
    app_version: APP_VERSION,
    type: 'fetch',
    request_id: input.requestId,
    epoch: input.epoch,
    resume_cursor: input.resumeCursor,
    continuation: input.continuation ?? {},
    max_items: maxItems,
    max_bytes: maxBytes,
    held_ids: input.heldIds ?? [],
    paused: [],
    state_barrier: [],
  });
}

/**
 * @param {{
 *   requestId: string,
 *   operationId: string,
 *   epoch: string,
 *   deliveryId: string,
 *   mailSeq: string,
 *   results: Array<{
 *     recipientId: string,
 *     status: string,
 *     code?: string,
 *     expectedStateVersion: string,
 *     acceptedBy?: string,
 *     acceptanceMode?: string,
 *     messageDigest: string,
 *     hostRequestId: string,
 *   }>,
 * }} input
 */
export function encodeReceipt(input) {
  return encodeJson({
    app_version: APP_VERSION,
    type: 'receipt',
    request_id: input.requestId,
    operation_id: input.operationId,
    epoch: input.epoch,
    delivery_id: input.deliveryId,
    mail_seq: input.mailSeq,
    results: input.results.map((r) => {
      /** @type {Record<string, string>} */
      const row = {
        recipient_id: r.recipientId,
        status: r.status === 'retry' ? 'retryable' : r.status,
        expected_state_version: r.expectedStateVersion,
        message_digest: r.messageDigest,
        host_request_id: r.hostRequestId,
      };
      if (r.code) row.code = r.code;
      if (r.acceptedBy) row.accepted_by = r.acceptedBy;
      if (r.acceptanceMode) row.acceptance_mode = r.acceptanceMode;
      return row;
    }),
  });
}

/** @param {unknown} frame */
export function frameType(frame) {
  return /** @type {{ type?: string }} */ (frame)?.type ?? '';
}

/** @param {unknown} frame @param {string} epoch */
export function assertFrameEpoch(frame, epoch) {
  const e = /** @type {{ epoch?: string, page?: { epoch?: string } }} */ (
    frame
  );
  const found = e.epoch ?? e.page?.epoch;
  if (found === undefined) {
    throw new SessionError('missing_epoch', 'frame missing epoch');
  }
  if (String(found) !== String(epoch)) {
    throw new SessionError('stale_epoch', `frame epoch ${found} != ${epoch}`);
  }
}
