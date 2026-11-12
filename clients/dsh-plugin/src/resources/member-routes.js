import { join } from 'node:path';
import { readdir, lstat } from 'node:fs/promises';
import { createHash } from 'node:crypto';
import { openMemberResources } from './engine.js';
import { memberIdentity } from './member-api.js';
import { resourceHost } from './host.js';
import { parseStrictJson } from './json.js';
import { exact, refuse, uuid } from './trust.js';
const base = '/auriga/resources/local/';
/** Local owner request gate is applied by the existing catalogue channel first.
 * The browser never chooses clientId, tenant, roots, archive path or member token.
 * @param {{directory:string,ctx:any,sameOrigin:(req:any,write:boolean)=>boolean,trustFile?:string,current:()=>any,now?:()=>number,fetchImpl?:typeof fetch}} options */
export function createMemberRoutes(options) {
  /** @type {Map<string,Promise<Awaited<ReturnType<typeof openMemberResources>>>>} */
  const engines = new Map();
  /** @type {Map<any,()=>void>} */ const subscriptions = new Map();
  const directory = join(options.directory, 'auriga-resources');
  const host = resourceHost(options.ctx);
  let closed = false;
  let identityReady = options.current() !== undefined;
  /** @param {string} scope @param {{tenantId:string,ownerId:string,clientId:string}} who */
  function openScope(scope, who) {
    const key = [scope, who.tenantId, who.ownerId, who.clientId].join('/');
    let promise = engines.get(key);
    if (!promise) {
      promise = openMemberResources({
        directory: join(directory, scope),
        identity: who,
        host,
        trustFile: options.trustFile,
        now: options.now,
        fetchImpl: options.fetchImpl,
        identityReady: () => identityReady,
        current: () => options.current(),
        alive: () =>
          !closed &&
          createHash('sha256')
            .update(options.current()?.config.platformOrigin ?? '')
            .digest('hex') === scope,
      });
      engines.set(key, promise);
      promise.catch(() => {
        if (engines.get(key) === promise) engines.delete(key);
      });
    }
    return promise;
  }
  /** @param {any} runtime */
  function engine(runtime) {
    if (closed) return Promise.reject(new Error('closed'));
    const who = memberIdentity(runtime);
    return openScope(
      createHash('sha256').update(runtime.config.platformOrigin).digest('hex'),
      who,
    );
  }
  async function discover() {
    let count = 0;
    /** @param {string} parent @param {string[]} parts */
    async function visit(parent, parts) {
      let entries;
      try {
        entries = await readdir(parent, { withFileTypes: true });
      } catch (error) {
        if (/** @type {any} */ (error).code === 'ENOENT') return;
        throw error;
      }
      for (const entry of entries) {
        if (++count > 10000) refuse('resource.storage_unavailable');
        if (
          !entry.isDirectory() ||
          entry.isSymbolicLink() ||
          (parts.length === 0
            ? !/^[a-f0-9]{64}$/.test(entry.name)
            : !uuid(entry.name))
        )
          continue;
        const path = join(parent, entry.name),
          next = [...parts, entry.name];
        if (next.length < 4) await visit(path, next);
        else {
          try {
            const manifest = await lstat(join(path, 'manifest'));
            if (!manifest.isDirectory() || manifest.isSymbolicLink()) continue;
          } catch (error) {
            if (/** @type {any} */ (error).code === 'ENOENT') continue;
            throw error;
          }
          await openScope(next[0], {
            tenantId: next[1],
            ownerId: next[2],
            clientId: next[3],
          });
        }
      }
    }
    await visit(directory, []);
  }
  let guardTail = Promise.resolve();
  function reconcile() {
    const next = guardTail.then(async () => {
      if (closed) return;
      await discover();
      // Share already verified tenant revocations across member scopes, including signed-out members.
      const lists = new Map();
      for (const [key, promise] of engines) {
        const list = (await promise).cachedRevocations();
        const scope = key.split('/').slice(0, 2).join('/');
        if (
          list &&
          (!lists.has(scope) || lists.get(scope).revision < list.revision)
        )
          lists.set(scope, list);
      }
      for (const [key, promise] of engines) {
        const member = await promise;
        const list = lists.get(key.split('/').slice(0, 2).join('/'));
        try {
          if (list) await member.acceptRevocations(list);
        } finally {
          await member.enforce();
        }
      }
    });
    guardTail = next.catch(() => {});
    return next;
  }
  /** @param {any} runtime */
  async function attach(runtime) {
    identityReady = true;
    for (const [previous, unsubscribe] of subscriptions)
      if (previous !== runtime) {
        unsubscribe();
        subscriptions.delete(previous);
      }
    if (
      runtime &&
      !subscriptions.has(runtime) &&
      typeof runtime.vault?.subscribeResourceIdentity === 'function'
    )
      subscriptions.set(
        runtime,
        runtime.vault.subscribeResourceIdentity(() => reconcile()),
      );
    try {
      if (runtime) await engine(runtime);
    } catch {
      /* Signed-out scopes are discovered independently. */
    }
    await reconcile();
  }
  const ready = reconcile();
  ready.catch(() => {});
  let checking = false;
  const discovery = setInterval(() => {
    if (checking || closed) return;
    checking = true;
    const runtime = options.current();
    // An unavailable identity provider must not defer private ownership checks indefinitely.
    if (!runtime) identityReady = true;
    void (runtime ? attach(runtime) : reconcile())
      .catch(() => {})
      .finally(() => {
        checking = false;
      });
  }, 1000);
  discovery.unref();
  return {
    ready,
    attach,
    reconcile,
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
        await ready;
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
      for (const unsubscribe of subscriptions.values()) unsubscribe();
      await guardTail;
      await Promise.allSettled(
        [...engines.values()].map(async (promise) => (await promise).close()),
      );
      engines.clear();
    },
  };
}
