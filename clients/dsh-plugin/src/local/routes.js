import { Buffer } from 'node:buffer';
import { createConsentRoutes } from './consent-routes.js';
import { createMessagingRoutes } from './messaging-routes.js';
import { parseStrictJson } from '../network/trust/jcs.js';
const BASE = '/auriga/shell';
const actions = new Set(['open_login', 'cancel_login', 'logout']);
/** @param {any} res @param {number} status @param {any} body */
function json(res, status, body) {
  res.writeHead(status, {
    'content-type': 'application/json; charset=utf-8',
    'cache-control': 'no-store',
    'x-content-type-options': 'nosniff',
  });
  res.end(JSON.stringify(body));
}
/** Local owner always remains admitted. No login HTML, assets or form route.
 * @param {any} connection */
export function createUnconfiguredLoginRoutes(connection) {
  return createConnectRoutes({
    runtime: null,
    admission: {
      nativeAuth: connection,
      sameOrigin: (/** @type {any} */ req) => req.method === 'GET',
    },
  });
}
/** @param {{runtime:any,admission:any,diagnostic?:(value:any)=>void}} options */
export function createConnectRoutes({ runtime, admission }) {
  const consent = createConsentRoutes(runtime),
    messaging = createMessagingRoutes(runtime);
  return async (/** @type {any} */ req, /** @type {any} */ res) => {
    const rejected = admission.nativeAuth.requestRejection(req);
    if (rejected !== undefined)
      return json(res, rejected, { code: 'common.forbidden' });
    if (!admission.sameOrigin(req, req.method !== 'GET'))
      return json(res, 403, { code: 'common.forbidden' });
    const path = (req.url ?? '').split('?')[0];
    if (path !== BASE && !path.startsWith(BASE + '/'))
      return json(res, 404, { code: 'common.not_found' });
    if (req.method === 'GET' && path === BASE + '/status')
      return json(
        res,
        200,
        runtime
          ? runtime.prepare(req).body
          : {
              available: true,
              configured: false,
              loggedIn: false,
              connected: false,
              account: null,
              brokerAvailable: false,
            },
      );
    if (!runtime) return json(res, 503, { code: 'common.unavailable' });
    if (path.startsWith(BASE + '/consent/')) return consent(req, res);
    if (path.startsWith(BASE + '/messaging/')) return messaging(req, res);
    if (req.method !== 'POST' || path !== BASE + '/command')
      return json(res, 404, { code: 'common.not_found' });
    if (!runtime.checkCsrf(req))
      return json(res, 403, { code: 'common.forbidden' });
    if (!/^application\/json(?:;|$)/i.test(req.headers['content-type'] ?? ''))
      return json(res, 400, { code: 'common.validation_failed' });
    try {
      const chunks = [];
      let size = 0;
      for await (const chunk of req) {
        if ((size += chunk.length) > 1024)
          return json(res, 400, { code: 'common.validation_failed' });
        chunks.push(chunk);
      }
      let value;
      try {
        value = parseStrictJson(Buffer.concat(chunks));
      } catch {
        return json(res, 400, { code: 'common.validation_failed' });
      }
      if (
        !value ||
        typeof value !== 'object' ||
        Array.isArray(value) ||
        Object.keys(value).join(',') !== 'action' ||
        !actions.has(value.action)
      )
        return json(res, 400, { code: 'common.validation_failed' });
      return json(res, 200, await runtime.command(req, value.action));
    } catch {
      return json(res, 503, { code: 'common.unavailable' });
    }
  };
}
