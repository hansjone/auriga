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

/** @param {unknown} value @param {boolean} [positive] */
function uint64(value, positive = false) {
  return (
    typeof value === 'string' &&
    /^(0|[1-9][0-9]{0,19})$/.test(value) &&
    BigInt(value) <= 18446744073709551615n &&
    (!positive || value !== '0')
  );
}
/** @param {unknown} value */
function id(value) {
  return (
    typeof value === 'string' &&
    /^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$/.test(value)
  );
}

/** Untrusted parameters only; never identity authority before full verification.
 * @param {any} result */
export function readyCandidate(result) {
  if (!id(result?.node_identity?.incarnation) || !uint64(result?.epoch, true))
    throw new SessionError('network.protocol_error', 'invalid READY candidate');
  return {
    nodeIncarnation: result.node_identity.incarnation,
    epoch: result.epoch,
  };
}

/** Call only after authenticating the complete original READY_RESULT object.
 * Unknown optional fields remain covered by its signature.
 * @param {any} result
 * @param {{realm:string,node:string,nodeIncarnation:string,epoch:string,requestId:string,allowRecoveryConflict?:boolean}} expected */
export function assertReadyResult(result, expected) {
  readyCandidate(result);
  const identity = result.node_identity;
  const audience = result.server_auth?.audience;
  const checkpoint = result.seen_checkpoint;
  if (
    typeof result.app_version !== 'string' ||
    !/^1\.(?:0|[1-9][0-9]{0,3})$/.test(result.app_version) ||
    result.type !== 'ready_result' ||
    result.request_id !== expected.requestId ||
    typeof result.status !== 'string' ||
    identity.realm !== expected.realm ||
    identity.node !== expected.node ||
    identity.incarnation !== expected.nodeIncarnation ||
    audience?.realm !== identity.realm ||
    audience?.node !== identity.node ||
    audience?.incarnation !== identity.incarnation ||
    audience?.epoch !== result.epoch ||
    result.epoch !== expected.epoch ||
    (result.producer_epoch !== undefined &&
      !uint64(result.producer_epoch, true)) ||
    ![
      'head_seq',
      'committed_cursor',
      'retention_low_seq',
      'local_high_water',
      'registration_revision',
    ].every((field) => uint64(result[field])) ||
    !(
      checkpoint === null ||
      (checkpoint &&
        uint64(checkpoint.checkpoint_seq) &&
        uint64(checkpoint.checkpoint_revision) &&
        typeof checkpoint.summary_digest === 'string' &&
        /^sha256:[0-9a-f]{64}$/.test(checkpoint.summary_digest))
    ) ||
    !Array.isArray(result.required_features) ||
    result.required_features.length > 64 ||
    !result.required_features.every(
      (/** @type {unknown} */ feature) =>
        typeof feature === 'string' && /^[a-z][a-z0-9_]{0,47}$/.test(feature),
    ) ||
    new Set(result.required_features).size !== result.required_features.length
  )
    throw new SessionError('network.protocol_error', 'invalid READY_RESULT');
  if (
    result.status !== 'ready' &&
    !(expected.allowRecoveryConflict && result.status === 'recovery_conflict')
  )
    throw new SessionError('ready_failed', 'READY_RESULT not ready');
  const supported = [
    'server_auth_v1',
    'source_attestation_v1',
    'inbox_only_v1',
    'broadcast_grants_v1',
  ];
  if (
    result.required_features.some(
      (/** @type {unknown} */ feature) =>
        !supported.includes(/** @type {string} */ (feature)),
    )
  )
    throw new SessionError(
      'network.unsupported_app_version',
      'unsupported required network feature',
    );
}
/** @param {any} entry */
function assertPendingRegistration(entry) {
  if (
    !entry ||
    !uint64(entry.candidate_local, true) ||
    !id(entry.allocation_op_id) ||
    typeof entry.allocation_fingerprint !== 'string' ||
    !/^sha256:[0-9a-f]{64}$/.test(entry.allocation_fingerprint) ||
    !['session', 'subagent'].includes(entry.address_type)
  )
    throw new SessionError(
      'network.protocol_error',
      'invalid typed local registration',
    );
}

/** Pure formal LOCAL_REGISTER construction; the production provider owns sending.
 * @param {{requestId:string,epoch:string,bootId:string,candidate_local:string,allocation_op_id:string,allocation_fingerprint:string,address_type:'session'|'subagent',expected_registration_revision:string}} input */
