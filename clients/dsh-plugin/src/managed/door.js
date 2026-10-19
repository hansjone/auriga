// @ts-nocheck
import { verify as cryptoVerify } from 'node:crypto';
import { sendProblem } from './problem.js';
import { PROTOCOL_VERSION } from './protocol.js';

const CLOCK_SKEW_SEC = 5;
const UNAUTH = 'common.unauthenticated';

/**
 * @typedef {{
 *   iss: string,
 *   aud: string,
 *   sub: string,
 *   tid: string,
 *   sid: string,
 *   name?: string,
 *   mtyp?: string,
 *   iat: number,
 *   exp: number,
 *   jti: string,
 *   htm: string,
 *   htu: string,
 *   pv: number,
 * }} IdentityClaims
 */

/**
 * In-memory jti replay cache (kept until exp + skew).
 * Contract notes jti is for log correlation only; task P1 requires replay rejection.
 */
export function createReplayCache({ now = () => Math.floor(Date.now() / 1000) } = {}) {
  /** @type {Map<string, number>} */
  const seen = new Map();

  const purge = (t) => {
    for (const [jti, exp] of seen) {
      if (exp < t) seen.delete(jti);
    }
  };

  return {
    /**
     * @param {string} jti
     * @param {number} exp Unix seconds
     * @returns {boolean} true if first use
     */
    claim(jti, exp) {
      const t = now();
      purge(t);
      if (seen.has(jti)) return false;
      seen.set(jti, exp + CLOCK_SKEW_SEC);
      return true;
    },
    size() {
      return seen.size;
    },
  };
}

/**
 * @param {string} token compact JWS
 * @returns {{ header: { alg: string, kid: string, typ: string }, payloadB64: string, signingInput: string, sig: Buffer } | null}
 */
export function parseCompactJws(token) {
  if (typeof token !== 'string') return null;
  const parts = token.split('.');
  if (parts.length !== 3) return null;
  try {
    const header = JSON.parse(
      Buffer.from(parts[0], 'base64url').toString('utf8'),
    );
    if (
      header.alg !== 'EdDSA' ||
      header.typ !== 'auriga-id+jwt' ||
      typeof header.kid !== 'string' ||
      !header.kid
    )
      return null;
    const sig = Buffer.from(parts[2], 'base64url');
    return {
      header,
      payloadB64: parts[1],
      signingInput: `${parts[0]}.${parts[1]}`,
      sig,
    };
  } catch {
    return null;
  }
}

/**
 * @param {{
 *   token: string | undefined | null,
 *   method: string,
 *   path: string,
 *   instanceId: string,
 *   ownerId: string,
 *   getPublicKey: (kid: string) => Promise<import('node:crypto').KeyObject | null> | import('node:crypto').KeyObject | null,
 *   replay?: ReturnType<typeof createReplayCache>,
 *   now?: () => number,
 *   skipReplay?: boolean,
 * }} opts
 * @returns {Promise<{ ok: true, claims: IdentityClaims } | { ok: false, code: string, reason: string }>}
 */
