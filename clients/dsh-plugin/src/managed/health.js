// @ts-nocheck
import { PROTOCOL_VERSION } from './protocol.js';
import { HEALTH_PATH } from './door.js';
import { randomBytes } from 'node:crypto';
import { performance } from 'node:perf_hooks';
import { validTraceparent, newTraceparent } from '../observability/index.js';
import { emitHealthObservation } from '../network/diagnostics.js';

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
 * @param {{ pluginVersion: string, ready?: () => boolean, instanceId?: string }} opts
 */
export function registerHealthRoute(ctx, opts) {
  // Bound duplicate probe logging per trace/state; no long-lived caller context.
  const seen = new Map();
  const handler = async (req, res) => {
    if (req.method !== 'GET' && req.method !== 'HEAD') {
      res.writeHead(405);
      res.end();
      return;
    }
    const started = performance.now();
    const values = [];
    for (let i = 0; i < (req.rawHeaders?.length ?? 0); i += 2) {
      if (String(req.rawHeaders[i]).toLowerCase() === 'traceparent')
        values.push(req.rawHeaders[i + 1]);
    }
    const supplied =
      values.length === 1
        ? values[0]
        : !req.rawHeaders
          ? req.headers?.traceparent
          : undefined;
    const parent = validTraceparent(supplied) ? supplied : newTraceparent();
    const span = `00-${parent.slice(3, 35)}-${randomBytes(8).toString('hex')}-${parent.slice(53)}`;
    const body = buildHealthBody({
      pluginVersion: opts.pluginVersion,
      ready: opts.ready ? opts.ready() : true,
    });
    const raw = JSON.stringify(body);
    const generation = req.headers?.['auriga-observation-generation'];
    const observedGeneration =
      typeof generation === 'string' && /^[1-9][0-9]{0,15}$/.test(generation)
        ? Number(generation)
        : 0;
    const signature =
      span.slice(3, 35) + ':' + observedGeneration + ':' + body.ready;
    if (!seen.has(signature)) {
      if (seen.size >= 64) seen.delete(seen.keys().next().value);
      seen.set(signature, true);
      emitHealthObservation(ctx, {
        parent,
        span,
        instance: opts.instanceId ?? '',
        generation: observedGeneration,
        ready: body.ready,
        elapsed: Math.floor(performance.now() - started),
      });
    }
    res.writeHead(200, {
      'content-type': 'application/json; charset=utf-8',
      'cache-control': 'no-store',
      traceparent: span,
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
