import { randomToken, same, sha256Hex } from './crypto.js';

const WRITE_METHODS = new Set(['POST', 'PUT', 'PATCH', 'DELETE']);

/**
 * Session cookie + CSRF helpers for standalone mode.
 * Cookie names drop `__Host-` when secure=false (HTTP / IP-only installs).
 *
 * @param {{
 *   secure?: boolean,
 *   sameSite?: 'Strict' | 'Lax' | 'None',
 *   sessionTtlSeconds?: number,
 *   now?: () => Date,
 *   store: {
 *     putSession: (id: string, session: Record<string, unknown>) => void,
 *     getSession: (id: string) => Record<string, unknown>|null,
 *     deleteSession: (id: string) => void,
 *   },
 * }} options
 */
export function createSessionManager(options) {
  const secure = options.secure !== false;
  const sameSite = options.sameSite ?? 'Lax';
  const sessionTtlSeconds = options.sessionTtlSeconds ?? 28_800;
  const now = options.now ?? (() => new Date());
  const sessionCookie = secure ? '__Host-auriga_session' : 'auriga_session';
  const csrfCookie = secure ? '__Host-auriga_csrf' : 'auriga_csrf';

  /**
   * @param {string} name
   * @param {string} value
   * @param {number} maxAge
   */
  function cookieHeader(name, value, maxAge) {
    const parts = [
      `${name}=${value}`,
      'Path=/',
      'HttpOnly',
      `SameSite=${sameSite}`,
      `Max-Age=${maxAge}`,
    ];
    if (secure) parts.push('Secure');
    return parts.join('; ');
  }

  /**
   * @param {{
   *   providerId: string,
   *   subject: string,
   *   displayName?: string,
   *   mfaPerformed?: boolean,
   *   authTime?: string,
   * }} identity
   */
  function issue(identity) {
    const sessionId = randomToken(32);
    const csrf = randomToken(32);
    const issuedAt = now();
    const expiresAt = new Date(issuedAt.getTime() + sessionTtlSeconds * 1000);
    const authTime = identity.authTime ?? issuedAt.toISOString();
    const session = {
      sessionId,
      providerId: identity.providerId,
      subject: identity.subject,
      displayName: identity.displayName ?? identity.subject,
      csrfHash: sha256Hex(csrf),
      issuedAt: issuedAt.toISOString(),
      expiresAt: expiresAt.toISOString(),
      authTime,
      mfaAt: identity.mfaPerformed ? authTime : null,
    };
    options.store.putSession(sessionId, session);
    return {
      session,
      csrf,
      setCookie: [
        cookieHeader(sessionCookie, sessionId, sessionTtlSeconds),
        // CSRF cookie is readable by JS for the double-submit pattern; not HttpOnly.
        [
          `${csrfCookie}=${csrf}`,
          'Path=/',
          `SameSite=${sameSite}`,
          `Max-Age=${sessionTtlSeconds}`,
          ...(secure ? ['Secure'] : []),
        ].join('; '),
      ],
    };
  }

  /** @param {Record<string, string>} cookies */
  function readSession(cookies) {
    const id = cookies[sessionCookie];
    if (!id) return null;
    const session = options.store.getSession(id);
    if (!session) return null;
    if (new Date(String(session.expiresAt)).getTime() <= now().getTime()) {
      options.store.deleteSession(id);
      return null;
    }
    return session;
  }

  /**
   * Validate CSRF on write methods. Safe methods skip.
   * @param {string} method
   * @param {Record<string, string>} cookies
   * @param {Record<string, string|undefined>} headers
   * @returns {{ ok: true, session: Record<string, unknown>|null } | { ok: false, code: string }}
   */
  function checkCsrf(method, cookies, headers) {
    const session = readSession(cookies);
    if (!WRITE_METHODS.has(method.toUpperCase())) return { ok: true, session };
    if (!session) return { ok: false, code: 'common.unauthenticated' };
    const header = headers['x-auriga-csrf'] ?? headers['X-Auriga-CSRF'];
    const cookieVal = cookies[csrfCookie];
    if (!header || !cookieVal || !same(header, cookieVal))
      return { ok: false, code: 'common.forbidden' };
    if (!same(sha256Hex(header), String(session.csrfHash)))
      return { ok: false, code: 'common.forbidden' };
    return { ok: true, session };
  }

  /** @param {Record<string, string>} cookies */
  function destroy(cookies) {
    const id = cookies[sessionCookie];
    if (id) options.store.deleteSession(id);
    return [
      cookieHeader(sessionCookie, '', 0),
      [
        `${csrfCookie}=`,
        'Path=/',
        'Max-Age=0',
        `SameSite=${sameSite}`,
        ...(secure ? ['Secure'] : []),
      ].join('; '),
    ];
  }

  return {
    sessionCookie,
    csrfCookie,
    issue,
    readSession,
    checkCsrf,
    destroy,
  };
}

/**
 * @param {string|undefined} header
 * @returns {Record<string, string>}
 */
export function parseCookieHeader(header) {
  /** @type {Record<string, string>} */
  const out = {};
  if (!header) return out;
  for (const part of header.split(';')) {
    const idx = part.indexOf('=');
    if (idx < 0) continue;
    const name = part.slice(0, idx).trim();
    const value = part.slice(idx + 1).trim();
    if (name) out[name] = value;
  }
  return out;
}
