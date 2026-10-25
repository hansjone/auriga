import { TransportError } from '../transport/errors.js';
import { SendQueue } from '../transport/send-queue.js';
import { pubWireBytes } from '../transport/limits.js';

/**
 * In-process duplex that preserves #128 hard buffer ceilings on each end.
 * Used as a stand-in for NatsTransport in closed-loop tests (no real WSS).
 *
 * @typedef {{
 *   send(payload: Uint8Array): Promise<void>,
 *   onMessage(handler: (payload: Uint8Array, epoch: number) => void): () => void,
 *   stop(): Promise<void>,
 *   get state(): string,
 *   get epoch(): number,
 * }} TransportLike
 */

/**
 * @param {{
 *   queueMaxMessages?: number,
 *   queueMaxBytes?: number,
 *   subjectBytes?: number,
 * }} [limits]
 * @returns {{ a: TransportLike, b: TransportLike }}
 */
export function createMemoryPipe(limits = {}) {
  const subjectBytes = limits.subjectBytes ?? 24;
  const maxMessages = limits.queueMaxMessages ?? 64;
  const maxBytes = limits.queueMaxBytes ?? 256 * 1024;

  /** @type {((p: Uint8Array, e: number) => void) | null} */
  let handlerA = null;
  /** @type {((p: Uint8Array, e: number) => void) | null} */
  let handlerB = null;
  let epochA = 1;
  let epochB = 1;
  let stoppedA = false;
  let stoppedB = false;
  const queueA = new SendQueue({ maxMessages, maxBytes });
  const queueB = new SendQueue({ maxMessages, maxBytes });

  /**
   * @param {'a'|'b'} from
   * @param {Uint8Array} payload
   */
  function send(from, payload) {
    const stopped = from === 'a' ? stoppedA : stoppedB;
    const queue = from === 'a' ? queueA : queueB;
    const peerHandler = from === 'a' ? handlerB : handlerA;
    const peerEpoch = from === 'a' ? epochB : epochA;
    if (stopped) {
      return Promise.reject(
        new TransportError('not_connected', 'pipe stopped'),
      );
    }
    const wireBytes = pubWireBytes(subjectBytes, payload.byteLength);
    try {
      queue.offer({ wireBytes, payload });
    } catch (e) {
      return Promise.reject(e);
    }
    // Only drain when a peer is listening; otherwise the queue retains frames
    // (slow/absent consumer) so #128 ceilings still refuse.
    if (peerHandler) {
      queueMicrotask(() => {
        while (queue.length > 0) {
          const item = /** @type {{ payload: Uint8Array }} */ (queue.shift());
          peerHandler(item.payload, peerEpoch);
        }
      });
    }
    return Promise.resolve();
  }

  /** @param {'a'|'b'} side */
  function end(side) {
    return {
      get state() {
        const stopped = side === 'a' ? stoppedA : stoppedB;
        return stopped ? 'stopped' : 'ready';
      },
      get epoch() {
        return side === 'a' ? epochA : epochB;
      },
      setEpoch(n) {
        if (side === 'a') epochA = n;
        else epochB = n;
      },
      send(payload) {
        return send(side, payload);
      },
      onMessage(handler) {
        if (side === 'a') handlerA = handler;
        else handlerB = handler;
        // Flush frames the peer already sent into our receive queue.
        const inbound = side === 'a' ? queueB : queueA;
        const myEpoch = side === 'a' ? epochA : epochB;
        queueMicrotask(() => {
          while (inbound.length > 0) {
            const item = /** @type {{ payload: Uint8Array }} */ (
              inbound.shift()
            );
            handler(item.payload, myEpoch);
          }
        });
        return () => {
          if (side === 'a' && handlerA === handler) handlerA = null;
          if (side === 'b' && handlerB === handler) handlerB = null;
        };
      },
      async stop() {
        if (side === 'a') stoppedA = true;
        else stoppedB = true;
      },
    };
  }

  return { a: end('a'), b: end('b') };
}
