import { Buffer } from 'node:buffer';

/** Invoked after native auth and same-origin admission in createConnectRoutes.
 * @param {any} runtime */
export function createMessagingRoutes(runtime) {
  return async (/** @type {any} */ req, /** @type {any} */ res) => {
    const send = (
      /** @type {number} */ status,
      /** @type {unknown} */ value,
    ) => {
      res.writeHead(status, {
        'content-type': 'application/json; charset=utf-8',
        'cache-control': 'no-store',
        'x-content-type-options': 'nosniff',
      });
      res.end(JSON.stringify(value));
    };
    if (!runtime.resolve(req) || runtime.resolve(req).localOnly)
      return send(401, { code: 'common.unauthenticated' });
    const action = (req.url ?? '')
      .split('?')[0]
      .slice('/auriga/local-login/messaging/'.length);
    try {
      if (req.method === 'GET' && action === 'snapshot')
        return send(200, await runtime.messagingUI(req).snapshot());
      if (req.method !== 'POST' || action !== 'receive')
        return send(405, { code: 'common.validation_failed' });
      if (!runtime.checkCsrf(req))
        return send(403, { code: 'common.forbidden' });
      if (
        req.headers['content-type']?.split(';')[0].trim() !== 'application/json'
      )
        return send(415, { code: 'common.validation_failed' });
      const parts = [];
      let size = 0;
      for await (const part of req) {
        size += part.length;
        if (size > 4096) return send(413, { code: 'common.validation_failed' });
        parts.push(part);
      }
      let value;
      try {
        value = JSON.parse(Buffer.concat(parts).toString('utf8'));
      } catch {
        return send(400, { code: 'common.validation_failed' });
      }
      if (
        !value ||
        typeof value !== 'object' ||
        Array.isArray(value) ||
        Object.keys(value).sort().join(',') !== 'enabled,local' ||
        typeof value.local !== 'string' ||
        !/^[1-9][0-9]{0,19}$/.test(value.local) ||
        typeof value.enabled !== 'boolean'
      )
        return send(400, { code: 'common.validation_failed' });
      return send(200, await runtime.messagingUI(req).setReceive(value));
    } catch (error) {
      const e = /** @type {any} */ (error);
      return send(503, {
        code: 'common.unavailable',
        ...(e.code === 'network.overloaded'
          ? {
              reason: e.code,
              retryable: e.retryable,
              retryAfter: e.retryAfter,
              attempts: e.attempts,
            }
          : {}),
      });
    }
  };
}
