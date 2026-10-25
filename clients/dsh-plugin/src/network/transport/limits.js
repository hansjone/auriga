import { TransportError } from './errors.js';

/**
 * Hard memory limits of one NATS link. All byte figures are wire bytes
 * (`PUB <subject> <len>\r\n<payload>\r\n`), which is what the socket buffers.
 */
export const DEFAULT_LIMITS = Object.freeze({
  /** Largest payload accepted from callers and from the broker (protocol: 64 KiB). */
  maxMessageBytes: 64 * 1024,
  /** Application send queue, messages. */
  queueMaxMessages: 256,
  /** Application send queue, wire bytes. */
  queueMaxBytes: 1024 * 1024,
  /** Messages are only handed to the SDK while socket.bufferedAmount + frame stays below this. */
  socketHighWaterBytes: 256 * 1024,
  /** socket.send() is refused (and the link failed) if bufferedAmount + frame would exceed this. */
  socketHardCapBytes: 512 * 1024,
  /** Head of queue blocked by a non-draining socket for this long fails the link. */
  writeStallMs: 10_000,
  /** Poll interval while the socket is above the high-water mark. */
  pollIntervalMs: 10,
});

/** @typedef {typeof DEFAULT_LIMITS} Limits */

/** @param {number} n */
const COUNT_DIGITS = (n) => String(n).length;

/**
 * Exact wire size of an uncompressed `PUB` without headers.
 * @param {number} subjectBytes
 * @param {number} payloadBytes
 */
export function pubWireBytes(subjectBytes, payloadBytes) {
  // "PUB " + subject + " " + len + "\r\n" + payload + "\r\n"
  return (
    4 + subjectBytes + 1 + COUNT_DIGITS(payloadBytes) + 2 + payloadBytes + 2
  );
}

/**
 * @param {Partial<Limits>} [overrides]
 * @param {{ subjectBytes: number }} ctx
 * @returns {Limits}
 */
export function resolveLimits(overrides = {}, ctx = { subjectBytes: 0 }) {
  const limits = { ...DEFAULT_LIMITS, ...overrides };
  for (const [key, value] of Object.entries(limits)) {
    if (!Number.isSafeInteger(value) || value <= 0) {
      throw new TypeError(`transport limit ${key} must be a positive integer`);
    }
  }
  const maxWire = pubWireBytes(ctx.subjectBytes, limits.maxMessageBytes);
  if (limits.queueMaxBytes < maxWire) {
    throw new TypeError(
      'queueMaxBytes must hold at least one maximum-size message',
    );
  }
  if (limits.socketHighWaterBytes < maxWire) {
    throw new TypeError(
      'socketHighWaterBytes must admit at least one maximum-size message',
    );
  }
  if (limits.socketHardCapBytes < limits.socketHighWaterBytes + 4096) {
    throw new TypeError(
      'socketHardCapBytes must leave at least 4 KiB above the high-water mark for SDK control frames',
    );
  }
  return limits;
}

/**
 * Proven worst-case bytes held on the send path of one link, and of the whole
 * transport (a refresh keeps the old link alive only to drain what it already
 * accepted, so at most two links exist).
 *
 *  queue   <= queueMaxBytes                      (SendQueue refuses beyond it)
 *  handoff <= one maximum frame                  (the pump publishes one message,
 *             then yields one microtask, which is when the SDK flushes it)
 *  socket  <= socketHardCapBytes                 (GuardedSocket refuses any send()
 *             that would push bufferedAmount past it; it is the only path to the
 *             real socket)
 *
 * The SDK's own `outbound` buffer is unbounded while the SDK believes it is
 * disconnected, so the transport never publishes in that state: publishing is
 * gated synchronously on the socket's own open/close events.
 *
 * @param {Limits} limits
 * @param {{ subjectBytes: number, maxLinks?: number }} ctx
 */
export function worstCaseBufferBound(limits, ctx) {
  const maxWire = pubWireBytes(ctx.subjectBytes, limits.maxMessageBytes);
  const perLink = {
    queue: limits.queueMaxBytes,
    handoff: maxWire,
    socket: limits.socketHardCapBytes,
  };
  const perLinkTotal = perLink.queue + perLink.handoff + perLink.socket;
  const maxLinks = ctx.maxLinks ?? 2;
  return { perLink, perLinkTotal, maxLinks, total: perLinkTotal * maxLinks };
}

/**
 * @param {unknown} payload
 * @param {Limits} limits
 */
export function assertPayload(payload, limits) {
  if (!(payload instanceof Uint8Array)) {
    throw new TransportError('invalid_payload', 'payload must be a Uint8Array');
  }
  if (payload.byteLength > limits.maxMessageBytes) {
    throw new TransportError(
      'message_too_large',
      `payload ${payload.byteLength} exceeds ${limits.maxMessageBytes} bytes`,
    );
  }
}
