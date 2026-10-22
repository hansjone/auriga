// @ts-nocheck
/**
 * Let DSH's native UI/RPC use a request already admitted by the managed door,
 * without dropping the browser fence the native check also carries.
 *
 * The node ingress rewrites Host to the instance upstream (an IP literal or
 * loopback), so DSH's own `Origin === Host` comparison would reject every
 * legitimate browser Origin (the edge instance host). For door-admitted
 * requests the fence is therefore applied here against the edge authorities the
 * agent rendered into `browserOrigin`; only the native *token* check is replaced
 * by the door's signed identity.
 */

import { isIP } from 'node:net';
import { domainToASCII } from 'node:url';

function canonicalHost(host, local = false) {
  if (typeof host !== 'string') return false;
  if (isIP(host) === 4) return true;
  if (local && host.startsWith('[') && host.endsWith(']'))
    return isIP(host.slice(1, -1)) === 6 && host === host.toLowerCase();
  if (local && host === 'localhost') return true;
  return (
    host.length <= 253 &&
    domainToASCII(host) === host &&
    /^(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z](?:[a-z0-9-]{0,61}[a-z0-9])?$/.test(
      host,
    )
  );
}

/** Validate deployment Origin semantics without URL parser normalization. */
export function validateBrowserOrigin(value) {
  if (
    !value ||
    typeof value !== 'object' ||
    Array.isArray(value) ||
    Object.keys(value).sort().join(',') !== 'host,port,scheme' ||
    !['http', 'https'].includes(value.scheme) ||
    !canonicalHost(value.host) ||
    !Number.isInteger(value.port) ||
    value.port < 1 ||
    value.port > 65535
  )
    throw new Error(
      'auriga-dsh: config.browserOrigin requires canonical scheme, host and effective port',
    );
  return Object.freeze({
    scheme: value.scheme,
    host: value.host,
    port: value.port,
  });
}

function parseAuthority(value, defaultPort) {
  if (typeof value !== 'string') return undefined;
  const match = /^(\[[0-9a-f:]+\]|[a-z0-9.-]+)(?::([1-9][0-9]{0,4}))?$/.exec(
    value,
  );
  if (!match || !canonicalHost(match[1], true)) return undefined;
  const port = match[2] ? Number(match[2]) : defaultPort;
  if (port < 1 || port > 65535) return undefined;
  return { host: match[1], port };
}

function parseOrigin(value) {
  if (typeof value !== 'string') return undefined;
  const match = /^(https?):\/\/([^/?#\\@]+)$/.exec(value);
  if (!match) return undefined;
  const authority = parseAuthority(match[2], match[1] === 'http' ? 80 : 443);
  return authority && { scheme: match[1], ...authority };
}

function singleHeader(req, name) {
  const value = req?.headers?.[name];
  if (value !== undefined && typeof value !== 'string') return false;
  // Node may discard duplicate singleton headers; inspect the original list too.
  if (Array.isArray(req?.rawHeaders)) {
    let count = 0;
    for (let i = 0; i < req.rawHeaders.length; i += 2)
      if (String(req.rawHeaders[i]).toLowerCase() === name) count++;
    if (count > 1) return false;
  }
  return value;
}

/** Door admission replaces native token auth, while browser Origin stays exact. */
export function browserFenceOk(req, browserOrigin) {
  const expected = validateBrowserOrigin(browserOrigin);
  const host = parseAuthority(
    singleHeader(req, 'host'),
    expected.scheme === 'http' ? 80 : 443,
  );
  if (!host) return false;
  const local =
    host.host === 'localhost' ||
    isIP(host.host) === 4 ||
    (host.host.startsWith('[') && isIP(host.host.slice(1, -1)) === 6);
  if (!local && (host.host !== expected.host || host.port !== expected.port))
    return false;
  const site = singleHeader(req, 'sec-fetch-site');
  if (
    site !== undefined &&
    !['same-origin', 'same-site', 'none'].includes(site)
  )
    return false;
  const value = singleHeader(req, 'origin');
  // Signed navigation/server-to-server requests may omit Origin, as before.
  if (value === undefined) return true;
  const origin = parseOrigin(value);
  return (
    !!origin &&
    origin.scheme === expected.scheme &&
    origin.host === expected.host &&
    origin.port === expected.port
  );
}

/**
 * @param {object} connection DSH `connection` service
 * @param {{ isAuthenticated: (req: object) => boolean }} door
 * @param {{ browserOrigin: import('../generated/managed-config.js').ManagedConfig['browserOrigin'] }} opts
 */
export function installHostAuthentication(connection, door, opts) {
  const browserOrigin = validateBrowserOrigin(opts?.browserOrigin);
  if (!connection) return () => {};
  const restores = [];
  const wrap = (name, wrapped) => {
    if (typeof connection[name] !== 'function') return;
    const original = connection[name];
    const fn = function (req, ...args) {
      return wrapped.call(this, original, req, ...args);
    };
    connection[name] = fn;
    restores.push(() => {
      if (connection[name] === fn) connection[name] = original;
    });
  };
  wrap('requestRejection', function (original, req, ...args) {
    if (!door.isAuthenticated(req)) return original.call(this, req, ...args);
    return browserFenceOk(req, browserOrigin) ? undefined : 403;
  });
  wrap('authorizeIndex', function (original, req, res, ...args) {
    if (!door.isAuthenticated(req))
      return original.call(this, req, res, ...args);
    if (browserFenceOk(req, browserOrigin)) return true;
    try {
      res.writeHead(403, { 'cache-control': 'no-store' });
      res.end('forbidden');
    } catch {
      /* ignore */
    }
    return false;
  });
  return () => restores.reverse().forEach((restore) => restore());
}
