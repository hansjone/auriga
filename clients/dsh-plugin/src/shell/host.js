import { readFile } from 'node:fs/promises';
import { validateBrowserOrigin } from '../managed/host-auth.js';

const ASSETS = new Map([
  ['/auriga/shell-ui/overview.js', './overview.js'],
  ['/auriga/shell-ui/icons.js', './icons.js'],
]);

/** @param {string|undefined} value */
function trustedOrigin(value) {
  if (value === undefined) return undefined;
  const url = new URL(value);
  if (!['http:', 'https:'].includes(url.protocol) || url.origin !== value)
    throw new Error(
      'auriga-dsh: trusted UI origin must be a canonical HTTP(S) origin',
    );
  return url.origin;
}

/**
 * Serve overview UI modules with the same native auth and same-origin gate as settings.
 * @param {any} ctx
 * @param {any} [config]
 * @param {{uiOrigin?:string,nativeAuth?:{requestRejection:(req:any)=>number|undefined}}} [installation]
 */
export function installShellUi(
  ctx,
  config,
  { uiOrigin = process.env.AURIGA_SETTINGS_UI_ORIGIN, nativeAuth } = {},
) {
  if (typeof ctx.inject !== 'function' || !ctx.webServer?.register) return;
  const browser =
    config?.browserOrigin && validateBrowserOrigin(config.browserOrigin);
  const expectedOrigin = browser
    ? new URL(`${browser.scheme}://${browser.host}:${browser.port}`).origin
    : trustedOrigin(uiOrigin);
  ctx.inject(['profileContext'], async (/** @type {any} */ scoped) => {
    if (!scoped.profileContext?.dir) return;
    const auth = nativeAuth ?? scoped.connection;
    scoped.effect(
      () =>
        scoped.webServer.register({
          kind: 'prefix',
          path: '/auriga/shell-ui',
          handler: createShellUiHandler({ auth, uiOrigin: expectedOrigin }),
        }),
      'auriga-dsh: shell-ui',
    );
  });
}

/** @param {{auth:any,uiOrigin?:string}} options */
export function createShellUiHandler({ auth, uiOrigin }) {
  const expectedOrigin = trustedOrigin(uiOrigin);
  return async (
    /** @type {import('node:http').IncomingMessage} */ req,
    /** @type {import('node:http').ServerResponse} */ res,
  ) => {
    const reject = (
      /** @type {number} */ code,
      /** @type {string} */ reason,
    ) => {
      res.writeHead(code, {
        'content-type': 'application/json',
        'cache-control': 'no-store',
        'x-content-type-options': 'nosniff',
      });
      res.end(JSON.stringify({ reason }));
    };
    if (typeof auth?.requestRejection !== 'function')
      return reject(503, 'unavailable');
    const rejected = auth.requestRejection(req);
    if (rejected !== undefined) return reject(rejected, 'auth');
    if (req.method !== 'GET' && req.method !== 'HEAD')
      return reject(405, 'method');
    const count = (/** @type {string} */ key) =>
      req.rawHeaders.filter(
        (_, i) => i % 2 === 0 && req.rawHeaders[i].toLowerCase() === key,
      ).length;
    let sameOrigin = false;
    try {
      const direct = new URL('http://' + req.headers.host);
      sameOrigin =
        count('host') === 1 &&
        count('origin') === 1 &&
        !direct.username &&
        !direct.password &&
        !direct.search &&
        !direct.hash &&
        direct.pathname === '/' &&
        req.headers.origin === (expectedOrigin ?? direct.origin);
    } catch {
      /* invalid host rejected */
    }
    if (
      (req.headers['sec-fetch-site'] &&
        !['same-origin', 'none'].includes(req.headers['sec-fetch-site'])) ||
      (req.headers.origin && !sameOrigin)
    )
      return reject(403, 'forbidden');
    const path = req.url?.split('?')[0];
    const file = ASSETS.get(path);
    if (!file) return reject(404, 'missing');
    const body = await readFile(new URL(file, import.meta.url));
    res.writeHead(200, {
      'content-type': 'text/javascript; charset=utf-8',
      'cache-control': 'no-store',
      'x-content-type-options': 'nosniff',
      'content-length': body.length,
    });
    if (req.method !== 'HEAD') res.end(body);
    else res.end();
  };
}
