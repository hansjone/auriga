// @ts-nocheck
import { LocalResourceFailure } from './service.js';

export const LOCAL_RESOURCES_PATH = '/auriga/local-resources';
const json = (res, status, body) => {
  res.writeHead(status, {
    'content-type':
      status >= 400
        ? 'application/problem+json; charset=utf-8'
        : 'application/json; charset=utf-8',
    'cache-control': 'no-store',
    'x-content-type-options': 'nosniff',
  });
  res.end(JSON.stringify(body));
};
const reject = (status, code) => {
  throw new LocalResourceFailure(status, code);
};
async function input(req) {
  if (req.headers['content-type'] !== 'application/json')
    reject(400, 'common.validation_failed');
  const chunks = [];
  let length = 0;
  for await (const chunk of req) {
    length += Buffer.byteLength(chunk);
    if (length > 16384) reject(400, 'common.validation_failed');
    chunks.push(Buffer.from(chunk));
  }
  let value;
  try {
    value = JSON.parse(Buffer.concat(chunks).toString('utf8'));
  } catch {
    reject(400, 'common.validation_failed');
  }
  if (!value || typeof value !== 'object' || Array.isArray(value))
    reject(400, 'common.validation_failed');
  return value;
}

/** Preserve native requestRejection and require a non-simple, same-origin carrier. */
export function createLocalResourceHandler({
  auth,
  service,
  alive = () => true,
}) {
  return async (req, res) => {
    const authorized = () =>
      alive() && !req.aborted && auth.requestRejection(req) === undefined;
    try {
      if (!alive()) reject(503, 'common.unavailable');
      const denial = auth.requestRejection(req);
      if (denial !== undefined)
        reject(
          denial,
          denial === 401 ? 'common.unauthenticated' : 'common.forbidden',
        );
      if (req.headers['x-auriga-local-resources'] !== '1')
        reject(403, 'common.forbidden');
      const path = req.url;
      let result;
      if (path === LOCAL_RESOURCES_PATH && req.method === 'GET')
        result = await service.list();
      else {
        if (
          ![
            `${LOCAL_RESOURCES_PATH}/detail`,
            `${LOCAL_RESOURCES_PATH}/enabled`,
            `${LOCAL_RESOURCES_PATH}/config`,
          ].includes(path)
        )
          reject(404, 'common.not_found');
        if (req.method !== 'POST') reject(405, 'common.validation_failed');
        // Managed reads keep the existing edge-derived fence. Mutations never reach it.
        if (path !== `${LOCAL_RESOURCES_PATH}/detail`) {
          const expected = `${req.socket?.encrypted ? 'https' : 'http'}://${req.headers.host}`;
          if (req.headers.origin !== new URL(expected).origin)
            reject(403, 'common.forbidden');
        }
        const body = await input(req);
        const action = path.slice(LOCAL_RESOURCES_PATH.length + 1);
        const keys =
          action === 'detail'
            ? 'id'
            : action === 'enabled'
              ? 'enabled,id,revision'
              : 'changes,configRevision,id,revision';
        if (Object.keys(body).sort().join(',') !== keys)
          reject(400, 'common.validation_failed');
        if (!authorized()) reject(401, 'common.unauthenticated');
        result =
          action === 'detail'
            ? await service.detail(body.id)
            : await service.change(action, body, authorized);
      }
      if (!authorized()) reject(401, 'common.unauthenticated');
      json(res, 200, result);
    } catch (error) {
      const known = error instanceof LocalResourceFailure;
      if (!res.destroyed)
        json(res, known ? error.status : 503, {
          type: 'about:blank',
          status: known ? error.status : 503,
          code: known ? error.code : 'common.unavailable',
          detail: 'local resource request rejected',
        });
    }
  };
}
