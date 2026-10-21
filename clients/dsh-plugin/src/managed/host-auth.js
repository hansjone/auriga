// @ts-nocheck
/**
 * Let DSH's native UI/RPC use a request already admitted by the managed door,
 * without dropping the browser fence the native check also carries.
 *
 * The node ingress rewrites Host to the instance upstream (an IP literal or
 * loopback), so DSH's own `Origin === Host` comparison would reject every
 * legitimate browser Origin (the edge instance host). For door-admitted
 * requests the fence is therefore applied here against the edge authorities the
 * agent rendered into `trustedHosts`; only the native *token* check is replaced
 * by the door's signed identity.
 */

function header(req, name) {
  const value = req?.headers?.[name];
  return typeof value === 'string' ? value : undefined;
}

function parseAuthority(value) {
  try {
    const url = new URL(`http://${value}`);
    if (url.username || url.password || url.pathname !== '/' || url.search)
      return undefined;
    return url;
  } catch {
    return undefined;
  }
}

function isLoopback(hostname) {
  if (hostname === 'localhost' || hostname === '[::1]') return true;
  const parts = hostname.split('.');
  return parts.length === 4 && parts[0] === '127';
}

function isIpLiteral(hostname) {
  return (
    /^\d{1,3}(\.\d{1,3}){3}$/.test(hostname) ||
    (hostname.startsWith('[') && hostname.endsWith(']'))
  );
}

/** Same semantics as DSH: a port-less entry matches the hostname on any port. */
function matchesTrusted(url, trustedHosts) {
  return trustedHosts.some((entry) => {
    const parsed = parseAuthority(entry);
    if (!parsed) return false;
    return /:\d+$/.test(entry)
      ? parsed.host === url.host
      : parsed.hostname === url.hostname;
  });
}

/**
 * @param {{ headers?: Record<string, unknown> }} req
 * @param {string[]} trustedHosts
 * @returns {boolean} true when Host is ours and browser markers are same-site
 */
export function browserFenceOk(req, trustedHosts) {
  const hostValue = header(req, 'host');
  if (hostValue === undefined) return false;
  const host = parseAuthority(hostValue);
  if (!host) return false;
  // IP literals and loopback cannot be rebound; names must be declared.
  if (
    !isLoopback(host.hostname) &&
    !isIpLiteral(host.hostname) &&
    !matchesTrusted(host, trustedHosts)
  )
    return false;
  if (header(req, 'sec-fetch-site') === 'cross-site') return false;
  const origin = header(req, 'origin');
  if (origin === undefined) return true;
  let originUrl;
  try {
    originUrl = new URL(origin);
  } catch {
    return false;
  }
  if (originUrl.protocol !== 'http:' && originUrl.protocol !== 'https:')
    return false;
  return (
    originUrl.host === host.host || matchesTrusted(originUrl, trustedHosts)
  );
}

/**
 * @param {object} connection DSH `connection` service
 * @param {{ isAuthenticated: (req: object) => boolean }} door
 * @param {{ trustedHosts?: string[] }} [opts]
 */
export function installHostAuthentication(connection, door, opts = {}) {
  if (!connection) return () => {};
  const trustedHosts = Array.isArray(opts.trustedHosts)
    ? opts.trustedHosts.filter((entry) => typeof entry === 'string')
    : [];
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
    return browserFenceOk(req, trustedHosts) ? undefined : 403;
  });
  wrap('authorizeIndex', function (original, req, res, ...args) {
    if (!door.isAuthenticated(req))
      return original.call(this, req, res, ...args);
    if (browserFenceOk(req, trustedHosts)) return true;
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
