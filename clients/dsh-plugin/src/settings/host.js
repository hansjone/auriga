import { readFile } from 'node:fs/promises';
import { join } from 'node:path';
import { TLSSocket } from 'node:tls';
import { createSettingsStore, normalizeOrigin, probe } from './store.js';

/** Public host service adapter. Business preferences do not modify loader config
 * or reuse credentials when an origin changes.
 * @param {any} ctx @param {any} config */
export function installSettings(ctx, config) {
  if (typeof ctx.inject !== 'function') return;
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

/** @param {{store:Awaited<ReturnType<typeof createSettingsStore>>,auth:any,activeOrigin:string,deviceName?:string|null}} options */
export function createSettingsHandler({
  store,
  auth,
  activeOrigin,
  deviceName = null,
}) {
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
    const protocol = req.socket instanceof TLSSocket ? 'https:' : 'http:';
    try {
      sameOrigin =
        count('host') === 1 &&
        count('origin') === 1 &&
        req.headers.origin ===
          new URL(protocol + '//' + req.headers.host).origin;
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
