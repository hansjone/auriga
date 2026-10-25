/* global structuredClone */
import { nodeKey, parseAddress } from '../host/address.js';
import { newId } from '../host/ledger.js';
import { CODES, HostSeamError } from '../host/errors.js';

export const LONG_SCAN_WINDOW_MS = 24 * 60 * 60 * 1000;
export const LONG_SCAN_LOCAL_THRESHOLD = 100;

/**
 * Private local state. No bodies, native session IDs, threads or target
 * locals are included in security reports. Send only via a trusted port.
 * @typedef {{ id: string, local: string, at: number }} InvalidHit
 * @typedef {{ hits: InvalidHit[], blockedUntil: number, manual: boolean, lastSeen?: number, longTargets?: Array<{ local: string, at: number }> }} SourceState
 * @typedef {{ id: string, kind: 'scan', sourceNode: string, invalidHits: number, distinctLocals: number, longDistinctLocals: number, at: number }} ScanReport
 * @typedef {{ sources: number, shortHits: number, longTargetStrikes: number, lastAt: number }} EvictedScanTotals
 * @typedef {{ id: string, kind: 'source_capacity', evictedSources: number, untrackedSources: number, evictedScan: EvictedScanTotals, at: number }} CapacityReport
 * @typedef {ScanReport|CapacityReport} SecurityReport
 * @typedef {{ sources: Record<string, SourceState>, reports: SecurityReport[], lastCapacityAlertAt?: number, evictedScan?: EvictedScanTotals }} InboundPolicyState
 */

/**
 * Call only with verified source metadata, never identity from message.body.
 * The policy has no host/model port and cannot execute remote instructions.
 * @param {{
 *   ledger: import('../host/ledger.js').Ledger,
 *   now?: () => number,
 *   hitThreshold?: number, localThreshold?: number,
 *   windowMs?: number, cooldownMs?: number,
 *   longWindowMs?: number, longLocalThreshold?: number,
 *   maxSources?: number, maxReports?: number,
 * }} options
 * maxReports caps scan summaries. A separate single slot is reserved for
 * the coalesced capacity warning, for a total bound of maxReports + 1.
 */
