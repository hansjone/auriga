import { Buffer } from 'node:buffer';
import { randomBytes } from 'node:crypto';
import { parseStrictJson } from '../../network/trust/jcs.js';
import { fold } from './adapter.js';

const BASE = '/auriga/uac';
/** @param {any} res @param {number} status @param {any} body */
function json(res, status, body) {
  res.writeHead(status, {
    'content-type': 'application/json; charset=utf-8',
    'cache-control': 'no-store',
    'x-content-type-options': 'nosniff',
  });
  res.end(JSON.stringify(body));
}

/**
 * Local UAC routes. Native DSH owner authentication admits the request; writes
 * additionally require exact same-origin and this process's CSRF token.
 * @param {{auth:{requestRejection(req:any):number|undefined},sameOrigin:(req:any,write:boolean)=>boolean,
 *   identity:()=>ReturnType<typeof import('./session.js').createLocalUacIdentity>|undefined,
 *   platformEmployee?:()=>string|undefined}} options
 */
export function createUacRoutes({
  auth,
  sameOrigin,
  identity,
  platformEmployee,
}) {
  const csrf = randomBytes(32).toString('base64url');
  const view = (/** @type {any} */ status) => {
    const platform = platformEmployee?.();
    return {
      ...status,
      csrf,
      // Association is informational: a local UAC session never stands in
      // for platform authorization, and a mismatch is surfaced, not merged.
      platformLink:
        status.state !== 'authenticated' || !platform
          ? 'none'
          : fold(platform) === fold(status.empNo)
            ? 'matched'
            : 'mismatch',
    };
  };
  return async (/** @type {any} */ req, /** @type {any} */ res) => {
    const rejected = auth.requestRejection(req);
    if (rejected !== undefined)
      return json(res, rejected, { code: 'common.forbidden' });
    const write = req.method !== 'GET';
    if (!sameOrigin(req, write))
      return json(res, 403, { code: 'common.forbidden' });
    const path = (req.url ?? '').split('?')[0];
    const current = identity();
    if (req.method === 'GET' && path === BASE + '/status') {
      if (!current)
        return json(
          res,
          200,
          view({ available: false, state: 'signed_out', ownerBound: false }),
        );
      await current.ready;
      return json(res, 200, view(current.status()));
    }
    const action = path.slice(BASE.length + 1);
    if (
      req.method !== 'POST' ||
      !path.startsWith(BASE + '/') ||
      !['start', 'poll', 'cancel', 'logout'].includes(action)
    )
      return json(res, 404, { code: 'common.not_found' });
    if (req.headers['x-auriga-csrf'] !== csrf)
      return json(res, 403, { code: 'common.forbidden' });
    if (!/^application\/json(?:;|$)/i.test(req.headers['content-type'] ?? ''))
      return json(res, 400, { code: 'common.validation_failed' });
    const chunks = [];
    let size = 0;
    let input;
    try {
      for await (const chunk of req) {
        if ((size += chunk.length) > 1024)
          return json(res, 400, { code: 'common.validation_failed' });
        chunks.push(chunk);
      }
      input = parseStrictJson(Buffer.concat(chunks), { maxBytes: 1024 });
    } catch {
      return json(res, 400, { code: 'common.validation_failed' });
    }
    const keys = Object.keys(input ?? {}).join(',');
    const needsId = action === 'poll' || action === 'cancel';
    if (
      !input ||
      typeof input !== 'object' ||
      Array.isArray(input) ||
      keys !== (needsId ? 'id' : '') ||
      (needsId &&
        (typeof input.id !== 'string' || !/^[0-9a-f-]{36}$/.test(input.id)))
    )
      return json(res, 400, { code: 'common.validation_failed' });
    if (!current) return json(res, 503, { code: 'common.unavailable' });
    try {
      if (action === 'start') await current.start();
      else if (action === 'poll') await current.poll(input.id);
      else if (action === 'cancel') await current.cancel(input.id);
      else await current.logout();
      return json(res, 200, view(current.status()));
    } catch (error) {
      const code = /** @type {any} */ (error)?.code;
      return code === 'not_found'
        ? json(res, 404, { code: 'common.not_found' })
        : json(res, 503, { code: 'common.unavailable' });
    }
  };
}
