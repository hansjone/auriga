import WebSocket from 'ws';
import { newTraceparent, logEvent } from '../../observability/index.js';
import { normalizeOrigin } from '../../settings/store.js';

/**
 * Production createSocket for NATS-over-WSS.
 * Returns a `ws` socket that always exposes `terminate()` so GuardedSocket can
 * tear down a stalled write without waiting for a graceful close handshake.
 *
 * @param {{
 *   ca?: string | Buffer | Array<string | Buffer>,
 *   rejectUnauthorized?: boolean,
 *   origin?: string,
 * }} [tls]
 * @returns {(url: string) => import('ws').WebSocket}
 */
export function createWsSocketFactory(tls = {}) {
  const rejectUnauthorized = tls.rejectUnauthorized ?? true;
  // Only installation-owned configuration calls this factory. Reject header
  // injection before WebSocket can normalize it; never infer from the WSS host.
  if (tls.origin !== undefined && /[\r\n\0]/.test(tls.origin))
    throw new Error('invalidOrigin');
  const origin =
    tls.origin === undefined ? undefined : normalizeOrigin(tls.origin);
  return (url) => {
    const parent = newTraceparent();
    logEvent('INFO', 'WSS connection started', {
      trace_id: parent.slice(3, 35),
      path: new URL(url).pathname,
    });
    return new WebSocket(url, {
      headers: { traceparent: parent },
      rejectUnauthorized,
      ...(tls.ca !== undefined ? { ca: tls.ca } : {}),
      ...(origin !== undefined ? { origin } : {}),
    });
  };
}
