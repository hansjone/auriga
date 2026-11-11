import { join } from 'node:path';
import { createHash } from 'node:crypto';
import { openMemberResources } from './engine.js';
import { memberIdentity } from './member-api.js';
import { resourceHost } from './host.js';
import { parseStrictJson } from './json.js';
import { exact, refuse } from './trust.js';
const base = '/auriga/resources/local/';
/** Local owner request gate is applied by the existing catalogue channel first.
 * The browser never chooses clientId, tenant, roots, archive path or member token.
 * @param {{directory:string,ctx:any,sameOrigin:(req:any,write:boolean)=>boolean,trustFile?:string,current:()=>any}} options */
export function createMemberRoutes(options) {
  /** @type {Map<string,{runtime:any,promise:Promise<Awaited<ReturnType<typeof openMemberResources>>>}>} */
  const engines = new Map();
  let closed = false;
  /** @param {any} runtime */
  function engine(runtime) {
    if (closed) return Promise.reject(new Error('closed'));
    const who = memberIdentity(runtime),
      origin = runtime.config.platformOrigin;
    const scope = createHash('sha256').update(origin).digest('hex');
    const key = `${scope}/${who.tenantId}/${who.ownerId}/${who.clientId}`;
    const previous = engines.get(key);
    if (previous && previous.runtime === runtime) return previous.promise;
    const promise = (async () => {
      await (await previous?.promise)?.close();
      return openMemberResources({
        directory: join(options.directory, 'auriga-resources', scope),
        runtime,
        host: resourceHost(options.ctx),
        trustFile: options.trustFile,
        alive: () => !closed && options.current() === runtime,
      });
    })();
    engines.set(key, { runtime, promise });
    promise.catch(() => {
      if (engines.get(key)?.promise === promise) engines.delete(key);
    });
    return promise;
  }
  // Persisted installations must be monitored even when no market tab is open.
  // Authentication can arrive after this profile service has been constructed.
  const discovery = setInterval(() => {
    const runtime = options.current();
    if (!runtime || closed) return;
    try {
      memberIdentity(runtime);
      void engine(runtime).catch(() => {});
    } catch {
      // An unbound device has no member installation scope yet.
    }
  }, 1000);
  discovery.unref();
  return {
    /** @param {any} runtime @param {any} req @param {any} res */
    async handle(runtime, req, res) {
      /** @param {number} status @param {unknown} body */
      const send = (status, body) => {
        res.writeHead(status, {
          'content-type': 'application/json',
          'cache-control': 'no-store',
          'x-content-type-options': 'nosniff',
        });
        res.end(JSON.stringify(body));
      };
      const write = req.method === 'POST';
      if (!options.sameOrigin(req, write) || (write && !runtime.checkCsrf(req)))
        return send(403, { code: 'common.forbidden' });
      const identity = runtime.resolve(req);
      if (!identity) return send(401, { code: 'common.unauthenticated' });
      try {
        const path = req.url;
        if (req.method === 'GET' && path === base + 'installations') {
          const result = await (await engine(runtime)).list();
          if (
            options.current() !== runtime ||
            runtime.resolve(req) !== identity
          )
            return send(401, { code: 'common.unauthenticated' });
          return send(200, result);
        }
        if (!write || path !== base + 'actions')
          return send(404, { code: 'common.not_found' });
        if (
          !/^application\/json(?:;|$)/i.test(req.headers['content-type'] ?? '')
        )
          return send(400, { code: 'common.validation_failed' });
        const chunks = [];
        let length = 0;
        for await (const chunk of req) {
          length += chunk.length;
          if (length > 16384)
            return send(413, { code: 'common.validation_failed' });
          chunks.push(chunk);
        }
        const input = parseStrictJson(Buffer.concat(chunks));
        if (!exact(input, ['packageId', 'versionId', 'action', 'key']))
          refuse('common.validation_failed');
        if (options.current() !== runtime || runtime.resolve(req) !== identity)
          return send(401, { code: 'common.unauthenticated' });
        const result = await (await engine(runtime)).apply(input);
        if (options.current() !== runtime || runtime.resolve(req) !== identity)
          return send(401, { code: 'common.unauthenticated' });
        return send(200, result);
      } catch (error) {
        const code = /** @type {any} */ (error).code;
        const allowed = [
          'common.unauthenticated',
          'common.forbidden',
          'common.not_found',
          'common.conflict',
          'common.precondition_failed',
          'common.validation_failed',
          'common.rate_limited',
          'resource.signature_invalid',
          'resource.digest_mismatch',
          'resource.level_locked',
          'resource.apply_failed',
          'resource.storage_unavailable',
          'resource.state_conflict',
        ];
        const safe = allowed.includes(code) ? code : 'common.unavailable';
        return send(
          safe === 'common.unauthenticated'
            ? 401
            : safe === 'common.forbidden' || safe === 'resource.level_locked'
              ? 403
              : safe === 'common.conflict' ||
                  safe === 'common.precondition_failed'
                ? 409
                : safe === 'common.validation_failed'
                  ? 400
                  : 503,
          { code: safe },
        );
      }
    },
    async close() {
      closed = true;
      clearInterval(discovery);
      await Promise.allSettled(
        [...engines.values()].map(async (entry) =>
          (await entry.promise).close(),
        ),
      );
      engines.clear();
    },
  };
}
