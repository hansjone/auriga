import { isIP } from 'node:net';
import { lookup as dnsLookup } from 'node:dns/promises';
import { Agent, fetch as undiciFetch } from 'undici';

export const MAX_BODY = 1_048_576;

const LOCAL_HOSTS = new Set(['localhost', '127.0.0.1', '::1', '[::1]']);

/** @param {string|null|undefined} host */
export function localHost(host) {
  return host != null && LOCAL_HOSTS.has(host.toLowerCase());
}

/** @param {string} ip */
export function publicAddress(ip) {
  if (!isIP(ip)) return false;
  if (ip.includes(':')) {
    const normalized = ip.toLowerCase();
    if (
      normalized === '::1' ||
      normalized.startsWith('fc') ||
      normalized.startsWith('fd') ||
      normalized.startsWith('fe80') ||
      normalized.startsWith('ff')
    )
      return false;
    const first = parseInt(normalized.split(':')[0] || '0', 16);
    if ((first & 0xe000) !== 0x2000) return false;
    if (first === 0x2001 && normalized.startsWith('2001:db8')) return false;
    if (first === 0x2002) return false;
    return true;
  }
  const parts = ip.split('.').map(Number);
  if (
    parts.length !== 4 ||
    parts.some((n) => !Number.isInteger(n) || n < 0 || n > 255)
  )
    return false;
  const [a, b] = parts;
  return !(
    a === 0 ||
    a === 10 ||
    a === 127 ||
    a >= 224 ||
    (a === 100 && b >= 64 && b <= 127) ||
    (a === 169 && b === 254) ||
    (a === 172 && b >= 16 && b <= 31) ||
    (a === 192 && (b === 0 || b === 168)) ||
    (a === 198 && (b === 18 || b === 19))
  );
}

/** @param {string} address */
function isLoopbackIp(address) {
  return address === '127.0.0.1' || address === '::1' || localHost(address);
}

/**
 * Validate resolved addresses; throw if any are prohibited.
 * @param {string} host
 * @param {string[]} addresses
 * @param {boolean} allowLocalHttp
 */
export function assertAddressesAllowed(host, addresses, allowLocalHttp) {
  if (!addresses.length) throw new Error('OIDC target address is prohibited');
  for (const address of addresses) {
    if (localHost(host) || isLoopbackIp(host)) {
      if (!allowLocalHttp || !isLoopbackIp(address))
        throw new Error('OIDC target address is prohibited');
    } else if (!publicAddress(address)) {
      throw new Error('OIDC target address is prohibited');
    }
  }
}

/**
 * @param {string} host
 * @param {boolean} allowLocalHttp
 * @param {(hostname: string) => Promise<Array<{ address: string, family: number }>>} [lookupAll]
 */
export async function resolvePublic(
  host,
  allowLocalHttp,
  lookupAll = dnsLookupAll,
) {
  if (isIP(host)) {
    assertAddressesAllowed(host, [host], allowLocalHttp);
    return [host];
  }
  const records = await lookupAll(host);
  const addresses = records.map((r) => r.address);
  assertAddressesAllowed(host, addresses, allowLocalHttp);
  return addresses;
}

/** @param {string} hostname */
async function dnsLookupAll(hostname) {
  return dnsLookup(hostname, { all: true, verbatim: true });
}

/**
 * @param {URL} url
 * @param {boolean} allowLocalHttp
 */
export function validateUrl(url, allowLocalHttp) {
  if (
    !url.hostname ||
    url.username ||
    url.password ||
    url.hash ||
    !(
      url.protocol === 'https:' ||
      (allowLocalHttp && localHost(url.hostname) && url.protocol === 'http:')
    )
  )
    throw Object.assign(new Error('invalid OIDC URL'), {
      code: 'common.validation_failed',
    });
}

/**
 * Stream-read a response body with a hard byte cap.
 * @param {import('undici').Response} response
 * @param {number} max
 */
