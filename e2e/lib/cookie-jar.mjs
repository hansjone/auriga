/**
 * Minimal RFC6265-ish cookie jar for e2e: host/path/Domain/Max-Age/Expires.
 * Audience jars (admin / portal / instance) stay separate; Domain cookies can
 * be mirrored into any jar whose request host matches.
 */

function defaultPath(reqPath) {
  if (!reqPath || !reqPath.startsWith('/')) return '/';
  const i = reqPath.lastIndexOf('/');
  if (i <= 0) return '/';
  return reqPath.slice(0, i) || '/';
}

function pathMatches(cookiePath, reqPath) {
  const cp = cookiePath || '/';
  const rp = reqPath || '/';
  if (rp === cp) return true;
  if (!rp.startsWith(cp)) return false;
  if (cp.endsWith('/')) return true;
  return rp.charAt(cp.length) === '/';
}

/** Domain-attribute match (leading dot optional); host-only needs exact host. */
export function domainMatches(cookieDomain, host, hostOnly) {
  const h = String(host || '').toLowerCase();
  if (!h) return false;
  if (hostOnly) return h === String(cookieDomain || '').toLowerCase();
  let d = String(cookieDomain || '')
    .toLowerCase()
    .replace(/^\./, '');
  if (!d) return false;
  if (h === d) return true;
  return h.endsWith('.' + d);
}

function parseAttrDate(v) {
  const t = Date.parse(v);
  return Number.isFinite(t) ? t : null;
}

/**
 * @typedef {{
 *   name: string,
 *   value: string,
 *   domain: string,
 *   path: string,
 *   hostOnly: boolean,
 *   secure: boolean,
 *   httpOnly: boolean,
 *   expiresAt: number | null,
 *   creationTime: number,
 * }} Cookie
 */

export class CookieJar {
  constructor() {
    /** @type {Cookie[]} */
    this.cookies = [];
  }

  clear() {
    this.cookies = [];
  }

  /** Drop cookies whose name matches (any domain/path). */
  deleteByName(name) {
    this.cookies = this.cookies.filter((c) => c.name !== name);
  }

  /**
   * @param {string | string[] | undefined} setCookie
   * @param {{ host: string, path?: string, secure?: boolean }} req
   * @param {number} [now]
   */
  storeFromResponse(setCookie, req, now = Date.now()) {
    const raw = Array.isArray(setCookie)
      ? setCookie
      : setCookie
        ? [setCookie]
        : [];
    for (const line of raw) {
      const c = parseSetCookieLine(String(line), req, now);
      if (!c) continue;
      this.#upsert(c, now);
    }
  }

  /**
   * Copy Domain-scoped cookies that match `host` from another jar (e.g. open nonce).
   * @param {CookieJar} other
   * @param {string} host
   * @param {number} [now]
   */
  absorbMatchingDomainCookies(other, host, now = Date.now()) {
    for (const c of other.cookies) {
      if (c.hostOnly) continue;
      if (!domainMatches(c.domain, host, false)) continue;
      if (c.expiresAt !== null && c.expiresAt <= now) continue;
      this.#upsert({ ...c });
    }
  }

  /**
   * @param {{ host: string, path?: string, secure?: boolean }} req
   * @param {number} [now]
   */
  cookieHeader(req, now = Date.now()) {
    this.#expire(now);
    const host = String(req.host || '').toLowerCase();
    const path = req.path || '/';
    const secure = !!req.secure;
    const matched = this.cookies.filter((c) => {
      if (c.expiresAt !== null && c.expiresAt <= now) return false;
      if (c.secure && !secure) return false;
      if (!domainMatches(c.domain, host, c.hostOnly)) return false;
      if (!pathMatches(c.path, path)) return false;
      return true;
    });
    // Longer path first, then earlier creation (stable enough for e2e).
    matched.sort((a, b) => {
      const pl = b.path.length - a.path.length;
      if (pl !== 0) return pl;
      return a.creationTime - b.creationTime;
    });
    return matched.map((c) => `${c.name}=${c.value}`).join('; ');
  }

