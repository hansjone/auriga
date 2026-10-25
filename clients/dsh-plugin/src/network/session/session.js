import { randomBytes } from 'node:crypto';
import {
  assertFrameEpoch,
  decodeJson,
  encodeFetch,
  encodeReady,
  encodeReceipt,
  frameType,
} from './frames.js';
import { SessionError } from './errors.js';
import { buildVerifiedPage } from './page.js';
import { createSystemInbox, systemPromptFrom } from './system-inbox.js';

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
 * }} options
 */
export function createNetworkSession(options) {
  const transport = options.transport;
  const adapter = options.adapter;
  const identity = options.identity;
  const bootId = options.bootId ?? 'boot-1';
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
  let fetchInflight = false;
  /** @type {(() => void) | null} */
  let unsub = null;
  /** @type {Map<string, { resolve: (v: any) => void, reject: (e: Error) => void }>} */
  const pending = new Map();

  function requireReady() {
    if (!ready) throw new SessionError('not_ready', 'session not ready');
  }

  /**
   * @param {string} requestId
   * @param {number} [timeoutMs]
   */
  function waitFor(requestId, timeoutMs = 5_000) {
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

  /** @param {Uint8Array} payload */
  async function onPayload(payload) {
    let frame;
    try {
      frame = decodeJson(payload);
    } catch {
      return;
    }
    const type = frameType(frame);
    try {
      if (type !== 'error' && type !== 'ready_result') {
        assertFrameEpoch(frame, epoch);
      }
    } catch (e) {
      if (e instanceof SessionError && e.code === 'stale_epoch') {
        // Drop stale frames; do not deliver into the ledger.
        return;
      }
      throw e;
    }

    if (type === 'ready_result') {
      settle(frame.request_id, frame);
      return;
    }
    if (type === 'head') {
      // Tip nudge: schedule a fetch when idle.
      void pull().catch(() => {});
      return;
    }
    if (type === 'fetch_result') {
      settle(frame.page?.request_id, frame);
      return;
    }
    if (type === 'receipt_committed') {
      settle(frame.request_id, frame);
      return;
    }
    if (type === 'error') {
      const id = frame.request_id;
      if (id && pending.has(id)) {
        const p = pending.get(id);
        pending.delete(id);
        p.reject(
          new SessionError(frame.code ?? 'remote_error', 'remote ERROR frame'),
        );
      }
    }
  }

  /**
   * READY handshake. Returns READY_RESULT (epoch / cursors).
   * @param {{
   *   resumeCursor?: string,
   *   localHighWater?: string,
   *   registrationRevision?: string,
   *   receiptCheckpoint?: string,
   * }} [hint]
   */
  async function start(hint = {}) {
    if (ready) throw new SessionError('invalid_state', 'already started');
    unsub = transport.onMessage((payload) => {
      void onPayload(payload);
    });
    const requestId = newId('req-ready');
    const wait = waitFor(requestId);
    await transport.send(
      encodeReady({
        requestId,
        channelNonce: newId('nonce'),
        resumeCursor: hint.resumeCursor ?? resumeCursor,
        localHighWater: hint.localHighWater ?? '0',
        registrationRevision: hint.registrationRevision ?? '0',
        receiptCheckpoint: hint.receiptCheckpoint ?? '0',
      }),
    );
    const result = await wait;
    if (result.status !== 'ready' && result.status !== undefined) {
      // Some stubs omit status; require epoch.
      if (!result.epoch) {
        throw new SessionError('ready_failed', 'READY_RESULT not ready');
      }
    }
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

  async function stop() {
    ready = false;
    unsub?.();
    unsub = null;
    for (const [, p] of pending) {
      p.reject(new SessionError('stopped', 'session stopped'));
    }
    pending.clear();
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
      const requestId = newId('req-fetch');
      const wait = waitFor(requestId);
      await transport.send(
        encodeFetch({
          requestId,
          epoch,
          resumeCursor,
          continuation: { scan_after: scanAfter },
          maxItems: opts.maxItems,
          maxBytes: opts.maxBytes,
        }),
      );
      const result = await wait;
      return applyFetchResult(result);
    } finally {
      fetchInflight = false;
    }
  }

  /** @param {any} result */
  async function applyFetchResult(result) {
    if (frameType(result) !== 'fetch_result') {
      throw new SessionError('bad_fetch_result', 'expected fetch_result');
    }
    if (result.page?.epoch === undefined) {
      throw new SessionError('bad_fetch_result', 'fetch_page missing epoch');
    }
    assertFrameEpoch(result, epoch);
    const pageEpoch = String(result.page.epoch);
    const page = buildVerifiedPage({
      tenant: identity.tenant,
      nodeIncarnation: identity.nodeIncarnation,
      epoch: pageEpoch,
      bootId,
      items: result.items ?? [],
    });
    // scan_after advances pagination; resume_cursor only tracks confirmed prefix.
    if (result.page?.scan_after !== undefined) {
      scanAfter = String(result.page.scan_after);
    }
    if (result.page?.committed_cursor !== undefined) {
      resumeCursor = String(result.page.committed_cursor);
    }
    if (page.items.length === 0) {
      return { receipts: [], committed: [] };
    }

    const receipts = await adapter.deliver(page);
    await publishSystemPrompts(page, receipts);
    const committed = await submitReceipts(page, receipts);
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
        acceptanceMode: receipt.acceptanceMode,
        messageDigest: receipt.messageDigest,
        hostRequestId: receipt.hostRequestId,
      });
    }

    /** @type {string[]} */
    const committed = [];
    for (const [deliveryId, group] of byDelivery) {
      if (group.results.length === 0) continue;
      const requestId = newId('req-rcpt');
      const operationId = newId('op-rcpt');
      const wait = waitFor(requestId);
      await transport.send(
        encodeReceipt({
          requestId,
          operationId,
          epoch,
          deliveryId,
          mailSeq: group.mailSeq,
          results: group.results,
        }),
      );
      const committedFrame = await wait;
      if (frameType(committedFrame) !== 'receipt_committed') {
        throw new SessionError('bad_receipt', 'expected receipt_committed');
      }
      if (committedFrame.committed_cursor !== undefined) {
        resumeCursor = String(committedFrame.committed_cursor);
      }
      const ids = (committedFrame.results ?? []).map((r) => r.recipient_id);
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
    start,
    stop,
    pull,
    /** Exposed for tests that inject a FETCH_RESULT without the wire. */
    applyFetchResult,
  };
}
