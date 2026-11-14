import { readFile } from 'node:fs/promises';
import { join } from 'node:path';
import { homedir } from 'node:os';
import { createSettingsStore, probe } from './store.js';
import { validateBrowserOrigin } from '../managed/host-auth.js';
import { createPrivateSkills } from '../resources/private-skills.js';

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
 * @param {{uiOrigin?:string,activeOrigin?:()=>string,desktop?:()=>any,networkControl?:()=>any,mode?:string,nativeAuth?:{requestRejection:(req:any)=>number|undefined}}} [installation] Trusted process configuration. */
export function installSettings(
  ctx,
  config,
  {
    uiOrigin = process.env.AURIGA_SETTINGS_UI_ORIGIN,
    activeOrigin,
    desktop,
    networkControl,
    nativeAuth,
    mode = 'connect',
  } = {},
) {
  /** @type {Map<string,ReturnType<typeof createSettingsStore>>} */
  const stores = new Map();
  const getStore = (/** @type {string} */ dir) => {
    if (!stores.has(dir))
      stores.set(
        dir,
        createSettingsStore(
          join(dir, 'auriga-settings'),
          config?.connect?.platformOrigin,
        ),
      );
    return /** @type {ReturnType<typeof createSettingsStore>} */ (
      stores.get(dir)
    );
  };
  if (typeof ctx.inject !== 'function') return { getStore };
  // Managed browserOrigin is renderer-owned (the existing managed contract).
  // A local reverse proxy can instead pin its external origin at process start.
  const browser =
    config?.browserOrigin && validateBrowserOrigin(config.browserOrigin);
  const expectedOrigin = browser
    ? new URL(`${browser.scheme}://${browser.host}:${browser.port}`).origin
    : trustedOrigin(uiOrigin);
  ctx.inject(['profileContext'], async (/** @type {any} */ scoped) => {
    if (!scoped.profileContext?.dir) return;
    const store = await getStore(scoped.profileContext.dir);
    scoped.effect(
      () =>
        scoped.webServer.register({
          kind: 'prefix',
          path: '/auriga/settings',
          handler: createSettingsHandler({
            store,
            desktop,
            networkControl,
            mode,
            privateSkills: createPrivateSkills({
              home: process.env.DSH_HOME || join(homedir(), '.dsh'),
            }),
            auth: nativeAuth ?? scoped.connection,
            uiOrigin: expectedOrigin,
            activeOrigin:
              activeOrigin ?? (() => store.snapshot().platformOrigin),
            deviceName: config?.connect
              ? (config.connect.deviceName ?? null)
              : null,
          }),
        }),
      'auriga-dsh: settings',
    );
  });
  return { getStore };
}

