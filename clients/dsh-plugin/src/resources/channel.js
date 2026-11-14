import { resourceRequest } from './broker-transport.js';
/* global AbortSignal */
import { catalogRoutes, watchCatalogIdentity } from './catalog-routes.js';
const prefix = '/auriga/resources';
const uuid =
  '[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}';
const reader = new RegExp(
  `^(?:/api/resource/v1/packages(?:/${uuid}(?:/versions(?:/${uuid})?)?)?|/api/resource/v1/distributions/${uuid})$`,
);

/** Validate the raw path before URL normalization. @param {string} path */
export function resourcePath(path) {
  if (typeof path !== 'string' || path.length > 4096 || /[#\\\s]/.test(path))
    throw new Error('resource path refused');
  const [pathname, query, extra] = path.split('?');
  if (!reader.test(pathname) || extra !== undefined)
    throw new Error('resource path refused');
  const params = new URLSearchParams(query);
  const paged =
    pathname.endsWith('/packages') || pathname.endsWith('/versions');
  for (const [key, value] of params) {
    if (
      !paged ||
      params.getAll(key).length !== 1 ||
      !['limit', 'cursor'].includes(key) ||
      (key === 'limit' &&
        (!/^[1-9][0-9]{0,2}$/.test(value) || Number(value) > 200)) ||
      (key === 'cursor' && (!value || value.length > 2048))
    )
      throw new Error('resource query refused');
  }
  return path;
}

/** Native authentication gates fixed reads and renderer-bound distribution writes.
 * Browser credentials never cross this boundary.
 * @param {{auth: {requestRejection(req: any): number | undefined}, current: () => any, memberHandler?:(runtime:any,req:any,res:any)=>Promise<any>, fetchImpl?: typeof fetch,ctx?:any}} options */
export function createResourceHandler({
  auth,
  current,
  memberHandler,
  fetchImpl: _fetchImpl,
  ctx,
}) {
  const catalog = catalogRoutes({ current, ctx });
  /** @param {any} req @param {any} res */
  const handle = async (req, res) => {
    /** @param {number} status @param {unknown} [body] */
    const send = (status, body = {}) => {
      res.writeHead(status, {
        'content-type': 'application/json',
        'cache-control': 'no-store',
        'x-content-type-options': 'nosniff',
      });
      res.end(JSON.stringify(body));
    };
    const rejection = auth.requestRejection(req);
    if (rejection !== undefined) return send(rejection);
    if (req.url?.startsWith(prefix + '/catalog/')) return catalog(req, res);
    if (req.url?.startsWith(prefix + '/local/')) {
      const runtime = current();
      if (!runtime || !memberHandler) return send(503);
      if (!runtime.resolve(req)) return send(401);
      return memberHandler(runtime, req, res);
    }
    const write = req.method === 'POST';
    if (req.method !== 'GET' && !write) return send(405);
    let path;
    try {
      if (!req.url?.startsWith(prefix + '/')) throw new Error('invalid prefix');
      const raw = req.url.slice(prefix.length);
      path =
        write && raw === '/api/resource/v1/distributions'
          ? raw
          : resourcePath(raw);
      if (write && path !== '/api/resource/v1/distributions') return send(405);
    } catch {
      return send(403);
    }
    const runtime = current();
    if (!runtime) return send(503);
    const identity = runtime.resolve(req);
    if (!identity) return send(401);
    if (write && (!runtime.resourceIdentity || !runtime.checkCsrf?.(req)))
      return send(403);
    try {
      let requestBody;
      let intentKey;
      if (write) {
        if (
          !/^application\/json(?:;|$)/i.test(req.headers['content-type'] ?? '')
        )
          return send(400);
        intentKey = req.headers['idempotency-key'];
        if (
          typeof intentKey !== 'string' ||
          !/^[A-Za-z0-9._-]{1,128}$/.test(intentKey)
        )
          return send(400);
        const chunks = [];
        let bytes = 0;
        for await (const chunk of req) {
          bytes += chunk.length;
          if (bytes > 65536) return send(413);
          chunks.push(chunk);
        }
        let input;
        try {
          input = JSON.parse(Buffer.concat(chunks).toString('utf8'));
        } catch {
          return send(400);
        }
        const isId = new RegExp(`^${uuid}$`);
        if (
          !input ||
          Object.keys(input).sort().join() !== 'instanceId,selections' ||
          input.instanceId !== runtime.resourceIdentity.instanceId ||
          !Array.isArray(input.selections) ||
          input.selections.length < 1 ||
          input.selections.length > 128 ||
          input.selections.some(
            (/** @type {any} */ s) =>
              !s ||
              Object.keys(s).sort().join() !== 'action,packageId,versionId' ||
              !isId.test(s.packageId) ||
              !isId.test(s.versionId) ||
              !['install', 'enable', 'disable', 'remove'].includes(s.action),
          )
        )
          return send(400);
        requestBody = JSON.stringify(input);
      }

      if (current() !== runtime || runtime.resolve(req) !== identity)
        return send(401);
      const response = await resourceRequest(runtime, path, {
        method: write ? 'POST' : 'GET',
        headers: {
          accept: 'application/json',

          ...(write
            ? {
                'content-type': 'application/json',
                'idempotency-key': intentKey,
              }
            : {}),
        },
        credentials: 'omit',
        redirect: 'error',
        signal: AbortSignal.timeout(15000),
        body: requestBody,
      });
      if (!response.ok) {
        await response.body?.cancel();
        return send(
          [400, 401, 403, 404, 409, 412, 422, 429].includes(response.status)
            ? response.status
            : 503,
        );
      }
      if (write && response.status !== 202) {
        await response.body?.cancel();
        return send(502);
      }
      if (
        !response.headers.get('content-type')?.startsWith('application/json')
      ) {
        await response.body?.cancel();
        return send(502);
      }
      const chunks = [];
      let size = 0;
      if (response.body) {
        const stream = response.body.getReader();
        try {
          for (;;) {
            const { done, value } = await stream.read();
            if (done) break;
            size += value.byteLength;
            if (size > 1048576) {
              await stream.cancel();
              return send(502);
            }
            chunks.push(value);
          }
        } finally {
          stream.releaseLock();
        }
      }
      const body = JSON.parse(Buffer.concat(chunks).toString('utf8'));
      if (current() !== runtime || runtime.resolve(req) !== identity)
        return send(401);
      return send(write ? 202 : 200, body);
    } catch {
      return send(runtime.resolve(req) ? 503 : 401);
    }
  };
  return Object.assign(handle, { reconcileCatalog: catalog.reconcile });
}

/** @param {any} ctx @param {{auth: {requestRejection(req: any): number | undefined}, current: () => any,memberHandler?:(runtime:any,req:any,res:any)=>Promise<any>}} options */
export function installResourceChannel(ctx, options) {
  if (
    typeof ctx.effect !== 'function' ||
    typeof ctx.webServer?.register !== 'function'
  )
    return false;
  const handler = createResourceHandler({ ...options, ctx });
  ctx.effect(
    () =>
      ctx.webServer.register({
        kind: 'prefix',
        path: prefix,
        handler,
      }),
    'auriga-dsh: resource catalogue and member distribution channel',
  );
  ctx.effect(
    () =>
      watchCatalogIdentity({
        current: options.current,
        onChange: handler.reconcileCatalog,
      }),
    'auriga-dsh: shared skill identity observer',
  );
  return true;
}