export async function verifyIdentityHeader(opts) {
  const now = opts.now ?? (() => Math.floor(Date.now() / 1000));
  const fail = (reason) => ({ ok: /** @type {const} */ (false), code: UNAUTH, reason });

  if (!opts.token) return fail('missing');

  const parsed = parseCompactJws(opts.token);
  if (!parsed) return fail('malformed');

  const pub = await opts.getPublicKey(parsed.header.kid);
  if (!pub) return fail('kid_unknown');

  let ok = false;
  try {
    ok = cryptoVerify(null, Buffer.from(parsed.signingInput), pub, parsed.sig);
  } catch {
    ok = false;
  }
  if (!ok) return fail('signature');

  /** @type {IdentityClaims} */
  let claims;
  try {
    claims = JSON.parse(
      Buffer.from(parsed.payloadB64, 'base64url').toString('utf8'),
    );
  } catch {
    return fail('malformed');
  }

  if (claims.iss !== 'auriga-edge') return fail('iss');
  if (claims.aud !== opts.instanceId) return fail('aud');
  if (claims.sub !== opts.ownerId) return fail('subject');
  if (claims.pv !== PROTOCOL_VERSION) return fail('protocol');
  if (typeof claims.iat !== 'number' || typeof claims.exp !== 'number')
    return fail('time');
  if (claims.exp > claims.iat + 60) return fail('ttl');

  const t = now();
  if (t + CLOCK_SKEW_SEC < claims.iat) return fail('not_yet_valid');
  if (t - CLOCK_SKEW_SEC > claims.exp) return fail('expired');

  const method = String(opts.method || 'GET').toUpperCase();
  if (String(claims.htm || '').toUpperCase() !== method) return fail('htm');
  if (claims.htu !== opts.path) return fail('htu');

  if (!opts.skipReplay && opts.replay) {
    if (typeof claims.jti !== 'string' || !claims.jti) return fail('jti');
    if (!opts.replay.claim(claims.jti, claims.exp)) return fail('replay');
  }

  return { ok: true, claims };
}

export const HEALTH_PATH = '/_auriga/health';

/**
 * HTTP/WS door: require valid Auriga-Identity except health path.
 * @param {{
 *   instanceId: string,
 *   ownerId: string,
 *   getPublicKey: (kid: string) => Promise<import('node:crypto').KeyObject | null> | import('node:crypto').KeyObject | null,
 *   replay?: ReturnType<typeof createReplayCache>,
 *   now?: () => number,
 * }} cfg
 */
export function createDoorLock(cfg) {
  const replay = cfg.replay ?? createReplayCache({ now: cfg.now });

  /**
   * @param {{ method?: string, url?: string, headers?: Record<string, string | string[] | undefined> }} req
   */
  async function check(req) {
    const url = new URL(req.url ?? '/', 'http://192.0.2.10');
    const path = url.pathname;
    if (path === HEALTH_PATH) return { ok: /** @type {const} */ (true), bypass: true };

    const raw = req.headers?.['auriga-identity'] ?? req.headers?.['Auriga-Identity'];
    const token = Array.isArray(raw) ? raw[0] : raw;
    return verifyIdentityHeader({
      token,
      method: req.method ?? 'GET',
      path,
      instanceId: cfg.instanceId,
      ownerId: cfg.ownerId,
      getPublicKey: cfg.getPublicKey,
      replay,
      now: cfg.now,
    });
  }

  /**
   * Wrap http.Server emit('request') chain.
   * @param {import('node:http').Server} server
   */
  function install(server) {
    if (!server || typeof server.emit !== 'function') return () => {};
    const original = server.emit.bind(server);
    server.emit = function guardedEmit(event, ...args) {
      if (event === 'request') {
        const req = args[0];
        const res = args[1];
        void check(req).then((result) => {
          if (result.ok) {
            if ('claims' in result && result.claims)
              req.__aurigaIdentity = result.claims;
            original(event, ...args);
            return;
          }
          sendProblem(res, 401, result.code);
        });
        return false;
      }
      if (event === 'upgrade') {
        const req = args[0];
        const socket = args[1];
        void check(req).then((result) => {
          if (result.ok) {
            if ('claims' in result && result.claims)
              req.__aurigaIdentity = result.claims;
            original(event, ...args);
            return;
          }
          try {
            socket.write(
              'HTTP/1.1 401 Unauthorized\r\nContent-Type: application/problem+json\r\nConnection: close\r\n\r\n' +
                JSON.stringify({
                  type: 'about:blank',
                  title: 'Unauthorized',
                  status: 401,
                  code: result.code,
                }),
            );
          } catch {
            /* ignore */
          }
          socket.destroy();
        });
        return false;
      }
      return original(event, ...args);
    };
    return () => {
      server.emit = original;
    };
  }

  return { check, install, replay };
}
