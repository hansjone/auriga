// @ts-nocheck
import { readFileSync, watch as fsWatch } from 'node:fs';
import { createPublicKey } from 'node:crypto';

/**
 * Load / cache Ed25519 identity-header public keys (JWKS subset).
 * Primary source: AURIGA_IDENTITY_KEYS_FILE; optional URL for rotation fetch.
 */

/**
 * @typedef {{ kty: string, crv: string, x: string, kid: string, alg?: string, use?: string, nbf?: number, exp?: number }} IdentityJwk
 * @typedef {{ keys: IdentityJwk[] }} IdentityKeySet
 */

/**
 * @param {IdentityJwk} jwk
 * @param {number} nowSec
 * @returns {import('node:crypto').KeyObject | null}
 */
export function jwkToPublicKey(jwk, nowSec = Math.floor(Date.now() / 1000)) {
  if (jwk.kty !== 'OKP' || jwk.crv !== 'Ed25519' || !jwk.kid || !jwk.x) return null;
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
 * @param {string | IdentityKeySet} source file path or already-parsed key set
 * @returns {IdentityKeySet}
 */
export function loadKeySet(source) {
  if (typeof source === 'object' && source && Array.isArray(source.keys))
    return { keys: source.keys };
  const raw = readFileSync(/** @type {string} */ (source), 'utf8');
  const parsed = JSON.parse(raw);
  if (!parsed || !Array.isArray(parsed.keys))
    throw new Error('identity keys file must contain keys[]');
  return { keys: parsed.keys };
}

/**
 * @param {{
 *   file?: string,
 *   keySet?: IdentityKeySet,
 *   keysUrl?: string,
 *   fetchImpl?: typeof fetch,
 *   watch?: boolean,
 *   now?: () => number,
 * }} [opts]
 */
export function createIdentityKeyCache(opts = {}) {
  /** @type {IdentityKeySet} */
  let keySet = opts.keySet ?? { keys: [] };
  let loadedFromFile = false;
  const now = opts.now ?? (() => Math.floor(Date.now() / 1000));
  /** @type {import('node:fs').FSWatcher | undefined} */
  let watcher;

  const reloadFile = () => {
    if (!opts.file) return;
    keySet = loadKeySet(opts.file);
    loadedFromFile = true;
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
        /* watch optional (tests / missing FS) */
      }
    }
  }

  /**
   * @param {string} kid
   * @returns {import('node:crypto').KeyObject | null}
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
      try {
        reloadFile();
      } catch {
        /* ignore */
      }
      key = tryFind();
    }
    return key;
  };

  return {
    /** @returns {IdentityKeySet} */
    snapshot() {
      return { keys: [...keySet.keys] };
    },
    /**
     * Look up kid; on miss, re-read file once (protocol) then optional URL.
     * @param {string} kid
     */
    async getPublicKey(kid) {
      let key = resolve(kid);
      if (key) return key;
      if (opts.keysUrl && opts.fetchImpl) {
        try {
          const res = await opts.fetchImpl(opts.keysUrl, {
            redirect: 'error',
            headers: { accept: 'application/json' },
          });
          if (res.ok) {
            const body = /** @type {IdentityKeySet} */ (await res.json());
            if (body?.keys) keySet = { keys: body.keys };
            key = resolve(kid);
          }
        } catch {
          /* keep cache */
        }
      }
      return key;
    },
    close() {
      watcher?.close();
    },
  };
}
