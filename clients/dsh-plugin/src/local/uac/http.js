/* global AbortSignal */
import { Buffer } from 'node:buffer';
import { lookup as dnsLookup } from 'node:dns/promises';
import { requestPinnedEnterpriseHttps } from '../enterprise.js';

export { MAX_ENTERPRISE_BODY as MAX_BODY } from '../enterprise.js';

/** @param {URL} url */
export function originKey(url) {
  const port = url.port || (url.protocol === 'https:' ? '443' : '80');
  return url.protocol + '//' + url.hostname.toLowerCase() + ':' + port;
}

/** Canonical numeric address text; DNS names are rejected.
 * @param {unknown} value */
export function parseIpAddress(value) {
  if (
    typeof value !== 'string' ||
    !value ||
    value.length > 64 ||
    /[^0-9a-fA-F:.]/.test(value)
  )
    return null;
  if (value.includes(':')) {
    try {
      return {
        address: new URL('https://[' + value + ']').hostname.slice(1, -1),
        family: 6,
      };
    } catch {
      return null;
    }
  }
  const parts = value.split('.');
  if (
    parts.length !== 4 ||
    !parts.every(
      (part) => /^(?:0|[1-9][0-9]{0,2})$/.test(part) && Number(part) <= 255,
    )
  )
    return null;
  return { address: value, family: 4 };
}

/** @param {unknown} value */
function literalAddress(value) {
  const ip = parseIpAddress(value);
  if (!ip) throw new Error('invalid UAC address allowlist');
  return ip.address;
}

/**
 * Private destinations are checked against both the configured origin and the
 * actually resolved socket address. No redirect, retry, cookie jar or proxy.
 * @param {any} settings private adapter settings
 * @param {{lookup?:(host:string)=>Promise<{address:string,family:number}[]>}} [options]
 */
export function createUacHttp(settings, options = {}) {
  /** @type {Map<string,Set<string>>} */
  const destinations = new Map();
  const entries = settings?.allowedDestinations;
  if (!Array.isArray(entries) || !entries.length || entries.length > 16)
    throw new Error('invalid UAC destination allowlist');
  for (const entry of entries) {
    if (!entry || typeof entry.origin !== 'string')
      throw new Error('invalid UAC destination allowlist');
    const origin = new URL(entry.origin);
    const key = originKey(origin);
    if (
      origin.protocol !== 'https:' ||
      origin.username ||
      origin.password ||
      origin.pathname !== '/' ||
      origin.search ||
      origin.hash ||
      destinations.has(key) ||
      !Array.isArray(entry.addresses) ||
      !entry.addresses.length ||
      entry.addresses.length > 32
    )
      throw new Error('invalid UAC destination allowlist');
    destinations.set(key, new Set(entry.addresses.map(literalAddress)));
  }
  const resolve =
    options.lookup ??
    ((/** @type {string} */ host) => dnsLookup(host, { all: true }));

  /** @param {URL} url @param {boolean} [base] */
  function validate(url, base = false) {
    if (
      url.protocol !== 'https:' ||
      url.username ||
      url.password ||
      url.hash ||
      (base && url.search) ||
      !destinations.has(originKey(url))
    )
      throw new Error('UAC destination rejected');
  }

  /**
   * @param {URL} url @param {unknown} body
   * @param {Record<string,string>} headers @param {number} seconds
   * @returns {Promise<{status:number,body:Buffer}>}
   */
  async function post(url, body, headers, seconds) {
    url = new URL(url);
    validate(url);
    const signal = AbortSignal.timeout(seconds * 1000);
    const allowed = /** @type {Set<string>} */ (
      destinations.get(originKey(url))
    );
    const host = url.hostname.replace(/^\[|\]$/g, '');
    const literal = parseIpAddress(host);
    /** @type {{address:string,family:number}[]} */
    const resolved = literal
      ? [literal]
      : await new Promise((done, fail) => {
          const cancelled = () => fail(signal.reason);
          signal.addEventListener('abort', cancelled, { once: true });
          void resolve(host)
            .then(done, fail)
            .finally(() => signal.removeEventListener('abort', cancelled));
        });
    const checked = resolved.map(({ address, family }) => {
      const ip = parseIpAddress(address);
      if (!ip || ip.family !== family || !allowed.has(ip.address))
        throw new Error('UAC destination rejected');
      return ip;
    });
    const pinned = checked[0];
    if (!pinned) throw new Error('UAC destination rejected');
    const payload = Buffer.from(JSON.stringify(body), 'utf8');
    const response = await requestPinnedEnterpriseHttps(url, pinned, {
      method: 'POST',
      signal,
      check: () => validate(url),
      body: payload,
      headers: {
        ...headers,
        'content-type': 'application/json;charset=UTF-8',
        'content-length': String(payload.length),
      },
    });
    return { status: response.statusCode ?? 0, body: response.body };
  }
  return { validate, post };
}
