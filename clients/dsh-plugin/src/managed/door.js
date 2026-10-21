// @ts-nocheck
import { verify as cryptoVerify } from 'node:crypto';
import { sendProblem } from './problem.js';
import { PROTOCOL_VERSION } from './protocol.js';
import { rawRequestPath } from './raw-path.js';

const CLOCK_SKEW_SEC = 5;
const UNAUTH = 'common.unauthenticated';
/** Hard cap on remembered jti values (task requires replay rejection). */
const REPLAY_MAX = 10_000;

export const HEALTH_PATH = '/_auriga/health';

/**
 * In-memory jti replay cache with size cap and expiry eviction.
 * Contract notes jti is for log correlation only; P1 task requires replay rejection.
 */
export function createReplayCache({
  now = () => Math.floor(Date.now() / 1000),
  maxSize = REPLAY_MAX,
} = {}) {
  /** @type {Map<string, number>} jti → exp+skew */
  const seen = new Map();

  const purgeExpired = (t) => {
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
      purgeExpired(t);
      if (seen.has(jti)) return false;
      // Never evict live entries: pressure must not reopen a replay window.
      if (seen.size >= maxSize) return false;
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
 */
export async function verifyIdentityHeader(opts) {
  const now = opts.now ?? (() => Math.floor(Date.now() / 1000));
  const fail = (reason) => ({
    ok: /** @type {const} */ (false),
    code: UNAUTH,
    reason,
  });

  try {
    if (!opts.token) return fail('missing');

    const parsed = parseCompactJws(opts.token);
    if (!parsed) return fail('malformed');

    const pub = await opts.getPublicKey(parsed.header.kid);
    if (!pub) return fail('kid_unknown');

    let ok = false;
    try {
      ok = cryptoVerify(
        null,
        Buffer.from(parsed.signingInput),
        pub,
        parsed.sig,
      );
    } catch {
      ok = false;
    }
    if (!ok) return fail('signature');

    let claims;
    try {
      claims = JSON.parse(
        Buffer.from(parsed.payloadB64, 'base64url').toString('utf8'),
      );
    } catch {
      return fail('malformed');
    }
    if (!claims || typeof claims !== 'object') return fail('malformed');

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
  } catch {
    return fail('error');
  }
}

/**
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
  // Only this door can admit a request; headers or request properties cannot.
  const authenticated = new WeakSet();
  /** @type {undefined | (() => void)} */
  let onAuthenticated = cfg.onAuthenticated;

  /**
   * @param {{ method?: string, url?: string, headers?: Record<string, string | string[] | undefined> }} req
   */
  async function check(req, upgrade = false) {
    try {
      const path = rawRequestPath(req.url);
      const method = String(req.method || 'GET').toUpperCase();
      if (
        !upgrade &&
        path === HEALTH_PATH &&
        (method === 'GET' || method === 'HEAD')
      ) {
        return { ok: /** @type {const} */ (true), bypass: true };
      }

      const raw =
        req.headers?.['auriga-identity'] ?? req.headers?.['Auriga-Identity'];
      const token = Array.isArray(raw) ? raw[0] : raw;
      return await verifyIdentityHeader({
        token,
        method,
        path,
        instanceId: cfg.instanceId,
        ownerId: cfg.ownerId,
        getPublicKey: cfg.getPublicKey,
        replay,
        now: cfg.now,
      });
    } catch {
      return {
        ok: /** @type {const} */ (false),
        code: UNAUTH,
        reason: 'error',
      };
    }
  }

  /**
   * Mark a request the door let through. The health bypass is admitted too
   * (so a native fence never turns readiness into 401) but is not counted as
   * an interaction; it can only reach the plugin's exact health route.
   */
  function admit(req, result) {
    if ('claims' in result && result.claims)
      req.__aurigaIdentity = result.claims;
    authenticated.add(req);
    if (result.bypass) return;
    try {
      onAuthenticated?.();
    } catch {
      /* ignore */
    }
  }

  /**
   * @param {import('node:http').Server} server
   */
  function install(server) {
    if (!server || typeof server.emit !== 'function') {
      throw new Error(
        'auriga-dsh: door lock requires http.Server (ctx.webServer.server)',
      );
    }
    const original = server.emit.bind(server);
    server.emit = function guardedEmit(event, ...args) {
      if (event === 'request') {
        const req = args[0];
        const res = args[1];
        void check(req)
          .then((result) => {
            if (result.ok) {
              admit(req, result);
              original(event, ...args);
              return;
            }
            sendProblem(res, 401, result.code || UNAUTH);
          })
          .catch(() => {
            try {
              sendProblem(res, 401, UNAUTH);
            } catch {
              /* ignore */
            }
          });
        return false;
      }
      if (event === 'upgrade') {
        const req = args[0];
        const socket = args[1];
        void check(req, true)
          .then((result) => {
            if (result.ok) {
              admit(req, result);
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
                    code: result.code || UNAUTH,
                  }),
              );
            } catch {
              /* ignore */
            }
            socket.destroy();
          })
          .catch(() => {
            try {
              socket.destroy();
            } catch {
              /* ignore */
            }
          });
        return false;
      }
      if (event === 'checkContinue' || event === 'checkExpectation') {
        // Only reachable when a host listener exists; never let it bypass the door.
        const res = args[1];
        try {
          res.writeHead(417, { connection: 'close' });
          res.end();
        } catch {
          /* ignore */
        }
        return false;
      }
      if (event === 'connect') {
        try {
          args[1]?.destroy?.();
        } catch {
          /* ignore */
        }
        return false;
      }
      return original(event, ...args);
    };
    return () => {
      server.emit = original;
    };
  }

  return {
    check,
    install,
    isAuthenticated: (req) => authenticated.has(req),
    replay,
    /** @param {undefined | (() => void)} fn */
    setOnAuthenticated(fn) {
      onAuthenticated = fn;
    },
  };
}
