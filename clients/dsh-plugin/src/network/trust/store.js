/* global structuredClone */
import { createPublicKey } from 'node:crypto';
import { Buffer } from 'node:buffer';
import { SessionError } from '../session/errors.js';
import { canonicalize } from './jcs.js';

/** Trusted local configuration, supplied only after provisioning/root validation.
 * @typedef {{kid: string, publicKeySpki: string, alg: string, purpose: string,
 * status: string, notBefore: string, notAfter: string}} TrustKey
 * @typedef {{realm: string, version: string, issuedAt: string, validUntil: string, keys: TrustKey[]}} TrustConfig
 * @typedef {{load(): Promise<any>, commit(value: TrustConfig): Promise<void>}} TrustCache
 */
const purposes = new Set([
  'business_delivery',
  'protocol_response',
  'management_state',
]);
/** @param {string} detail @returns {never} */
function unavailable(detail) {
  throw new SessionError('network.key_unavailable', detail);
}
/** @param {any} value */
export function positiveVersion(value) {
  return (
    typeof value === 'string' &&
    /^[1-9][0-9]{0,19}$/.test(value) &&
    BigInt(value) <= 18446744073709551615n
  );
}
/** @param {any} value */
export function timeValue(value) {
  if (
    typeof value !== 'string' ||
    !/^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ$/.test(value)
  )
    return NaN;
  const time = Date.parse(value);
  return Number.isFinite(time) &&
    new Date(time).toISOString() === value.replace('Z', '.000Z')
    ? time
    : NaN;
}

/** Cache must be private, durable and single-writer, like the host ledger.
 * The configuration is a trust anchor; never pass a keyset taken from a frame.
 * @param {{cache: TrustCache, config?: TrustConfig, now?: () => number}} options
 */
export async function openTrustStore({ cache, config, now = Date.now }) {
  /** @type {TrustConfig | undefined} */
  let current;
  /** @type {Map<string, import('node:crypto').KeyObject>} */
  let publicKeys = new Map();
  let blocked = false;
  let queue = Promise.resolve();
  /** @param {TrustConfig} input */
  function validate(input) {
    const c = structuredClone(input);
    if (
      !c ||
      !positiveVersion(c.realm) ||
      !positiveVersion(c.version) ||
      !Array.isArray(c.keys) ||
      c.keys.length > 128 ||
      !Number.isFinite(timeValue(c.issuedAt)) ||
      !(timeValue(c.validUntil) > timeValue(c.issuedAt))
    )
      unavailable('invalid keyset');
    const keys = new Map();
    for (const k of c.keys) {
      if (
        !k ||
        !/^[A-Za-z0-9._-]{1,128}$/.test(k.kid) ||
        keys.has(k.kid) ||
        k.alg !== 'Ed25519' ||
        !purposes.has(k.purpose) ||
        !['active', 'revoked'].includes(k.status) ||
        !Number.isFinite(timeValue(k.notBefore)) ||
        !(timeValue(k.notAfter) > timeValue(k.notBefore)) ||
        typeof k.publicKeySpki !== 'string' ||
        !/^[A-Za-z0-9_-]+$/.test(k.publicKeySpki)
      )
        unavailable('invalid key');
      try {
        const der = Buffer.from(k.publicKeySpki, 'base64url');
        const key = createPublicKey({ key: der, format: 'der', type: 'spki' });
        if (
          key.asymmetricKeyType !== 'ed25519' ||
          key.export({ format: 'der', type: 'spki' }).toString('base64url') !==
            k.publicKeySpki
        )
          unavailable('invalid public key');
        keys.set(k.kid, key);
      } catch {
        unavailable('invalid public key');
      }
    }
    return { c, keys };
  }
  const persisted = await cache.load();
  if (persisted !== undefined) {
    const checked = validate(persisted);
    current = checked.c;
    publicKeys = checked.keys;
  }
  /** @param {TrustConfig} input */
  function update(input) {
    // Snapshot before awaiting; callers cannot mutate an in-flight update.
    const checked = validate(input);
    const op = queue.then(async () => {
      const c = checked.c;
      if (current) {
        if (
          c.realm !== current.realm ||
          BigInt(c.version) < BigInt(current.version)
        )
          unavailable('keyset rollback');
        if (c.version === current.version) {
          if (canonicalize(c) !== canonicalize(current))
            unavailable('keyset version reused');
          return; // Re-reading a keyset must never refresh its expiry.
        }
      }
      blocked = true;
      await cache.commit(c); // No new key is live before durable commit.
      current = c;
      publicKeys = checked.keys;
      blocked = false;
    });
    queue = op.catch(() => {});
    return op;
  }
  const store = {
    update,
    /** @param {string} kid @param {string} purpose @param {string} version */
    key(kid, purpose, version) {
      const clock = now();
      if (
        blocked ||
        !current ||
        !Number.isFinite(clock) ||
        clock < timeValue(current.issuedAt) ||
        clock >=
          Math.min(
            timeValue(current.validUntil),
            timeValue(current.issuedAt) + 86400000,
          ) ||
        !positiveVersion(version) ||
        BigInt(version) > BigInt(current.version)
      )
        return unavailable('keyset unavailable');
      const k = current.keys.find((k) => k.kid === kid);
      if (
        !k ||
        k.status !== 'active' ||
        clock < timeValue(k.notBefore) ||
        clock >= timeValue(k.notAfter)
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
        notBefore: timeValue(k.notBefore),
        notAfter: timeValue(k.notAfter),
      };
    },
  };
  if (config) await update(config);
  return store;
}
