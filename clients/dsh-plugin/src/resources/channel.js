/* global AbortSignal */
const prefix = '/auriga/resources';
const uuid =
  '[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}';
const reader = new RegExp(
  `^/api/resource/v1/packages(?:/${uuid}(?:/versions(?:/${uuid})?)?)?$`,
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

/** Native authentication gates a read-only proxy. No request headers cross this boundary.
 * @param {{auth: {requestRejection(req: any): number | undefined}, current: () => any, fetchImpl?: typeof fetch}} options */
export function createResourceHandler({ auth, current, fetchImpl = fetch }) {
  /** @param {any} req @param {any} res */
  return async (req, res) => {
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
    if (req.method !== 'GET') return send(405);
    let path;
    try {
      if (!req.url?.startsWith(prefix + '/')) throw new Error('invalid prefix');
      path = resourcePath(req.url.slice(prefix.length));
    } catch {
      return send(403);
    }
    const runtime = current();
    if (!runtime) return send(503);
    const identity = runtime.resolve(req);
    if (!identity) return send(401);
    try {
      const token = await runtime.getAccess();
      if (current() !== runtime || runtime.resolve(req) !== identity)
        return send(401);
      const response = await fetchImpl(
        new URL(path, runtime.config.platformOrigin),
        {
          method: 'GET',
          headers: {
            accept: 'application/json',
            authorization: 'Bearer ' + token,
          },
          credentials: 'omit',
          redirect: 'error',
          signal: AbortSignal.timeout(15000),
        },
      );
      if (!response.ok) {
        await response.body?.cancel();
        return send(
          [401, 403, 404, 429].includes(response.status)
            ? response.status
            : 503,
        );
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
      return send(200, body);
    } catch {
      return send(runtime.resolve(req) ? 503 : 401);
    }
  };
}

/** @param {any} ctx @param {{auth: {requestRejection(req: any): number | undefined}, current: () => any}} options */
export function installResourceChannel(ctx, options) {
  ctx.effect(
    () =>
      ctx.webServer.register({
        kind: 'prefix',
        path: prefix,
        handler: createResourceHandler(options),
      }),
    'auriga-dsh: read-only resource catalogue',
  );
}
