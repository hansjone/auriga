import { homedir } from 'node:os';
import { join } from 'node:path';
import { diagnoseResource } from './diagnostics.js';
import { catalogService } from './catalog-service.js';
import { catalogApi, catalogIdentity } from './catalog-api.js';
import { catalogHost } from './catalog-host.js';
import { resourceHost } from './host.js';
import { createPrivateSkills } from './private-skills.js';
import { exact, refuse } from './trust.js';
import { parseStrictJson, canonicalize } from './json.js';
import { existsSync, lstatSync, readFileSync } from 'node:fs';
import { assertLocalPath } from './host-lock.js';
import { createCatalogIdentityFence } from './identity-fence.js';

/** Owner identity changes must clear the old complete active view even without an HTTP request.
 * This is a content observer only; it neither runs a guardian nor recovers another writer's lock.
 * @param {{current:()=>any,home?:string,managed?:boolean,onChange?:(event:{changed:boolean})=>Promise<any>}} options */
export function watchCatalogIdentity({
  current,
  home = process.env.DSH_HOME,
  managed = process.env.AURIGA_MODE === 'managed',
  onChange,
}) {
  if (managed || !home) return () => {};
  const fence = createCatalogIdentityFence({ home });
  const state = join(home, 'auriga/guardian/state.json');
  let closed = false,
    running = false;
  /** @type {any} */ let runtimeSeen;
  let stamp = '',
    fingerprint = '',
    previous = '';
  // E may rewrite revision/clock every second. Only meaningful guardian changes
  // bypass the service's minute schedule; the cheap file observer takes no C lock.
  const guardianFingerprint = () => {
    try {
      assertLocalPath(state);
      if (!existsSync(state)) {
        stamp = '';
        return 'missing';
      }
      const stat = lstatSync(state);
      if (!stat.isFile() || stat.size > 2097152)
        throw new Error('invalid state');
      const current = stat.mtimeMs + ':' + stat.ino + ':' + stat.size;
      if (current !== stamp) {
        const value = parseStrictJson(readFileSync(state), true);
        fingerprint = canonicalize({
          guardian: value.guardian,
          pendingIsolation: value.pendingIsolation ?? null,
        });
        stamp = current;
      }
      return fingerprint;
    } catch {
      stamp = '';
      return 'invalid';
    }
  };
  const tick = () => {
    if (closed || running) return;
    const runtime = current();
    if (!runtime || runtime.resourceIdentity) return;
    const fingerprint = guardianFingerprint();
    const changed = runtime !== runtimeSeen || fingerprint !== previous;
    runtimeSeen = runtime;
    previous = fingerprint;
    running = true;
    void (existsSync(state) ? fence.fromRuntime(runtime) : Promise.resolve())
      .then(() => {
        if (!closed) return onChange?.({ changed });
      })
      .catch(() => {})
      .finally(() => {
        running = false;
      });
  };
  const timer = setInterval(tick, 1000);
  timer.unref();
  tick();
  return () => {
    closed = true;
    fence.close();
    clearInterval(timer);
  };
}
/** Native admission has already bound the request to this DSH owner.
 * Local routes never forward browser cookies, tokens, client IDs or filesystem roots.
 * @param {{current:()=>any,ctx?:any,fetchImpl?:typeof fetch,service?:(runtime:any)=>any}} options */
