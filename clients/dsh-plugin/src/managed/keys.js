// @ts-nocheck
import { readFileSync, watch as fsWatch } from 'node:fs';
import { createPublicKey } from 'node:crypto';

/**
 * Load / cache Ed25519 identity-header (or panel) public keys from a local JWKS file.
 * Contract: AURIGA_IDENTITY_KEYS_FILE only — no remote fetch.
 */

const X_PATTERN = /^[A-Za-z0-9_-]{43}$/;
const RELOAD_MIN_INTERVAL_MS = 1000;

/**
 * @typedef {{ kty: string, crv: string, x: string, kid: string, alg?: string, use?: string, nbf?: number, exp?: number }} IdentityJwk
 * @typedef {{ keys: IdentityJwk[] }} IdentityKeySet
 */

/**
 * @param {unknown} jwk
 * @returns {jwk is IdentityJwk}
 */
export function isValidIdentityJwk(jwk) {
  if (!jwk || typeof jwk !== 'object') return false;
  const k = /** @type {Record<string, unknown>} */ (jwk);
  if (k.kty !== 'OKP' || k.crv !== 'Ed25519') return false;
  if (typeof k.kid !== 'string' || !k.kid) return false;
  if (typeof k.x !== 'string' || !X_PATTERN.test(k.x)) return false;
  if (k.alg != null && k.alg !== 'EdDSA') return false;
  return true;
}

/**
 * @param {IdentityJwk} jwk
 * @param {number} nowSec
 */
export function jwkToPublicKey(jwk, nowSec = Math.floor(Date.now() / 1000)) {
  if (!isValidIdentityJwk(jwk)) return null;
  if (typeof jwk.nbf === 'number' && nowSec < jwk.nbf - 5) return null;
  if (typeof jwk.exp === 'number' && nowSec > jwk.exp + 5) return null;
  try {
    return createPublicKey({
      key: { kty: 'OKP', crv: 'Ed25519', x: jwk.x },
      format: 'jwk',
    });
  } catch {
    return null;
  }
}

/**
 * Validate and normalize a key set; throws if empty or malformed.
 * @param {unknown} parsed
 * @returns {IdentityKeySet}
 */
export function assertKeySet(parsed) {
  if (!parsed || typeof parsed !== 'object' || !Array.isArray(parsed.keys)) {
    throw new Error('identity keys must be { keys: [...] }');
  }
  const keys = [];
  for (const item of parsed.keys) {
    if (!isValidIdentityJwk(item)) {
      throw new Error('identity keys entry must be Ed25519 OKP with kid and x');
    }
    keys.push(item);
  }
  if (keys.length < 1)
    throw new Error('identity keys must contain at least one key');
  return { keys };
}

/**
 * @param {string | IdentityKeySet} source
 * @returns {IdentityKeySet}
 */
export function loadKeySet(source) {
  if (typeof source === 'object' && source) return assertKeySet(source);
  const raw = readFileSync(/** @type {string} */ (source), 'utf8');
  return assertKeySet(JSON.parse(raw));
}

/**
 * @param {{
 *   file?: string,
 *   keySet?: IdentityKeySet,
 *   watch?: boolean,
 *   now?: () => number,
 * }} [opts]
 */
export function createIdentityKeyCache(opts = {}) {
  if (!opts.file && !opts.keySet) {
    throw new Error(
      'auriga-dsh: identity keys required (AURIGA_IDENTITY_KEYS_FILE or in-memory keySet)',
    );
  }

  /** @type {IdentityKeySet} */
  let keySet = opts.keySet ? assertKeySet(opts.keySet) : { keys: [] };
  let loadedFromFile = false;
  let lastReloadAt = 0;
  const now = opts.now ?? (() => Math.floor(Date.now() / 1000));
  /** @type {import('node:fs').FSWatcher | undefined} */
  let watcher;

  const reloadFile = () => {
    if (!opts.file) return;
    keySet = loadKeySet(opts.file);
    loadedFromFile = true;
    lastReloadAt = Date.now();
  };

  if (opts.file) {
    reloadFile();
    if (opts.watch !== false) {
      try {
        watcher = fsWatch(opts.file, () => {
          try {
            reloadFile();
          } catch {
            /* keep previous keys on transient read errors */
          }
        });
      } catch {
        /* watch optional */
      }
    }
  }

  /**
   * @param {string} kid
   */
  const resolve = (kid) => {
    const tryFind = () => {
      for (const jwk of keySet.keys) {
        if (jwk.kid !== kid) continue;
        return jwkToPublicKey(jwk, now());
      }
      return null;
    };
    let key = tryFind();
    if (!key && opts.file && loadedFromFile) {
      const elapsed = Date.now() - lastReloadAt;
      if (elapsed >= RELOAD_MIN_INTERVAL_MS) {
        try {
          reloadFile();
        } catch {
          /* ignore */
        }
        key = tryFind();
      }
    }
    return key;
  };

  return {
    snapshot() {
      return { keys: [...keySet.keys] };
    },
    /** @param {string} kid */
    async getPublicKey(kid) {
      return resolve(kid);
    },
    close() {
      watcher?.close();
    },
  };
}
