import { readFile } from 'node:fs/promises';
import { join } from 'node:path';
import { createSettingsStore, normalizeOrigin, probe } from './store.js';
import { validateBrowserOrigin } from '../managed/host-auth.js';

/** Only installation/renderer configuration may supply this value, never a request
 * header or saved platform preference. Reject normalization and non-origin URLs.
 * @param {string|undefined} value */
function trustedOrigin(value) {
  if (value === undefined) return undefined;
  const url = new URL(value);
  if (!['http:', 'https:'].includes(url.protocol) || url.origin !== value)
    throw new Error(
      'auriga-dsh: trusted UI origin must be a canonical HTTP(S) origin',
    );
  return url.origin;
}

/** Public host service adapter. Business preferences do not modify loader config
 * or reuse credentials when an origin changes.
 * @param {any} ctx @param {any} config
 * @param {{uiOrigin?:string}} [installation] Trusted process configuration. */
export function installSettings(
  ctx,
  config,
  { uiOrigin = process.env.AURIGA_SETTINGS_UI_ORIGIN } = {},
) {
  if (typeof ctx.inject !== 'function') return;
  // Managed browserOrigin is renderer-owned (the existing managed contract).
  // A local reverse proxy can instead pin its external origin at process start.
  const browser =
    config?.browserOrigin && validateBrowserOrigin(config.browserOrigin);
  const expectedOrigin = browser
    ? new URL(`${browser.scheme}://${browser.host}:${browser.port}`).origin
    : trustedOrigin(uiOrigin);
  ctx.inject(['profileContext'], async (/** @type {any} */ scoped) => {
    if (!scoped.profileContext?.dir) return;
    const store = await createSettingsStore(
      join(scoped.profileContext.dir, 'auriga-settings'),
      config?.connect?.platformOrigin,
    );
    scoped.effect(
      () =>
        scoped.webServer.register({
          kind: 'prefix',
          path: '/auriga/settings',
          handler: createSettingsHandler({
            store,
            auth: scoped.connection,
            uiOrigin: expectedOrigin,
            activeOrigin: config?.connect?.platformOrigin
              ? normalizeOrigin(config.connect.platformOrigin)
              : '',
            deviceName: config?.connect
              ? (config.connect.deviceName ?? 'Auriga DSH')
              : null,
          }),
        }),
      'auriga-dsh: settings',
    );
  });
}

/** @param {{store:Awaited<ReturnType<typeof createSettingsStore>>,auth:any,activeOrigin:string,deviceName?:string|null,uiOrigin?:string}} options */
export function createSettingsHandler({
  store,
  auth,
  activeOrigin,
  deviceName = null,
  uiOrigin,
}) {
  const expectedOrigin = trustedOrigin(uiOrigin);
  return async (
    /** @type {import('node:http').IncomingMessage} */ req,
    /** @type {import('node:http').ServerResponse} */ res,
  ) => {
    const json = (/** @type {number} */ code, /** @type {unknown} */ body) => {
      res.writeHead(code, {
        'content-type': 'application/json',
        'cache-control': 'no-store',
        'x-content-type-options': 'nosniff',
      });
      res.end(JSON.stringify(body));
    };
    if (typeof auth?.requestRejection !== 'function')
      return json(503, { reason: 'unavailable' });
    const rejected = auth.requestRejection(req);
    if (rejected !== undefined) return json(rejected, { reason: 'auth' });
    const write = req.method === 'POST';
    const count = (/** @type {string} */ key) =>
      req.rawHeaders.filter(
        (_, i) => i % 2 === 0 && req.rawHeaders[i].toLowerCase() === key,
      ).length;
    let sameOrigin = false;
    try {
      // DSH's public webServer is node:http. Native auth still owns Host trust;
      // only a trusted external origin can override direct HTTP semantics.
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
      ((write || req.headers.origin) && !sameOrigin)
    )
      return json(403, { reason: 'forbidden' });
    const path = req.url?.split('?')[0];
    try {
      if (req.method === 'GET' && path === '/auriga/settings/view.js') {
        res.writeHead(200, {
          'content-type': 'text/javascript; charset=utf-8',
          'cache-control': 'no-store',
          'x-content-type-options': 'nosniff',
        });
        res.end(await readFile(new URL('./view.js', import.meta.url)));
        return;
      }
      if (req.method === 'GET' && path === '/auriga/settings/state')
        return json(200, { ...store.snapshot(), activeOrigin, deviceName });
      if (
        !write ||
        !['/auriga/settings/save', '/auriga/settings/test'].includes(path ?? '')
      )
        return json(404, { reason: 'missing' });
      if (
        req.headers['content-type']?.split(';')[0].trim() !==
          'application/json' ||
        req.headers['x-auriga-settings'] !== '1'
      )
        return json(403, { reason: 'forbidden' });
      const parts = [];
      let length = 0;
      for await (const part of req) {
        length += part.length;
        if (length > 4096) return json(413, { reason: 'invalidSettings' });
        parts.push(part);
      }
      let input;
      try {
        input = JSON.parse(Buffer.concat(parts).toString('utf8'));
      } catch {
        return json(400, { reason: 'invalidSettings' });
      }
      if (path === '/auriga/settings/test') {
        if (!input || Object.keys(input).join(',') !== 'platformOrigin')
          return json(400, { reason: 'invalidSettings' });
        return json(200, await probe(input.platformOrigin));
      }
      await store.save(input);
      return json(200, { ...store.snapshot(), activeOrigin, deviceName });
    } catch (error) {
      const reason = error instanceof Error ? error.message : '';
      return json(
        ['invalidOrigin', 'httpsRequired', 'invalidSettings'].includes(reason)
          ? 400
          : 503,
        {
          reason: [
            'invalidOrigin',
            'httpsRequired',
            'invalidSettings',
          ].includes(reason)
            ? reason
            : 'unavailable',
        },
      );
    }
  };
}