export function encodeLocalRegister(input) {
  assertPendingRegistration(input);
  if (
    !id(input.requestId) ||
    !id(input.bootId) ||
    !uint64(input.epoch, true) ||
    !uint64(input.expected_registration_revision)
  )
    throw new SessionError(
      'network.protocol_error',
      'invalid registration context',
    );
  return encodeJson({
    app_version: APP_VERSION,
    type: 'local_register',
    request_id: input.requestId,
    epoch: input.epoch,
    boot_id: input.bootId,
    candidate_local: input.candidate_local,
    allocation_op_id: input.allocation_op_id,
    allocation_fingerprint: input.allocation_fingerprint,
    address_type: input.address_type,
    expected_registration_revision: input.expected_registration_revision,
  });
}

/** Call only after server authentication. RegisterResult has no fingerprint echo:
 * the request's original fingerprint stays bound by its correlated request_id.
 * Consumers accept compatible minor versions and ignore unknown optional fields;
 * the complete original object, including those fields, must already be verified.
 * @param {any} result @param {any} input */
export function assertRegisterResult(result, input) {
  assertPendingRegistration(input);
  if (!uint64(input.expected_registration_revision))
    throw new SessionError(
      'network.protocol_error',
      'invalid original registration revision',
    );
  if (
    typeof result?.app_version !== 'string' ||
    !/^1\.(?:0|[1-9][0-9]{0,3})$/.test(result.app_version) ||
    result.type !== 'register_result' ||
    result.request_id !== input.request_id ||
    result.local !== input.candidate_local ||
    result.allocation_op_id !== input.allocation_op_id ||
    result.address_type !== input.address_type ||
    !id(result.registration_id) ||
    !['registered', 'existing'].includes(result.result) ||
    !uint64(result.registration_revision, true) ||
    BigInt(result.registration_revision) !==
      BigInt(input.expected_registration_revision) + 1n ||
    !uint64(result.local_high_water, true) ||
    BigInt(result.local_high_water) < BigInt(result.local)
  )
    throw new SessionError(
      'network.protocol_error',
      'registration response mismatch',
    );
}

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
 *   receiptCheckpoint: {checkpoint_seq:string,checkpoint_revision:string,summary_digest:string},
 *   pendingRegistration?: {candidate_local:string,allocation_op_id:string,allocation_fingerprint:string,address_type:'session'|'subagent'}|null,
 *   adapterCapabilities?: string[],
 *   bootId?: string,
 *   keysetVersion?: string,
 * }} input
 */
export function encodeReady(input) {
  if (
    input.pendingRegistration !== undefined &&
    input.pendingRegistration !== null
  )
    assertPendingRegistration(input.pendingRegistration);
  return encodeJson({
    app_version: APP_VERSION,
    type: 'ready',
    request_id: input.requestId,
    channel_nonce: input.channelNonce,
    boot_id: input.bootId ?? 'boot-1',
    keyset_version: input.keysetVersion ?? '1',
    pending_registration: input.pendingRegistration ?? null,
    features: ['server_auth_v1', 'source_attestation_v1', 'inbox_only_v1'],
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
 *     target: import('../host/address.js').Address,
 *     evidenceDigest?: string,
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
      /** @type {Record<string, any>} */
      const row = {
        recipient_id: r.recipientId,
        status: r.status === 'retry' ? 'retryable' : r.status,
        expected_state_version: r.expectedStateVersion,
        message_digest: r.messageDigest,
        host_request_id: r.hostRequestId,
        target: r.target,
      };
      if (r.status === 'rejected') row.code = 'network.target_unavailable';
      if (r.acceptedBy) row.accepted_by = r.acceptedBy;
      if (r.acceptanceMode) row.acceptance_mode = r.acceptanceMode;
      if (r.evidenceDigest) row.evidence_digest = r.evidenceDigest;
      if (r.acceptanceMode === 'inbox_only')
        row.execution_status = 'not_applicable';
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
  const e =
    /** @type {{ epoch?: string, page?: { epoch?: string }, server_auth?: {audience?: {epoch?:string}} }} */ (
      frame
    );
  const found = e.epoch ?? e.page?.epoch ?? e.server_auth?.audience?.epoch;
  if (found === undefined) {
    throw new SessionError('missing_epoch', 'frame missing epoch');
  }
  if (String(found) !== String(epoch)) {
    throw new SessionError('stale_epoch', `frame epoch ${found} != ${epoch}`);
  }
}
