/* global structuredClone */
import { randomBytes } from 'node:crypto';
import {
  assertFrameEpoch,
  assertRegisterResult,
  decodeJson,
  encodeFetch,
  encodeReady,
  encodeReceipt,
  encodeJson,
  encodeLocalRegister,
  frameType,
} from './frames.js';
import { SessionError } from './errors.js';
import { buildVerifiedPage } from './page.js';
import { createSystemInbox, systemPromptFrom } from './system-inbox.js';
import { verifyEnvelopeWithRefresh } from '../trust/verifier.js';
import { FETCH_MAX_BYTES } from './constants.js';
import { networkTiming } from '../timing.js';

const FINAL = new Set([
  'host_accepted',
  'rejected',
  'quarantined',
  'outcome_unknown',
]);

/**
 * Wires host adapter ↔ byte transport for READY / HEAD / FETCH / RECEIPT.
 *
 * @typedef {import('./memory-pipe.js').TransportLike} TransportLike
 * @typedef {ReturnType<import('../host/adapter.js').createHostAdapter>} HostAdapter
 *
 * @param {{
 *   transport: TransportLike,
 *   adapter: HostAdapter,
 *   identity: { tenant: string, nodeIncarnation: string, realm: string, node: string },
 *   bootId?: string,
 *   systemInbox?: ReturnType<typeof createSystemInbox>,
 *   nowIso?: () => string,
 *   newId?: (prefix: string) => string,
 *   trust: Awaited<ReturnType<import('../trust/store.js').openTrustStore>>,
 *   now?: () => number,
 *   audit?: (event: {code: string, reason: string, index?: number}) => void,
 *   onVerifiedItems?: (items: any[]) => Promise<void>,
 *   submit?: (request: import('../../generated/network/SubmitRequest.js').SubmitRequest) => Promise<unknown>,
 *   prepareDelivery?: (raw:any) => Promise<any>,
 * }} options
 */