/** @param {{store:Awaited<ReturnType<typeof createSettingsStore>>,auth:any,activeOrigin:string|(()=>string),deviceName?:string|null,desktop?:()=>any,networkControl?:()=>any,uiOrigin?:string,mode?:string,privateSkills?:ReturnType<typeof createPrivateSkills>}} options */
export function createSettingsHandler({
  store,
  desktop,
  auth,
  activeOrigin,
  deviceName = null,
  uiOrigin,
  mode = 'connect',
  privateSkills,
  networkControl,
}) {
  const expectedOrigin = trustedOrigin(uiOrigin);
  const snapshot = () => ({
    ...store.snapshot(),
    ...(desktop?.() ? { desktop: desktop().status() } : {}),
    mode,
    activeOrigin:
      typeof activeOrigin === 'function' ? activeOrigin() : activeOrigin,
    deviceName,
    ...(networkControl
      ? { networkControl: { available: networkControl()?.available === true } }
      : {}),
  });
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
      // Origin-less desktop forwarding relies on owner admission AND the
      // custom JSON header requiring a browser CORS preflight (not permitted
      // by DSH cross-origin). A self-claimed header alone is never authority.
      (req.headers['sec-fetch-site'] &&
        !['same-origin', 'none'].includes(req.headers['sec-fetch-site'])) ||
      ((write || req.headers.origin) &&
        !sameOrigin &&
        !(
          write &&
          req.headers.origin === undefined &&
          req.headers['x-auriga-settings'] === '1' &&
          req.headers['content-type']?.split(';')[0].trim() ===
            'application/json'
        ))
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
        return json(200, snapshot());
      if (write && path === '/auriga/settings/network-control') {
        if (
          req.headers['content-type']?.split(';')[0].trim() !==
            'application/json' ||
          req.headers['x-auriga-settings'] !== '1'
        )
          return json(403, { reason: 'forbidden' });
        const control = networkControl?.();
        if (!control?.available) return json(503, { reason: 'unavailable' });
        const bodyLimit = 64 * 1024 * 1024;
        const declared = req.headers['content-length'];
        if (
          declared &&
          (!/^\d+$/.test(declared) || Number(declared) > bodyLimit)
        )
          return json(413, { reason: 'invalidSettings' });
        const parts = [];
        let size = 0;
        for await (const part of req) {
          size += part.length;
          if (size > bodyLimit) return json(413, { reason: 'invalidSettings' });
          parts.push(part);
        }
        let input;
        try {
          input = JSON.parse(Buffer.concat(parts).toString('utf8'));
        } catch {
          return json(400, { reason: 'invalidSettings' });
        }
        if (
          !input ||
          Object.keys(input).join(',') !== 'packageJson' ||
          typeof input.packageJson !== 'string' ||
          Buffer.byteLength(input.packageJson) > 32 * 1024 * 1024
        )
          return json(400, { reason: 'invalidSettings' });
        try {
          return json(200, {
            receipt: await control.importPackage(input.packageJson),
          });
        } catch {
          return json(422, { reason: 'invalidSettings' });
        }
      }
      if (req.method === 'GET' && path === '/auriga/settings/skills')
        return privateSkills
          ? json(200, {
              items: await privateSkills.list(),
              ...((await privateSkills.writeLock?.())
                ? { writeLock: await privateSkills.writeLock() }
                : {}),
            })
          : json(503, { reason: 'unavailable' });
      if (write && path === '/auriga/settings/skills') {
        if (!privateSkills) return json(503, { reason: 'unavailable' });
        if (
          req.headers['content-type']?.split(';')[0].trim() !==
            'application/json' ||
          req.headers['x-auriga-settings'] !== '1'
        )
          return json(403, { reason: 'forbidden' });
        const chunks = [];
        let length = 0;
        for await (const chunk of req) {
          length += chunk.length;
          if (length > 1048576) return json(413, { reason: 'invalidSettings' });
          chunks.push(chunk);
        }
        let input;
        try {
          input = JSON.parse(Buffer.concat(chunks).toString('utf8'));
        } catch {
          return json(400, { reason: 'invalidSettings' });
        }
        if (!input || typeof input !== 'object' || Array.isArray(input))
          return json(400, { reason: 'invalidSettings' });
        const keys = Object.keys(input).sort().join(',');
        try {
          if (input.action === 'create' && keys === 'action,source')
            return json(200, await privateSkills.create(input.source));
          if (input.action === 'preview' && keys === 'action,snapshotId')
            return json(200, await privateSkills.frozen(input.snapshotId));
          if (
            input.action === 'read' &&
            keys === 'action,enabled,name' &&
            typeof input.enabled === 'boolean'
          )
            return json(
              200,
              await privateSkills.read(input.name, input.enabled),
            );
          if (
            (input.action === 'edit' &&
              keys === 'action,enabled,name,revision,source') ||
            (['enable', 'disable', 'remove', 'freeze'].includes(input.action) &&
              keys === 'action,enabled,name,revision')
          )
            return json(200, await privateSkills.change(input));
          return json(400, { reason: 'invalidSettings' });
        } catch (error) {
          return json(
            /** @type {any} */ (error).code === 'common.conflict' ||
              /** @type {any} */ (error).code === 'EEXIST'
              ? 409
              : 422,
            { reason: 'invalidSettings' },
          );
        }
      }
      if (mode === 'managed') return json(403, { reason: 'managed' });
      if (
        !write ||
        ![
          '/auriga/settings/save',
          '/auriga/settings/test',
          '/auriga/settings/desktop',
        ].includes(path ?? '')
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
      if (path === '/auriga/settings/desktop') {
        const updater = desktop?.();
        if (!updater) return json(503, { reason: 'unavailable' });
        if (
          Object.keys(input ?? {}).join(',') === 'enabled' &&
          typeof input.enabled === 'boolean'
        )
          return json(200, updater.configure(input.enabled));
        if (Object.keys(input ?? {}).join(',') === 'channel')
          return json(200, updater.setChannel(input.channel));
        if (Object.keys(input ?? {}).join(',') !== 'action')
          return json(400, { reason: 'invalidSettings' });
        if (input.action === 'check') return json(200, await updater.check());
        if (input.action === 'update') return json(202, updater.beginUpdate());
        if (input.action === 'cancel') return json(200, updater.cancel());
        return json(400, { reason: 'invalidSettings' });
      }
      if (path === '/auriga/settings/test') {
        if (!input || Object.keys(input).join(',') !== 'platformOrigin')
          return json(400, { reason: 'invalidSettings' });
        return json(200, await probe(input.platformOrigin));
      }
      await store.save(input);
      return json(200, snapshot());
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
