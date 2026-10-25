import WebSocket from 'ws';

/**
 * Production createSocket for NATS-over-WSS.
 * Returns a `ws` socket that always exposes `terminate()` so GuardedSocket can
 * tear down a stalled write without waiting for a graceful close handshake.
 *
 * @param {{
 *   ca?: string | Buffer | Array<string | Buffer>,
 *   rejectUnauthorized?: boolean,
 * }} [tls]
 * @returns {(url: string) => import('ws').WebSocket}
 */
export function createWsSocketFactory(tls = {}) {
  const rejectUnauthorized = tls.rejectUnauthorized ?? true;
  return (url) =>
    new WebSocket(url, {
      rejectUnauthorized,
      ...(tls.ca !== undefined ? { ca: tls.ca } : {}),
    });
}
