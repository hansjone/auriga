/* global AbortSignal */
import { Buffer } from 'node:buffer';
import { isIP } from 'node:net';
import { lookup as dnsLookup } from 'node:dns/promises';
import { request as httpsRequest } from 'node:https';
import { request as httpRequest } from 'node:http';

export const MAX_BODY = 2 * 1024 * 1024;

/** @param {URL} url */
export function originKey(url) {
  const port = url.port || (url.protocol === 'https:' ? '443' : '80');
  return url.protocol + '//' + url.hostname.toLowerCase() + ':' + port;
}

/** Canonical numeric address text; DNS names are rejected.
 * @param {unknown} value */
function literalAddress(value) {
  if (typeof value !== 'string' || !/^[0-9a-fA-F:.]+$/.test(value))
    throw new Error('invalid UAC address allowlist');
  const family = isIP(value);
  if (!family) throw new Error('invalid UAC address allowlist');
  return family === 6 ? new URL('http://[' + value + ']').hostname : value;
}

const loopbackHost = (/** @type {string} */ host) =>
  host === 'localhost' || host === '127.0.0.1' || host === '[::1]';

/**
 * Private destinations are checked against both the configured origin and the
 * actually resolved socket address. No redirect, retry, cookie jar or proxy.
 * `testLoopback` exists only for in-process replicas and permits plain HTTP to
 * loopback origins that are still listed in allowedDestinations.
 * @param {any} settings private adapter settings
 * @param {{lookup?:(host:string)=>Promise<{address:string,family:number}[]>,testLoopback?:boolean}} [options]
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
    const local =
      options.testLoopback === true &&
      origin.protocol === 'http:' &&
      loopbackHost(origin.hostname);
    const key = originKey(origin);
    if (
      !(origin.protocol === 'https:' || local) ||
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
    const local =
      options.testLoopback === true &&
      url.protocol === 'http:' &&
      loopbackHost(url.hostname);
    if (
      !(url.protocol === 'https:' || local) ||
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
    validate(url);
    const signal = AbortSignal.timeout(seconds * 1000);
    const allowed = /** @type {Set<string>} */ (
      destinations.get(originKey(url))
    );
    const host = url.hostname.replace(/^\[|\]$/g, '');
    /** @type {{address:string,family:number}[]} */
    const resolved = isIP(host)
      ? [{ address: host, family: isIP(host) }]
      : await new Promise((done, fail) => {
          const cancelled = () => fail(signal.reason);
          signal.addEventListener('abort', cancelled, { once: true });
          void resolve(host)
            .then(done, fail)
            .finally(() => signal.removeEventListener('abort', cancelled));
        });
    if (
      !resolved.length ||
      resolved.some((a) => !allowed.has(literalAddress(a.address)))
    )
      throw new Error('UAC destination rejected');
    const pinned = resolved[0];
    const payload = Buffer.from(JSON.stringify(body), 'utf8');
    const send = url.protocol === 'https:' ? httpsRequest : httpRequest;
    return await new Promise((done, fail) => {
      const req = send(
        url,
        {
          method: 'POST',
          agent: false,
          rejectUnauthorized: true,
          signal,
          headers: {
            ...headers,
            'content-type': 'application/json;charset=UTF-8',
            'content-length': String(payload.length),
          },
          lookup: (_hostname, lookupOptions, callback) => {
            if (/** @type {any} */ (lookupOptions).all)
              callback(null, [
                { address: pinned.address, family: pinned.family },
              ]);
            else callback(null, pinned.address, pinned.family);
          },
        },
        (res) => {
          /** @type {Buffer[]} */ const chunks = [];
          let size = 0;
          res.on('data', (chunk) => {
            size += chunk.length;
            if (size > MAX_BODY) res.destroy(new Error('UAC response limit'));
            else chunks.push(chunk);
          });
          res.on('error', fail);
          res.on('end', () =>
            done({ status: res.statusCode ?? 0, body: Buffer.concat(chunks) }),
          );
        },
      );
      req.on('error', fail);
      req.end(payload);
    });
  }
  return { validate, post };
}
