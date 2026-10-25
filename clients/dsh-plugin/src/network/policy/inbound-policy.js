/* global structuredClone */
import { nodeKey, parseAddress } from '../host/address.js';
import { newId } from '../host/ledger.js';
import { CODES, HostSeamError } from '../host/errors.js';

/**
 * Private local state. No bodies, native session IDs, threads or target
 * locals are included in security reports. Send only via a trusted port.
 * @typedef {{ id: string, local: string, at: number }} InvalidHit
 * @typedef {{ hits: InvalidHit[], blockedUntil: number, manual: boolean }} SourceState
 * @typedef {{ id: string, sourceNode: string, invalidHits: number, distinctLocals: number, at: number }} SecurityReport
 * @typedef {{ sources: Record<string, SourceState>, reports: SecurityReport[] }} InboundPolicyState
 */

/**
 * Call only with verified source metadata, never identity from message.body.
 * The policy has no host/model port and cannot execute remote instructions.
 * @param {{
 *   ledger: import('../host/ledger.js').Ledger,
 *   now?: () => number,
 *   hitThreshold?: number, localThreshold?: number,
 *   windowMs?: number, cooldownMs?: number,
 *   maxSources?: number, maxReports?: number,
 * }} options
 */
export function createInboundPolicy({
  ledger,
  now = () => Date.parse(ledger.nowIso()),
  hitThreshold = 20,
  localThreshold = 10,
  windowMs = 60_000,
  cooldownMs = 600_000,
  maxSources = 1024,
  maxReports = 128,
}) {
  for (const value of [
    hitThreshold,
    localThreshold,
    windowMs,
    cooldownMs,
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

  /** @param {InboundPolicyState} state @param {string} key @param {number} at */
  function ensureSource(state, key, at) {
    // Never evict an active block to make room for a rotating scanner.
    for (const [id, source] of Object.entries(state.sources)) {
      source.hits = source.hits.filter((h) => h.at > at - windowMs);
      if (!blocked(source, at) && source.hits.length === 0)
        delete state.sources[id];
    }
    if (!state.sources[key] && Object.keys(state.sources).length >= maxSources)
      return null;
    return (state.sources[key] ??= {
      hits: [],
      blockedUntil: 0,
      manual: false,
    });
  }

  /** @param {InboundPolicyState} state @param {string} key @param {SourceState} source @param {number} at */
  function report(state, key, source, at) {
    const summary = {
      id: newId('scan'),
      sourceNode: key,
      invalidHits: source.hits.length,
      distinctLocals: new Set(source.hits.map((h) => h.local)).size,
      at,
    };
    // One pending summary per source; bounded queue full never unblocks it.
    const existing = state.reports.findIndex((r) => r.sourceNode === key);
    if (existing >= 0) state.reports[existing] = summary;
    else if (state.reports.length < maxReports) state.reports.push(summary);
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
    if (
      source.hits.length >= hitThreshold ||
      new Set(source.hits.map((h) => h.local)).size >= localThreshold
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
      if (source) return blocked(source, now());
      // A full table fails closed for unknown sources rather than silently
      // forgetting blocks. Expired entries are reclaimed on the next write.
      return (
        Object.values(sources).filter(
          (s) =>
            blocked(s, now()) || s.hits.some((h) => h.at > now() - windowMs),
        ).length >= maxSources
      );
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
    blockSource(from) {
      const key = sourceKey(from);
      return ledger.update((draft) => {
        const state = (draft.inboundPolicy ??= { sources: {}, reports: [] });
        const source = ensureSource(state, key, now());
        if (!source) throw new HostSeamError(CODES.overloaded);
        source.manual = true;
      });
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
