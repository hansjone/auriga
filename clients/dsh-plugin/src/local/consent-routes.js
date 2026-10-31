import { Buffer } from 'node:buffer';

/** Called only after the existing native admission and same-origin checks.
 * The authenticated request itself is the capability; browser identity fields
 * are never copied into consent context. @param {any} runtime */
export function createConsentRoutes(runtime) {
  return async (/** @type {any} */ req, /** @type {any} */ res) => {
    const path = (req.url ?? '').split('?')[0];
    const action = path.slice('/auriga/local-login/consent/'.length);
    const send = (
      /** @type {number} */ status,
      /** @type {unknown} */ body,
    ) => {
      res.writeHead(status, {
        'content-type': 'application/json; charset=utf-8',
        'cache-control': 'no-store',
        'x-content-type-options': 'nosniff',
      });
      res.end(JSON.stringify(body));
    };
    if (!runtime.resolve(req) || runtime.resolve(req).localOnly)
      return send(401, { code: 'common.unauthenticated' });
    try {
      if (req.method === 'GET' && action === 'snapshot')
        return send(200, await runtime.consentUI(req).snapshot());
      if (
        req.method !== 'POST' ||
        !['decide', 'default', 'revoke'].includes(action)
      )
        return send(405, { code: 'common.validation_failed' });
      if (!runtime.checkCsrf(req))
        return send(403, { code: 'common.forbidden' });
      if (
        req.headers['content-type']?.split(';')[0].trim() !== 'application/json'
      )
        return send(415, { code: 'common.validation_failed' });
      const parts = [];
      let size = 0;
      for await (const chunk of req) {
        size += chunk.length;
        if (size > 16384)
          return send(413, { code: 'common.validation_failed' });
        parts.push(chunk);
      }
      let input;
      try {
        input = JSON.parse(Buffer.concat(parts).toString('utf8'));
      } catch {
        return send(400, { code: 'common.validation_failed' });
      }
      const allowed = /** @type {Record<string,string[]>} */ ({
        decide: [
          'layer',
          'requestId',
          'choice',
          'sourceScope',
          'expectedRevision',
        ],
        default: ['layer', 'mode'],
        revoke: ['layer', 'ruleId'],
      })[action];
      if (
        !input ||
        typeof input !== 'object' ||
        Array.isArray(input) ||
        Object.keys(input).some((key) => !allowed.includes(key))
      )
        return send(400, { code: 'common.validation_failed' });
      const ui = runtime.consentUI(req);
      if (action === 'decide') await ui.decide(input);
      else if (action === 'default')
        await ui.setDefault(input.layer, input.mode);
      else await ui.revoke(input);
      return send(200, { ok: true });
    } catch (error) {
      return send(503, {
        code: 'common.unavailable',
        ...(error instanceof Error &&
        'reason' in error &&
        error.reason === 'cooldown'
          ? { reason: 'cooldown' }
          : {}),
      });
    }
  };
}
