/**
 * Request path helpers for identity htu / health bypass.
 * Contract: use the raw path (no percent-decode, no `.`/`..` collapse).
 */

/**
 * Strip query/hash from `req.url`, keep the raw path string.
 * @param {string|undefined|null} url
 */
export function rawRequestPath(url) {
  const s = typeof url === 'string' ? url : '/';
  const end = (() => {
    const q = s.indexOf('?');
    const h = s.indexOf('#');
    if (q < 0 && h < 0) return s.length;
    if (q < 0) return h;
    if (h < 0) return q;
    return Math.min(q, h);
  })();
  const path = s.slice(0, end);
  return path.length ? path : '/';
}

/**
 * Normalize an HTTP path for plugin-manager guard matching only
 * (decode, collapse `.`/`..`, strip trailing slash, lower-case).
 * @param {string|undefined|null} urlPath
 */
export function normalizeHttpPath(urlPath) {
  let path = String(urlPath || '').split(/[?#]/)[0] || '/';
  path = path.replace(/\\/g, '/');
  // Collapse duplicate slashes
  path = path.replace(/\/{2,}/g, '/');
  try {
    path = decodeURIComponent(path);
  } catch {
    /* keep undecoded */
  }
  const parts = [];
  for (const seg of path.split('/')) {
    if (!seg || seg === '.') continue;
    if (seg === '..') {
      parts.pop();
      continue;
    }
    parts.push(seg);
  }
  let out = '/' + parts.join('/');
  if (out.length > 1 && out.endsWith('/')) out = out.slice(0, -1);
  return out.toLowerCase();
}
