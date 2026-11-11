import { sendProblem } from './problem.js';

/** Installed inside the signed identity door: a live browser identity alone must
 * not keep a revoked/expired managed device usable. Message readiness is separate.
 * @param {import('node:http').Server} server
 * @param {()=>boolean} available
 * @param {()=>boolean} [revoked] Proven material revocation or expiry only.
 */
export function installManagedSessionGuard(
  server,
  available,
  revoked = () => false,
) {
  const original = server.emit;
  /** @type {Set<import('node:net').Socket>} */
  const sockets = new Set();
  const message = (/** @type {any} */ req) => {
    const chinese = String(req.headers?.['accept-language'] ?? '').startsWith(
      'zh',
    );
    if (revoked())
      return chinese
        ? '实例会话已失效，请从门户重新进入'
        : 'Instance session has expired. Re-enter from the portal.';
    return chinese ? '连接中断，正在重试' : 'Connection interrupted. Retrying.';
  };
  const guarded = function (
    /** @type {string|symbol} */ event,
    /** @type {any[]} */ ...args
  ) {
    if (event === 'request') {
      const [req, res] = args;
      const path = req.url?.split('?')[0];
      const diagnostic =
        (['GET', 'HEAD'].includes(req.method) && path === '/_auriga/health') ||
        (req.method === 'GET' &&
          [
            '/auriga/local-login/status',
            '/auriga/settings/state',
            '/auriga/settings/view.js',
          ].includes(path));
      if (!diagnostic && !available()) {
        if (req.method === 'GET' && path === '/') {
          res.writeHead(401, {
            'content-type': 'text/html; charset=utf-8',
            'cache-control': 'no-store',
            'content-security-policy': "default-src 'none'",
          });
          res.end(
            '<!doctype html><meta charset="utf-8"><p>' + message(req) + '</p>',
          );
        } else sendProblem(res, 401, 'common.unauthenticated', message(req));
        return false;
      }
    }
    if (event === 'upgrade') {
      const socket = args[1];
      if (!available()) {
        socket.destroy();
        return false;
      }
      sockets.add(socket);
      socket.once('close', () => sockets.delete(socket));
    }
    return Reflect.apply(original, server, [event, ...args]);
  };
  server.emit = guarded;
  const timer = setInterval(() => {
    if (!available()) for (const socket of sockets) socket.destroy();
  }, 1000);
  timer.unref?.();
  return () => {
    clearInterval(timer);
    if (server.emit === guarded) server.emit = original;
    sockets.clear();
  };
}
