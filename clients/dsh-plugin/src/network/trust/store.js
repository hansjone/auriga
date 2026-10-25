import { SessionError } from '../session/errors.js';
import { canonicalize } from './jcs.js';

import {
  positiveVersion,
  timeValue,
  unavailable,
  publicKey,
  validatePublicKey,
  validatePublication,
} from './keyset.js';
export { positiveVersion, timeValue } from './keyset.js';
/** @typedef {import('../../generated/network/keyset-types.d.ts').SignedKeyset} TrustConfig
 * @typedef {import('../../generated/network/keyset-types.d.ts').KeysetPayload} Payload
 * @typedef {import('../../generated/network/keyset-types.d.ts').PublicKey} TrustKey
 * @typedef {{load(): Promise<any>, commit(value: any): Promise<void>}} TrustCache */

/** Cache must be private, durable and single-writer, like the host ledger.
 * The configuration is a trust anchor; never pass a keyset taken from a frame.
 * refresh fetches a SignedKeyset from fixed trusted configuration; roots verify it;
 * frame-provided version/kid are hints, never authority or a URL.
 * @param {{cache: TrustCache, config?: TrustConfig|string|Uint8Array, roots: Pick<TrustKey, 'kid'|'public_key_spki'>[], now?: () => number,
 * refresh?: (request: {realm: string, minimumVersion: string, kid: string}) => Promise<TrustConfig>}} options
 */
export async function openTrustStore({
  cache,
  config,
  roots,
  now = Date.now,
  refresh,
}) {
  /** @type {Payload | undefined} */
  let current;
  /** @type {Map<string, import('node:crypto').KeyObject>} */
  let publicKeys = new Map();
  let blocked = false;
  let queue = Promise.resolve();
  /** @type {Promise<void>|undefined} */
  let refreshing;
  let nextRefreshAt = -Infinity;
  const rootKeys = new Map();
  for (const root of roots) {
    if (
      !root ||
      !/^[A-Za-z0-9._-]{1,128}$/.test(root.kid) ||
      rootKeys.has(root.kid)
    )
      unavailable('invalid trust root');
    rootKeys.set(root.kid, publicKey(root.public_key_spki));
  }
  /** @type {Map<string, TrustKey>} */
  let history = new Map();
  /** @param {Payload} payload */
  function nextHistory(payload) {
    const next = new Map(history);
    for (const k of payload.keys) {
      const old = history.get(k.kid);
      if (
        old &&
        (old.public_key_spki !== k.public_key_spki ||
          old.purpose !== k.purpose ||
          (old.state === 'revoked' && k.state !== 'revoked') ||
          (old.state === 'retired' &&
            !['retired', 'revoked'].includes(k.state)) ||
          (old.state === 'active' && k.state === 'next'))
      )
        unavailable('key identity or state rollback');
      if (
        [...history.values()].some(
          (prior) =>
            prior.kid !== k.kid && prior.public_key_spki === k.public_key_spki,
        )
      )
        unavailable('public key reused');
      next.set(k.kid, k);
    }
    return next;
  }
  const persisted = await cache.load();
  if (persisted !== undefined) {
    const checked = validatePublication(persisted.publication, rootKeys);
    if (!Array.isArray(persisted.history)) unavailable('invalid key history');
    for (const k of persisted.history) {
      validatePublicKey(k);
      if (history.has(k.kid)) unavailable('duplicate key history');
      history.set(k.kid, k);
    }
    if (checked.publication.keyset.keys.some((k) => !history.has(k.kid)))
      unavailable('incomplete key history');
    history = nextHistory(checked.publication.keyset);
    current = checked.publication.keyset;
    publicKeys = checked.keys;
  }
  /** @param {TrustConfig|string|Uint8Array} input */
  async function update(input) {
    // Snapshot before awaiting; callers cannot mutate an in-flight update.
    const checked = validatePublication(input, rootKeys);
    const op = queue.then(async () => {
      const c = checked.publication.keyset;
      if (current) {
        if (
          c.realm !== current.realm ||
          BigInt(c.keyset_version) < BigInt(current.keyset_version)
        )
          unavailable('keyset rollback');
        if (c.keyset_version === current.keyset_version) {
          if (canonicalize(c) !== canonicalize(current))
            unavailable('keyset version reused');
          return; // Re-reading a keyset must never refresh its expiry.
        }
      }
      const next = nextHistory(c);
      blocked = true;
      await cache.commit({
        publication: checked.publication,
        history: [...next.values()],
      }); // No new key is live before durable commit.
      current = c;
      history = next;
      publicKeys = checked.keys;
      blocked = false;
    });
    queue = op.catch(() => {});
    return op;
  }
  const store = {
    update,
    /** Merge refreshes across all envelopes; bound unsatisfied hints to one
     * attempt per second, including failed/stale responses. No automatic loop.
     * @param {string} kid @param {string} version */
    async refreshFor(kid, version) {
      if (
        !current ||
        !positiveVersion(version) ||
        typeof kid !== 'string' ||
        !/^[A-Za-z0-9._-]{1,128}$/.test(kid)
      )
        return;
      if (
        BigInt(version) <= BigInt(current.keyset_version) &&
        current.keys.some((k) => k.kid === kid)
      )
        return;
      if (refreshing) return refreshing;
      const clock = now();
      if (!refresh || !Number.isFinite(clock) || clock < nextRefreshAt) return;
      nextRefreshAt = clock + 1000;
      const request = { realm: current.realm, minimumVersion: version, kid };
      refreshing = Promise.resolve()
        .then(async () => {
          try {
            await update(await refresh(request));
            // Successful rotations must not delay a subsequent real rotation.
            if (
              current &&
              BigInt(current.keyset_version) >= BigInt(version) &&
              current.keys.some((k) => k.kid === kid)
            )
              nextRefreshAt = -Infinity;
          } catch {
            unavailable('key refresh failed');
          }
        })
        .finally(() => {
          refreshing = undefined;
        });
      return refreshing;
    },
    /** @param {string} kid @param {string} purpose @param {string} version */
    key(kid, purpose, version) {
      const clock = now();
      if (
        blocked ||
        !current ||
        !Number.isFinite(clock) ||
        clock < timeValue(current.issued_at) ||
        clock >=
          Math.min(
            timeValue(current.valid_until),
            timeValue(current.issued_at) + 86400000,
          ) ||
        !positiveVersion(version) ||
        BigInt(version) > BigInt(current.keyset_version)
      )
        return unavailable('keyset unavailable');
      const k = current.keys.find((k) => k.kid === kid);
      if (
        !k ||
        !['active', 'retired'].includes(k.state) ||
        clock < timeValue(k.not_before) ||
        clock >= timeValue(k.not_after)
      )
        return unavailable('key unavailable');
      if (k.purpose !== purpose)
        throw new SessionError(
          'network.signature_invalid',
          'key purpose mismatch',
        );
      return {
        publicKey: publicKeys.get(kid),
        realm: current.realm,
        notBefore: timeValue(k.not_before),
        notAfter: timeValue(k.not_after),
      };
    },
  };
  if (config) await update(config);
  return store;
}