export function createInboundPolicy({
  ledger,
  now = () => Date.parse(ledger.nowIso()),
  hitThreshold = 20,
  localThreshold = 10,
  windowMs = 60_000,
  cooldownMs = 600_000,
  longWindowMs = LONG_SCAN_WINDOW_MS,
  longLocalThreshold = LONG_SCAN_LOCAL_THRESHOLD,
  maxSources = 1024,
  maxReports = 128,
}) {
  for (const value of [
    hitThreshold,
    localThreshold,
    windowMs,
    cooldownMs,
    longWindowMs,
    longLocalThreshold,
    maxSources,
    maxReports,
  ])
    if (!Number.isSafeInteger(value) || value < 1)
      throw new RangeError('invalid inbound policy limit');

  /** @param {import('../host/address.js').Address} from */
  function sourceKey(from) {
    return nodeKey(
      parseAddress(from, { realm: ledger.identity.realm, role: 'source' }),
    );
  }

  /** @param {SourceState} source @param {number} at */
  const blocked = (source, at) => source.manual || source.blockedUntil > at;

  /** @param {SourceState} source */
  const hasStrikes = (source) =>
    source.hits.length > 0 || (source.longTargets?.length ?? 0) > 0;

  /** @param {InboundPolicyState} state */
  function evictionSnapshot(state) {
    return {
      ...(state.evictedScan ?? {
        sources: 0,
        shortHits: 0,
        longTargetStrikes: 0,
        lastAt: 0,
      }),
    };
  }

  /** Preserve bounded, source-free evidence before deleting any live strike.
   * This commits in the same ledger transaction as replacement and alert.
   * @param {InboundPolicyState} state @param {SourceState} source @param {number} at */
  function accumulateEvictedScan(state, source, at) {
    const totals = evictionSnapshot(state);
    totals.sources++;
    totals.shortHits += source.hits.length;
    totals.longTargetStrikes += source.longTargets?.length ?? 0;
    totals.lastAt = at;
    state.evictedScan = totals;
  }

  /** Keep a single bounded, coalesced pressure warning. Cooling entries
   * are never evicted, and capacity exhaustion never blocks unknown peers.
   * @param {InboundPolicyState} state @param {number} at @param {boolean} evicted @param {boolean} evictedStrike */
  function reportCapacity(state, at, evicted, evictedStrike) {
    const previous = state.reports.find((r) => r.kind === 'source_capacity');
    if (previous?.kind === 'source_capacity') {
      // A stale acknowledgement must not discard an updated summary.
      previous.id = newId('capacity');
      previous.at = at;
      previous.evictedScan = evictionSnapshot(state);
      if (evicted) previous.evictedSources++;
      else previous.untrackedSources++;
      return;
    }
    if (
      !evictedStrike &&
      state.lastCapacityAlertAt !== undefined &&
      at - state.lastCapacityAlertAt < windowMs
    )
      return;
    state.lastCapacityAlertAt = at;
    state.reports.push({
      id: newId('capacity'),
      kind: 'source_capacity',
      at,
      evictedSources: evicted ? 1 : 0,
      untrackedSources: evicted ? 0 : 1,
      evictedScan: evictionSnapshot(state),
    });
  }

  /** @param {InboundPolicyState} state @param {string} key @param {number} at */
  function ensureSource(state, key, at) {
    // Reclaim all strike-free entries first; never reclaim an active block.
    for (const [id, source] of Object.entries(state.sources)) {
      source.lastSeen ??= Math.max(0, ...source.hits.map((h) => h.at));
      // Existing ledgers retain the short-window evidence they already had.
      source.longTargets ??= [
        ...new Map(
          source.hits.map((h) => [h.local, { local: h.local, at: h.at }]),
        ).values(),
      ];
      source.hits = source.hits.filter((h) => h.at > at - windowMs);
      source.longTargets = source.longTargets.filter(
        (h) => h.at > at - longWindowMs,
      );
      if (!blocked(source, at) && !hasStrikes(source)) delete state.sources[id];
    }
    if (
      !state.sources[key] &&
      Object.keys(state.sources).length >= maxSources
    ) {
      const candidate = Object.entries(state.sources)
        .filter(([, source]) => !blocked(source, at))
        .sort(
          (a, b) =>
            Number(hasStrikes(a[1])) - Number(hasStrikes(b[1])) ||
            (a[1].lastSeen ?? 0) - (b[1].lastSeen ?? 0),
        )[0];
      const evictedStrike = !!candidate && hasStrikes(candidate[1]);
      if (evictedStrike) accumulateEvictedScan(state, candidate[1], at);
      reportCapacity(state, at, !!candidate, evictedStrike);
      if (!candidate) return null;
      delete state.sources[candidate[0]];
    }
    return (state.sources[key] ??= {
      hits: [],
      blockedUntil: 0,
      manual: false,
      lastSeen: at,
      longTargets: [],
    });
  }

  /** @param {InboundPolicyState} state @param {string} key @param {SourceState} source @param {number} at */
  function report(state, key, source, at) {
    const summary = {
      id: newId('scan'),
      kind: /** @type {const} */ ('scan'),
      sourceNode: key,
      invalidHits: source.hits.length,
      distinctLocals: new Set(source.hits.map((h) => h.local)).size,
      longDistinctLocals: source.longTargets?.length ?? 0,
      at,
    };
    // One pending summary per source; bounded queue full never unblocks it.
    const existing = state.reports.findIndex(
      (r) => r.kind === 'scan' && r.sourceNode === key,
    );
    if (existing >= 0) state.reports[existing] = summary;
    else if (
      state.reports.filter((r) => r.kind !== 'source_capacity').length <
      maxReports
    )
      state.reports.push(summary);
  }

  /** Apply in the same transaction as refusal evidence, so a crash cannot
   * erase strikes while a durable rejection suppresses their replay.
   * @param {import('../host/ledger.js').LedgerState} draft
   * @param {{ from: import('../host/address.js').Address, recipientId: string, local: string }} input */
  function recordInvalidIn(draft, { from, recipientId, local }) {
    const key = sourceKey(from);
    const state = (draft.inboundPolicy ??= { sources: {}, reports: [] });
    const at = now();
    const source = ensureSource(state, key, at);
    if (
      !source ||
      blocked(source, at) ||
      source.hits.some((h) => h.id === recipientId)
    )
      return;
    source.hits.push({ id: recipientId, local, at });
    source.lastSeen = at;
    const targets = (source.longTargets ??= []);
    const previous = targets.find((h) => h.local === local);
    if (previous) previous.at = at;
    else targets.push({ local, at });
    if (
      source.hits.length >= hitThreshold ||
      new Set(source.hits.map((h) => h.local)).size >= localThreshold ||
      targets.length >= longLocalThreshold
    ) {
      source.blockedUntil = at + cooldownMs;
      report(state, key, source, at);
    }
  }

  return {
    /** @param {import('../host/address.js').Address} from */
    isBlocked(from) {
      ledger.assertAvailable();
      const key = sourceKey(from);
      const sources = ledger.peek().inboundPolicy?.sources ?? {};
      const source = sources[key];
      return source ? blocked(source, now()) : false;
    },
    /** @param {{ from: import('../host/address.js').Address, recipientId: string, local: string }} input */
    recordInvalid({ from, recipientId, local }) {
      return ledger.update((draft) =>
        recordInvalidIn(draft, { from, recipientId, local }),
      );
    },
    recordInvalidIn,
    /** Local authenticated UI only; not a remotely callable tool.
     * @param {import('../host/address.js').Address} from */
    async blockSource(from) {
      const key = sourceKey(from);
      const retained = await ledger.update((draft) => {
        const state = (draft.inboundPolicy ??= { sources: {}, reports: [] });
        const source = ensureSource(state, key, now());
        if (!source) return false;
        source.manual = true;
        source.lastSeen = now();
        return true;
      });
      if (!retained) throw new HostSeamError(CODES.overloaded);
    },
    /** Retained until an authenticated report acknowledgement. */
    pendingReports() {
      return structuredClone(ledger.peek().inboundPolicy?.reports ?? []);
    },
    /** @param {string} id */
    markReportSent(id) {
      return ledger.update((draft) => {
        if (draft.inboundPolicy)
          draft.inboundPolicy.reports = draft.inboundPolicy.reports.filter(
            (r) => r.id !== id,
          );
      });
    },
  };
}
