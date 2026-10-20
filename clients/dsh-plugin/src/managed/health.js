// @ts-nocheck
import { PROTOCOL_VERSION } from './protocol.js';
import { HEALTH_PATH } from './door.js';

export { HEALTH_PATH };

/**
 * Contract fields only for unauthenticated health:
 * { status, ready, protocol, pluginVersion }
 * @param {{ pluginVersion: string, ready?: boolean }} opts
 */
export function buildHealthBody(opts) {
  return {
    status: 'ok',
    ready: opts.ready !== false,
    protocol: PROTOCOL_VERSION,
    pluginVersion: opts.pluginVersion,
  };
}

/**
 * Register GET/HEAD /_auriga/health (no identity header required).
 * @param {object} ctx Cordis-like
 * @param {{ pluginVersion: string, ready?: () => boolean }} opts
 */
export function registerHealthRoute(ctx, opts) {
  const handler = async (req, res) => {
    if (req.method !== 'GET' && req.method !== 'HEAD') {
      res.writeHead(405);
      res.end();
      return;
    }
    const body = buildHealthBody({
      pluginVersion: opts.pluginVersion,
      ready: opts.ready ? opts.ready() : true,
    });
    const raw = JSON.stringify(body);
    res.writeHead(200, {
      'content-type': 'application/json; charset=utf-8',
      'cache-control': 'no-store',
    });
    res.end(req.method === 'HEAD' ? undefined : raw);
  };

  if (ctx?.webServer?.register) {
    ctx.webServer.register({
      kind: 'exact',
      path: HEALTH_PATH,
      handler,
    });
  }
  return handler;
}