export async function readBodyLimited(response, max = MAX_BODY) {
  const declared = response.headers.get('content-length');
  if (declared != null) {
    const n = Number(declared);
    if (Number.isFinite(n) && n > max)
      throw new Error('OIDC response too large');
  }
  if (!response.body) return '';
  const reader = response.body.getReader();
  /** @type {Uint8Array[]} */
  const chunks = [];
  let total = 0;
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    if (!value) continue;
    total += value.byteLength;
    if (total > max) {
      await reader.cancel().catch(() => {});
      throw new Error('OIDC response too large');
    }
    chunks.push(value);
  }
  return Buffer.concat(chunks.map((c) => Buffer.from(c))).toString('utf8');
}

/**
 * @typedef {{ statusCode: number, body: string }} HttpResponse
 */

/**
 * @param {{
 *   allowLocalHttp?: boolean,
 *   fetchImpl?: typeof fetch,
 *   lookupAll?: (hostname: string) => Promise<Array<{ address: string, family: number }>>,
 * }} [options]
 */
export function createOidcHttp(options = {}) {
  const allowLocalHttp = options.allowLocalHttp === true;
  const lookupAll = options.lookupAll ?? dnsLookupAll;
  const customFetch = options.fetchImpl;

  /**
   * Lookup used by the undici Agent so the TCP connect uses only validated addresses
   * (no second unbound DNS resolve by fetch).
   * @param {string} hostname
   * @param {import('node:dns').LookupOptions | number | undefined} _opts
   * @param {(err: NodeJS.ErrnoException|null, address: string, family: number) => void} callback
   */
  function pinnedLookup(hostname, _opts, callback) {
    lookupAll(hostname)
      .then((records) => {
        const addresses = records.map((r) => r.address);
        assertAddressesAllowed(hostname, addresses, allowLocalHttp);
        const first = records[0];
        callback(
          null,
          first.address,
          first.family || (first.address.includes(':') ? 6 : 4),
        );
      })
      .catch((err) => {
        const error = /** @type {NodeJS.ErrnoException} */ (
          err instanceof Error ? err : new Error(String(err))
        );
        error.code = error.code ?? 'ENOTFOUND';
        callback(error, '', 4);
      });
  }

  const agent = customFetch
    ? null
    : new Agent({
        connect: { lookup: pinnedLookup },
        connectTimeout: 10_000,
        bodyTimeout: 10_000,
        headersTimeout: 10_000,
      });

  /**
   * @param {string|URL} target
   * @param {string} method
   * @param {string|null} [body]
   * @param {Record<string, string>} [headers]
   * @returns {Promise<HttpResponse>}
   */
  async function send(target, method, body = null, headers = {}) {
    const url =
      typeof target === 'string' ? new URL(target) : new URL(target.href);
    validateUrl(url, allowLocalHttp);

    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), 10_000);
    try {
      /** @type {Record<string, unknown>} */
      const init = {
        method,
        headers: {
          Accept: 'application/json',
          ...(body != null
            ? { 'Content-Type': 'application/x-www-form-urlencoded' }
            : {}),
          ...headers,
        },
        body: body ?? undefined,
        redirect: 'error',
        signal: controller.signal,
      };
      let response;
      try {
        if (customFetch) {
          // Tests may inject fetchImpl; still pre-validate DNS to keep the contract.
          await resolvePublic(url.hostname, allowLocalHttp, lookupAll);
          response = await customFetch(url, /** @type {RequestInit} */ (init));
        } else {
          init.dispatcher = agent;
          response = await undiciFetch(
            url,
            /** @type {import('undici').RequestInit} */ (init),
          );
        }
      } catch (err) {
        /** @type {unknown} */
        let cur = err;
        for (let i = 0; i < 5 && cur; i++) {
          const msg = cur instanceof Error ? cur.message : String(cur);
          if (/prohibited/i.test(msg))
            throw new Error('OIDC target address is prohibited');
          cur =
            cur instanceof Error
              ? /** @type {{ cause?: unknown }} */ (cur).cause
              : null;
        }
        throw err;
      }
      const text = await readBodyLimited(
        /** @type {import('undici').Response} */ (response),
      );
      return { statusCode: response.status, body: text };
    } finally {
      clearTimeout(timer);
    }
  }

  return {
    send,
    allowLocalHttp,
    agent,
    /** @param {string} u */
    validateUrl: (u) => validateUrl(new URL(u), allowLocalHttp),
    /** Expose for tests */
    pinnedLookup,
  };
}