export function createNetworkSession(options) {
  const transport = options.transport;
  const adapter = options.adapter;
  const identity = options.identity;
  const bootId = options.bootId ?? 'boot-1';
  const now = options.now ?? Date.now;
  let channelNonce = '';
  /** @type {{code: string, reason: string, index?: number}[]} */
  const diagnostics = [];
  let auditWindow = 0;
  let auditCount = 0;
  /** Connection-level bounded diagnostics, never sender/recipient penalties.
   * @param {{code: string, reason: string, index?: number}} event */
  function audit(event) {
    if (now() - auditWindow >= 60000) {
      auditWindow = now();
      auditCount = 0;
    }
    if (++auditCount > 32) return;
    diagnostics.push(event);
    if (diagnostics.length > 64) diagnostics.shift();
    try {
      options.audit?.({ ...event });
    } catch {
      /* Diagnostics cannot open the delivery gate. */
    }
  }
  /** @param {string|null} requestId @param {string} [expectedEpoch] */
  function context(requestId, expectedEpoch = epoch) {
    return { ...identity, epoch: expectedEpoch, requestId, channelNonce };
  }
  const systemInbox = options.systemInbox ?? createSystemInbox();
  const nowIso = options.nowIso ?? (() => new Date().toISOString());
  const newId =
    options.newId ??
    ((prefix) => `${prefix}-${randomBytes(6).toString('hex')}`);

  /** @type {string} */
  let epoch = '0';
  /** Platform-confirmed prefix only (receipt / terminal commits). */
  let resumeCursor = '0';
  /** Pagination scan tip; never written into resume_cursor. */
  let scanAfter = '0';
  let ready = false;
  let generation = 0;
  let awaitingKeys = false;
  let rescan = false;
  let seenRefresh = options.trust.refreshRevision;
  /** @type {(() => void)|undefined} */
  let unrefresh;
  function refreshed() {
    if (options.trust.refreshRevision <= seenRefresh) return;
    seenRefresh = options.trust.refreshRevision;
    if (!awaitingKeys) return;
    awaitingKeys = false;
    rescan = true;
    if (ready && !fetchInflight) void pull().catch(() => {});
  }
  let fetchInflight = false;
  let nudged = false;
  /** @type {(() => void) | null} */
  let unsub = null;
  /** @type {Map<string, { resolve: (v: any) => void, reject: (e: Error) => void }>} */
  const pending = new Map();
  /** @type {WeakMap<object,number>} */
  const receivedSizes = new WeakMap();

  function requireReady() {
    if (!ready) throw new SessionError('not_ready', 'session not ready');
  }

  /**
   * @param {string} requestId
   * @param {number} [timeoutMs]
   */
  function waitFor(requestId, timeoutMs = networkTiming.operationMs) {
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => {
        pending.delete(requestId);
        reject(new SessionError('timeout', `wait ${requestId}`));
      }, timeoutMs);
      timer.unref?.();
      pending.set(requestId, {
        resolve: (v) => {
          clearTimeout(timer);
          resolve(v);
        },
        reject: (e) => {
          clearTimeout(timer);
          reject(e);
        },
      });
    });
  }

  /** @param {string} requestId @param {unknown} frame */
  function settle(requestId, frame) {
    const p = pending.get(requestId);
    if (!p) return;
    pending.delete(requestId);
    p.resolve(frame);
  }
  /** Await both paths so send failure/stop cannot leave an unhandled waiter.
   * @param {Uint8Array} payload @param {string} requestId */
  async function exchange(payload, requestId) {
    const wait = waitFor(requestId);
    try {
      const [result] = await Promise.all([wait, transport.send(payload)]);
      return result;
    } catch (error) {
      const p = pending.get(requestId);
      pending.delete(requestId);
      p?.reject(new SessionError('stopped', 'request interrupted'));
      throw error;
    }
  }

  /** @param {Uint8Array} payload */
  async function onPayload(payload) {
    let frame;
    try {
      frame = decodeJson(payload);
    } catch {
      audit({ code: 'network.protocol_error', reason: 'invalid wire JSON' });
      return;
    }
    const type = frameType(frame);
    try {
      if (type !== 'error' && type !== 'ready_result') {
        assertFrameEpoch(frame, epoch);
      }
    } catch (e) {
      if (
        e instanceof SessionError &&
        (e.code === 'stale_epoch' || e.code === 'missing_epoch')
      ) {
        // Drop bad-epoch frames; do not deliver into the ledger.
        return;
      }
      throw e;
    }

    if (type === 'ready_result') {
      if (!pending.has(frame.request_id)) return;
      try {
        await verifyEnvelopeWithRefresh(frame, {
          trust: options.trust,
          context: context(frame.request_id, String(frame.epoch)),
          now,
        });
      } catch (error) {
        rejectFrame(frame.request_id, error);
        return;
      }
      settle(frame.request_id, frame);
      return;
    }
    if (type === 'head') {
      try {
        await verifyEnvelopeWithRefresh(frame, {
          trust: options.trust,
          context: context(null),
          now,
        });
      } catch (error) {
        rejectFrame(null, error);
        return;
      }
      // Tip nudge: schedule a fetch when idle.
      if (fetchInflight) {
        nudged = true;
        return;
      }
      void pull().catch(() => {});
      return;
    }
    if (type === 'fetch_result') {
      receivedSizes.set(frame, payload.byteLength);
      settle(frame.page?.request_id, frame);
      return;
    }
    if (
      [
        'attempt_result',
        'admit_result',
        'register_result',
        'assign_result',
        'decide_result',
        'status',
      ].includes(type)
    ) {
      if (!pending.has(frame.request_id)) return;
      try {
        await verifyEnvelopeWithRefresh(frame, {
          trust: options.trust,
          context: context(frame.request_id),
          now,
        });
      } catch (error) {
        rejectFrame(frame.request_id, error);
        return;
      }
      settle(frame.request_id, frame);
      return;
    }
    if (type === 'receipt_committed') {
      if (!pending.has(frame.request_id)) return;
      try {
        await verifyEnvelopeWithRefresh(frame, {
          trust: options.trust,
          context: context(frame.request_id),
          now,
        });
      } catch (error) {
        rejectFrame(frame.request_id, error);
        return;
      }
      settle(frame.request_id, frame);
      return;
    }
    if (type === 'error') {
      const id = frame.request_id;
      if (id && pending.has(id)) {
        try {
          await verifyEnvelopeWithRefresh(frame, {
            trust: options.trust,
            context: context(id),
            now,
          });
        } catch (error) {
          rejectFrame(id, error);
          return;
        }
        const p = pending.get(id);
        pending.delete(id);
        p?.reject(
          new SessionError(frame.code ?? 'remote_error', 'remote ERROR frame'),
        );
      }
    }
  }

  /** @param {string|null} id @param {unknown} error */
  function rejectFrame(id, error) {
    const failure =
      error instanceof SessionError
        ? error
        : new SessionError('network.signature_invalid', 'verification failed');
    audit({ code: failure.code, reason: failure.message });
    if (id) {
      const waiter = pending.get(id);
      pending.delete(id);
      waiter?.reject(failure);
    }
  }

  /**
   * READY handshake. Returns READY_RESULT (epoch / cursors).
   * @param {{
   *   resumeCursor?: string,
   *   localHighWater?: string,
   *   registrationRevision?: string,
   *   receiptCheckpoint?: {checkpoint_seq:string,checkpoint_revision:string,summary_digest:string},
   *   pendingRegistration?: {candidate_local:string,allocation_op_id:string,allocation_fingerprint:string,address_type:'session'|'subagent'}|null,
   * }} [hint]
   */
  async function start(hint = {}) {
    if (ready) throw new SessionError('invalid_state', 'already started');
    const started = ++generation;
    unsub?.();
    unrefresh?.();
    unrefresh = options.trust.onRefresh(refreshed);
    unsub = transport.onMessage((payload) => {
      void onPayload(payload).catch((error) => rejectFrame(null, error));
    });
    const requestId = newId('req-ready');
    channelNonce = newId('nonce');
    const result = await exchange(
      encodeReady({
        requestId,
        channelNonce,
        resumeCursor: hint.resumeCursor ?? resumeCursor,
        localHighWater: hint.localHighWater ?? '0',
        registrationRevision: hint.registrationRevision ?? '0',
        receiptCheckpoint: hint.receiptCheckpoint ?? {
          checkpoint_seq: '0',
          checkpoint_revision: '0',
          summary_digest: 'sha256:' + '0'.repeat(64),
        },
        pendingRegistration: hint.pendingRegistration,
        bootId,
        keysetVersion: options.trust.version,
      }),
      requestId,
    );
    if (started !== generation)
      throw new SessionError('stopped', 'handshake superseded');
    if (
      result.status !== 'ready' ||
      result.node_identity?.realm !== identity.realm ||
      result.node_identity?.node !== identity.node ||
      result.node_identity?.incarnation !== identity.nodeIncarnation
    )
      throw new SessionError(
        'ready_failed',
        'READY_RESULT not ready or identity mismatch',
      );
    const supported = [
      'server_auth_v1',
      'source_attestation_v1',
      'inbox_only_v1',
      'broadcast_grants_v1',
    ];
    if (
      !Array.isArray(result.required_features) ||
      result.required_features.some(
        (/** @type {string} */ feature) => !supported.includes(feature),
      )
    )
      throw new SessionError(
        'network.unsupported_app_version',
        'unsupported required network feature',
      );
    epoch = String(result.epoch);
    if (result.committed_cursor !== undefined) {
      resumeCursor = String(result.committed_cursor);
    }
    if (result.scan_after !== undefined) {
      scanAfter = String(result.scan_after);
    }
    ready = true;
    return result;
  }

  function disconnect() {
    generation++;
    ready = false;
    unsub?.();
    unsub = null;
    unrefresh?.();
    unrefresh = undefined;
    scanAfter = resumeCursor;
    for (const [, p] of pending) {
      p.reject(new SessionError('stopped', 'session stopped'));
    }
    pending.clear();
  }
  async function stop() {
    disconnect();
    await transport.stop?.();
  }

  /**
   * One FETCH → host.deliver → RECEIPT cycle.
   * @param {{ maxItems?: number, maxBytes?: number }} [opts]
   */
  async function pull(opts = {}) {
    requireReady();
    if (fetchInflight) {
      throw new SessionError('fetch_inflight', 'single-flight FETCH');
    }
    fetchInflight = true;
    try {
      if (rescan) {
        scanAfter = resumeCursor;
        rescan = false;
      }
      const requestId = newId('req-fetch');
      const result = await exchange(
        encodeFetch({
          requestId,
          epoch,
          resumeCursor,
          continuation: { scan_after: scanAfter },
          maxItems: opts.maxItems,
          maxBytes: opts.maxBytes,
        }),
        requestId,
      );
      return await applyFetchResult(result, requestId, opts.maxBytes);
    } finally {
      fetchInflight = false;
      if (ready && (rescan || nudged)) {
        nudged = false;
        void pull().catch(() => {});
      }
    }
  }

  /** @param {any} result @param {string} requestId @param {number} [maxBytes] */
  async function applyFetchResult(result, requestId, maxBytes) {
    requireReady();
    if (frameType(result) !== 'fetch_result') {
      throw new SessionError('bad_fetch_result', 'expected fetch_result');
    }
    assertFrameEpoch(result, epoch);
    const wireBytes = receivedSizes.get(result);
    if (wireBytes !== undefined && wireBytes > (maxBytes ?? FETCH_MAX_BYTES)) {
      const error = new SessionError(
        'network.protocol_error',
        'FETCH_RESULT exceeds requested wire budget',
      );
      rejectFrame(null, error);
      throw error;
    }
    const pageEpoch = String(result.page?.epoch ?? result.epoch);
    const expectedContext = context(requestId);
    let page;
    const refreshBefore = options.trust.refreshRevision;
    try {
      page = await buildVerifiedPage({
        tenant: identity.tenant,
        nodeIncarnation: identity.nodeIncarnation,
        epoch: pageEpoch,
        bootId,
        items: result.items ?? [],
        header: result.page,
        trust: options.trust,
        context: expectedContext,
        now,
        maxBytes,
        prepareDelivery: options.prepareDelivery,
      });
    } catch (error) {
      rejectFrame(null, error);
      throw error;
    }
    // A key refresh may outlive stop/reconnect; do not apply an old connection's page.
    requireReady();
    if (
      channelNonce !== expectedContext.channelNonce ||
      epoch !== expectedContext.epoch
    )
      throw new SessionError(
        'stale_epoch',
        'connection changed during verification',
      );
    for (const failure of page.rejections) audit(failure);
    if (
      page.rejections.some(
        (failure) => failure.code === 'network.key_unavailable',
      )
    ) {
      awaitingKeys = true;
      if (options.trust.refreshRevision > refreshBefore) {
        awaitingKeys = false;
        rescan = true;
      }
    }
    await options.onVerifiedItems?.(page.verifiedItems);
    requireReady();
    if (
      channelNonce !== expectedContext.channelNonce ||
      epoch !== expectedContext.epoch
    )
      throw new SessionError('stale_epoch', 'connection changed during intake');
    // scan_after advances pagination; resume_cursor only tracks confirmed prefix.
    const previousScan = scanAfter;
    if (result.page?.scan_after !== undefined) {
      scanAfter = String(result.page.scan_after);
    }
    if (result.page?.committed_cursor !== undefined) {
      resumeCursor = String(result.page.committed_cursor);
    }
    if (
      result.page?.snapshot_head !== undefined &&
      BigInt(scanAfter) > BigInt(previousScan) &&
      BigInt(scanAfter) < BigInt(result.page.snapshot_head)
    )
      nudged = true;
    if (page.items.length === 0) {
      return { receipts: [], committed: [] };
    }

    const verifiedPage = /** @type {import('../host/adapter.js').Page} */ (
      page
    );
    const receipts = await adapter.deliver(verifiedPage);
    requireReady();
    if (
      channelNonce !== expectedContext.channelNonce ||
      epoch !== expectedContext.epoch
    )
      throw new SessionError(
        'stale_epoch',
        'connection changed during host delivery',
      );
    await publishSystemPrompts(verifiedPage, receipts);
    const committed = await submitReceipts(verifiedPage, receipts);
    return { receipts, committed, page };
  }

  /**
   * System local intakes go to the approval channel only.
   * @param {import('../host/adapter.js').Page} page
   * @param {import('../host/adapter.js').Receipt[]} receipts
   */
  async function publishSystemPrompts(page, receipts) {
    const byId = new Map();
    for (const item of page.items) {
      for (const r of item.recipients) byId.set(r.recipientId, item);
    }
    for (const receipt of receipts) {
      if (
        receipt.status !== 'host_accepted' ||
        receipt.acceptedBy !== 'system' ||
        receipt.acceptanceMode !== 'inbox_only'
      ) {
        continue;
      }
      const item = byId.get(receipt.recipientId);
      if (!item) continue;
      systemInbox.offer(systemPromptFrom(item, receipt, nowIso()));
    }
  }

  /**
   * @param {import('../host/adapter.js').Page} page
   * @param {import('../host/adapter.js').Receipt[]} receipts
   */
  async function submitReceipts(page, receipts) {
    /** @type {Map<string, { mailSeq: string, results: any[] }>} */
    const byDelivery = new Map();
    const itemByRecipient = new Map();
    for (const item of page.items) {
      for (const r of item.recipients) {
        itemByRecipient.set(r.recipientId, item);
      }
    }
    for (const receipt of receipts) {
      if (!FINAL.has(receipt.status)) continue;
      const item = itemByRecipient.get(receipt.recipientId);
      if (!item) continue;
      let group = byDelivery.get(item.deliveryId);
      if (!group) {
        group = { mailSeq: item.mailSeq, results: [] };
        byDelivery.set(item.deliveryId, group);
      }
      group.results.push({
        recipientId: receipt.recipientId,
        status: receipt.status,
        code: receipt.code,
        expectedStateVersion: String(receipt.ledgerRevision ?? 0),
        acceptedBy: receipt.acceptedBy,
        acceptanceMode:
          receipt.acceptanceMode === 'prompt'
            ? 'normal'
            : receipt.acceptanceMode,
        messageDigest: receipt.messageDigest,
        hostRequestId: receipt.hostRequestId,
        target: receipt.targetAddress,
        evidenceDigest: receipt.evidenceDigest,
      });
    }

    /** @type {string[]} */
    const committed = [];
    for (const [deliveryId, group] of byDelivery) {
      if (group.results.length === 0) continue;
      const requestId = newId('req-rcpt');
      const operationId = newId('op-rcpt');
      const committedFrame = await exchange(
        encodeReceipt({
          requestId,
          operationId,
          epoch,
          deliveryId,
          mailSeq: group.mailSeq,
          results: group.results,
        }),
        requestId,
      );
      if (frameType(committedFrame) !== 'receipt_committed') {
        throw new SessionError('bad_receipt', 'expected receipt_committed');
      }
      if (committedFrame.committed_cursor !== undefined) {
        resumeCursor = String(committedFrame.committed_cursor);
      }
      const ids = (committedFrame.results ?? []).map(
        /** @param {any} r */ (r) => r.recipient_id,
      );
      await adapter.markCommitted(ids);
      committed.push(...ids);
    }
    return committed;
  }

  return {
    get epoch() {
      return epoch;
    },
    get resumeCursor() {
      return resumeCursor;
    },
    get scanAfter() {
      return scanAfter;
    },
    get ready() {
      return ready;
    },
    systemInbox,
    get diagnostics() {
      return structuredClone(diagnostics);
    },
    start,
    disconnect,
    stop,
    /** Private HTTPS submission; never bypass readiness or expose private response to the model.
     * @param {import('../../generated/network/SubmitRequest.js').SubmitRequest} request */
    async submit(request) {
      requireReady();
      if (!options.submit)
        throw new SessionError('not_ready', 'submission port unavailable');
      return options.submit(request);
    },
    pull,
    rescan() {
      rescan = true;
      if (ready && !fetchInflight) void pull().catch(() => {});
    },
    /** Fixed protocol response type, fully verified before returning to a host port.
     * @param {string} type @param {Record<string,unknown>} fields @param {string} responseType */
    async request(type, fields, responseType) {
      requireReady();
      const requestedGeneration = generation;
      const id = newId('req-control');
      const registration =
        type === 'local_register'
          ? decodeJson(
              encodeLocalRegister({
                .../** @type {any} */ (fields),
                requestId: id,
                epoch,
                bootId,
              }),
            )
          : null;
      const result = await exchange(
        registration
          ? encodeJson(registration)
          : encodeJson({ ...fields, app_version: '1.0', type, request_id: id }),
        id,
      );
      requireReady();
      if (requestedGeneration !== generation)
        throw new SessionError(
          'stale_epoch',
          'connection changed during control request',
        );
      if (result.type !== responseType)
        throw new SessionError(
          'network.protocol_error',
          'unexpected response type',
        );
      if (registration) assertRegisterResult(result, registration);
      return result;
    },
    /** Exposed for tests that inject a FETCH_RESULT without the wire. */
    applyFetchResult,
  };
}
