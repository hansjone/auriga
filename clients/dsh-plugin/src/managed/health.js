// @ts-nocheck
import { PROTOCOL_VERSION } from './protocol.js';
import { HEALTH_PATH } from './door.js';

export { HEALTH_PATH };

/**
 * @param {{
 *   pluginVersion: string,
 *   ready?: boolean,
 *   activity?: ReturnType<import('./activity.js').createActivityProbe>,
 * }} opts
 */
export function buildHealthBody(opts) {
  const activity = opts.activity?.snapshot();
  return {
    status: 'ok',
    ready: opts.ready !== false,
    protocol: PROTOCOL_VERSION,
    pluginVersion: opts.pluginVersion,
    activity: activity
      ? {
          runningTurns: activity.runningTurns,
          openTerminals: activity.openTerminals,
          activeImBots: activity.activeImBots,
          connectedClients: activity.connectedClients,
        }
      : undefined,
  };
}

/**
 * Register GET /_auriga/health (no identity header required).
 * @param {object} ctx Cordis-like
 * @param {{ pluginVersion: string, activity?: ReturnType<import('./activity.js').createActivityProbe>, ready?: () => boolean }} opts
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
      activity: opts.activity,
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