  /** @returns {string[]} */
  names() {
    return this.cookies.map((c) => c.name);
  }

  /**
   * Whether a named cookie would be sent to host/path.
   * @param {string} name
   * @param {{ host: string, path?: string, secure?: boolean }} req
   * @param {number} [now]
   */
  wouldSend(name, req, now = Date.now()) {
    const header = this.cookieHeader(req, now);
    if (!header) return false;
    return header.split('; ').some((p) => p.startsWith(name + '='));
  }

  /** @param {Cookie} c */
  #upsert(c, now = Date.now()) {
    this.cookies = this.cookies.filter(
      (x) =>
        !(
          x.name === c.name &&
          x.domain === c.domain &&
          x.path === c.path &&
          x.hostOnly === c.hostOnly
        ),
    );
    if (c.expiresAt !== null && c.expiresAt <= now) return;
    this.cookies.push(c);
  }

  #expire(now) {
    this.cookies = this.cookies.filter(
      (c) => c.expiresAt === null || c.expiresAt > now,
    );
  }
}

/**
 * @param {string} line
 * @param {{ host: string, path?: string, secure?: boolean }} req
 * @param {number} now
 * @returns {Cookie | null}
 */
export function parseSetCookieLine(line, req, now) {
  const parts = String(line)
    .split(';')
    .map((s) => s.trim());
  if (!parts[0]) return null;
  const eq = parts[0].indexOf('=');
  if (eq <= 0) return null;
  const name = parts[0].slice(0, eq).trim();
  const value = parts[0].slice(eq + 1).trim();
  if (!name) return null;

  let domain = String(req.host || '').toLowerCase();
  let hostOnly = true;
  let path = defaultPath(req.path || '/');
  let secure = false;
  let httpOnly = false;
  /** @type {number | null} */
  let expiresAt = null;
  let maxAgeSeen = false;
  let rootPathSeen = false;

  for (let i = 1; i < parts.length; i++) {
    const p = parts[i];
    const j = p.indexOf('=');
    const k = (j < 0 ? p : p.slice(0, j)).trim().toLowerCase();
    const v = j < 0 ? '' : p.slice(j + 1).trim();
    if (k === 'domain' && v) {
      domain = v.replace(/^\./, '').toLowerCase();
      hostOnly = false;
    } else if (k === 'path' && v) {
      path = v.startsWith('/') ? v : '/' + v;
      rootPathSeen = v === '/';
    } else if (k === 'secure') {
      secure = true;
    } else if (k === 'httponly') {
      httpOnly = true;
    } else if (k === 'max-age') {
      if (!/^-?\d+$/.test(v)) continue;
      const n = Number(v);
      if (!Number.isSafeInteger(n)) continue;
      maxAgeSeen = true;
      expiresAt = n <= 0 ? now : now + n * 1000;
    } else if (k === 'expires' && !maxAgeSeen) {
      const t = parseAttrDate(v);
      if (t !== null) expiresAt = t;
    }
  }

  // Reject Domain that does not cover the request host.
  if (
    !hostOnly &&
    (!domain.includes('.') || !domainMatches(domain, req.host, false))
  ) {
    return null;
  }
  if (secure && !req.secure) return null;
  if (name.startsWith('__Host-') && (!secure || !hostOnly || !rootPathSeen))
    return null;
  if (name.startsWith('__Secure-') && !secure) return null;

  return {
    name,
    value,
    domain,
    path,
    hostOnly,
    secure,
    httpOnly,
    expiresAt,
    creationTime: now,
  };
}

/**
 * Pick which audience jar to use for a virtual Host.
 * @param {string} host
 * @param {{ admin: string, portal: string, instanceParent?: string }} hosts
 * @param {{ admin: CookieJar, portal: CookieJar, instance: CookieJar }} jars
 */
export function jarForHost(host, hosts, jars) {
  const h = String(host || '').toLowerCase();
  if (h === String(hosts.admin).toLowerCase()) return jars.admin;
  if (h === String(hosts.portal).toLowerCase()) return jars.portal;
  return jars.instance;
}