export function catalogRoutes(options) {
  const services = new WeakMap();
  /** @param {any} runtime @param {ReturnType<typeof catalogIdentity>} who */
  function service(runtime, who) {
    const key = canonicalize([who, runtime.catalogGeneration?.() ?? null]);
    let cached = services.get(runtime);
    if (cached?.key !== key) {
      const home = process.env.DSH_HOME || join(homedir(), '.dsh');
      const value =
        options.service?.(runtime) ??
        catalogService({
          runtime,
          current: options.current,
          home,
          privateSkills: createPrivateSkills({ home }),
          host: catalogHost(options.ctx),
          pluginHost: resourceHost(options.ctx),
          trustFile: process.env.AURIGA_RESOURCE_TRUST_FILE,
          fetchImpl: options.fetchImpl,
        });
      cached = { key, value };
      services.set(runtime, cached);
    }
    return cached.value;
  }
  const handle = async (/** @type {any} */ req, /** @type {any} */ res) => {
    const send = (/** @type {number} */ status, /** @type {any} */ body) => {
      res.writeHead(status, {
        'content-type': 'application/json',
        'cache-control': 'no-store',
        'x-content-type-options': 'nosniff',
      });
      res.end(JSON.stringify(body));
    };
    const runtime = options.current();
    if (!runtime || !runtime.resolve(req))
      return send(401, { code: 'common.unauthenticated' });
    const write = req.method === 'POST',
      path = req.url.slice('/auriga/resources/catalog/'.length);
    if (req.method !== 'GET' && !write) return send(405, {});
    if (
      write &&
      (req.headers['x-auriga-catalog'] !== '1' ||
        !/^application\/json(?:;|$)/i.test(req.headers['content-type'] ?? '') ||
        (req.headers['sec-fetch-site'] &&
          !['same-origin', 'none'].includes(req.headers['sec-fetch-site'])))
    )
      return send(403, { code: 'common.forbidden' });
    // The native managed adapter already verifies browserFence; local admission owns its origin.
    // Custom JSON header requires a CORS preflight; this handler never allows cross-origin requests.
    if (write && req.headers.origin && !runtime.resourceIdentity) {
      try {
        const origin = new URL(req.headers.origin);
        if (
          origin.origin !== req.headers.origin ||
          !['http:', 'https:'].includes(origin.protocol)
        )
          return send(403, { code: 'common.forbidden' });
        if (
          origin.host !== req.headers.host &&
          origin.origin !== process.env.AURIGA_SETTINGS_UI_ORIGIN
        )
          return send(403, { code: 'common.forbidden' });
      } catch {
        return send(403, { code: 'common.forbidden' });
      }
    }
    try {
      const api = catalogApi({
        runtime,
        current: options.current,
        fetchImpl: options.fetchImpl,
      });
      const owner = req.headers?.['x-auriga-catalog-owner'];
      const generation = req.headers?.['x-auriga-catalog-generation'];
      if (
        (owner !== undefined && owner !== api.who.ownerId) ||
        (generation !== undefined &&
          generation !== runtime.catalogGeneration?.())
      )
        refuse('common.unauthenticated');
      if (
        !write &&
        /^packages(?:\/[a-f0-9-]{36}(?:\/versions(?:\/[a-f0-9-]{36})?)?)?(?:\?[^#\\]*)?$/.test(
          path,
        )
      ) {
        const [pathname, query] = path.split('?');
        const params = new URLSearchParams(query);
        for (const [key, value] of params) {
          if (
            !['cursor', 'limit', 'kind'].includes(key) ||
            (key === 'kind' && !['plugin', 'skill'].includes(value)) ||
            params.getAll(key).length !== 1 ||
            (key === 'limit' &&
              !/^(?:[1-9]|[1-9][0-9]|1[0-9]{2}|200)$/.test(value)) ||
            (key === 'cursor' && (!value || value.length > 2048))
          )
            refuse('common.validation_failed');
        }
        const { value } = await api.request(
          '/api/resource/v1/catalog/' +
            pathname +
            (params.size ? '?' + params : ''),
        );
        if (
          value.items &&
          value.items.some(
            (/** @type {any} */ item) =>
              !['skill', 'plugin'].includes(item.kind) ||
              (params.has('kind') && item.kind !== params.get('kind')),
          )
        )
          refuse('common.validation_failed');
        return send(200, value);
      }
      const s = service(runtime, api.who);
      if (!write && path === 'installations') {
        const result = await s.list();
        api.check();
        return send(200, result);
      }
      if (!write) return send(404, {});
      const chunks = [];
      let bytes = 0;
      for await (const chunk of req) {
        if ((bytes += chunk.length) > 65536) return send(413, {});
        chunks.push(chunk);
      }
      const input = parseStrictJson(Buffer.concat(chunks));
      let result;
      if (
        path === 'actions' &&
        (exact(input, [
          'packageId',
          'versionId',
          'action',
          'revision',
          'key',
        ]) ||
          exact(input, [
            'packageId',
            'versionId',
            'action',
            'revision',
            'key',
            'confirmation',
          ]))
      )
        result = await s.apply(input);
      else if (path === 'observe' && exact(input, ['packageId', 'preset']))
        result = await s.observe(input.packageId, input.preset);
      else if (path === 'submission-targets' && exact(input, ['snapshotId']))
        result = await s.targets(input.snapshotId);
      else if (
        path === 'submissions' &&
        (exact(input, [
          'snapshotId',
          'version',
          'license',
          'author',
          'reason',
          'permissions',
          'key',
        ]) ||
          exact(input, [
            'snapshotId',
            'version',
            'license',
            'author',
            'reason',
            'permissions',
            'key',
            'packageId',
          ]))
      )
        result = await s.submit(input);
      else return send(400, { code: 'common.validation_failed' });
      api.check();
      return send(path === 'actions' ? 202 : 200, result);
    } catch (error) {
      const failure = diagnoseResource(error, 'catalog');
      return send(failure.status, failure.body);
    }
  };
  return Object.assign(handle, {
    /** @param {{changed?:boolean}} [event] */
    async reconcile(event) {
      const runtime = options.current();
      if (!runtime || runtime.resourceIdentity) return;
      if (!runtime.identity?.()) {
        services.delete(runtime);
        return;
      }
      await service(runtime, catalogIdentity(runtime)).reconcile(event);
    },
  });
}
