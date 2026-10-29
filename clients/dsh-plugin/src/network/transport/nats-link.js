import * as natsCore from '@nats-io/nats-core';
import { publicationContext, logEvent } from '../../observability/index.js';
import { TransportError } from './errors.js';
import { GuardedSocket } from './guarded-socket.js';
import {
  assertPayload,
  pubWireBytes,
  resolveLimits,
  worstCaseBufferBound,
} from './limits.js';
import { SendQueue } from './send-queue.js';
import { createWsSocketFactory } from './ws-socket.js';
import { diagnosticErrno, socketDiagnosticErrno } from '../diagnostics.js';

/**
 * @typedef {{ jwt: string, nkeySeed?: string } | { token: string }} TicketAuth
 * @typedef {{
 *   url: string,
 *   auth: TicketAuth,
 *   upSubject: string,
 *   downSubject: string,
 *   inboxPrefix: string,
 *   epoch?: number,
 *   name?: string,
 *   limits?: Partial<import('./limits.js').Limits>,
 *   connectTimeoutMs?: number,
 *   pingIntervalMs?: number,
 *   createSocket?: (url: string) => any,
 *   nats?: typeof natsCore,
 *   onMessage?: (payload: Uint8Array, link: NatsLink) => void,
 *   onClosed?: (error: TransportError | undefined, link: NatsLink) => void,
 * }} LinkOptions
 */

/** @param {number} ms */
const sleep = (ms) =>
  new Promise((resolve) => {
    const timer = setTimeout(resolve, ms);
    timer.unref?.();
  });

/**
 * The authenticator runs inside the SDK's synchronous connect path: never async, never throws.
 * @param {TicketAuth} auth
 * @param {typeof natsCore} nats
 * @returns {(nonce?: string) => any}
 */
function buildAuthenticator(auth, nats) {
  let inner;
  try {
    if ('jwt' in auth) {
      inner = nats.jwtAuthenticator(
        auth.jwt,
        auth.nkeySeed === undefined
          ? undefined
          : new TextEncoder().encode(auth.nkeySeed),
      );
    } else if ('token' in auth) {
      inner = nats.tokenAuthenticator(auth.token);
    }
    // Dry run now: signing errors must surface here, not inside the SDK's connect path.
    inner?.('dry-run');
  } catch (cause) {
    throw new TransportError(
      'ticket_invalid',
      'ticket credentials are unusable',
      {
        cause,
      },
    );
  }
  if (!inner) {
    throw new TransportError(
      'ticket_invalid',
      'ticket has no usable credentials',
    );
  }
  return (nonce) => {
    try {
      return inner(nonce) ?? {};
    } catch {
      return {};
    }
  };
}

/** @param {Error | undefined | void} error */
function classifyClose(error) {
  if (!error)
    return new TransportError('connection_closed', 'connection closed');
  const name = error.constructor?.name ?? '';
  if (
    name === 'UserAuthenticationExpiredError' ||
    /authentication expired/i.test(error.message)
  ) {
    return new TransportError('ticket_expired', 'network ticket expired', {
      cause: error,
    });
  }
  if (
    name === 'AuthorizationError' ||
    /authorization|authentication/i.test(error.message)
  ) {
    return new TransportError('unauthorized', 'broker rejected the ticket', {
      cause: error,
    });
  }
  return new TransportError('connection_lost', error.message, { cause: error });
}

/**
 * One NATS-over-WSS connection with a provable send-side memory bound.
 * Single use: once closed the supervisor builds a new link (new ticket, new
 * epoch). The SDK is configured `reconnect: false`, so its reconnect path and
 * the unbounded outbound buffer it keeps while disconnected are never used.
 */
export class NatsLink {
  #opts;
  #nats;
  #limits;
  #queue;
  /** @type {GuardedSocket | undefined} */
  #socket;
  /** @type {natsCore.NatsConnection | undefined} */
  #nc;
  /** @type {natsCore.Subscription | undefined} */
  #sub;
  #state = 'idle'; // idle | connecting | ready | draining | closed
  /** @type {TransportError | undefined} */
  #failure;
  #pumping = false;
  /** @type {number | undefined} */
  #blockedSince;
  /** @type {number | undefined} */
  #lastBuffered;
  #published = 0;
  #subjectBytes;

  /** @param {LinkOptions} opts */
  constructor(opts) {
    this.#opts = opts;
    this.#nats = opts.nats ?? natsCore;
    this.#subjectBytes = new TextEncoder().encode(opts.upSubject).length;
    this.#limits = resolveLimits(opts.limits, {
      subjectBytes: this.#subjectBytes,
    });
    this.#queue = new SendQueue({
      maxMessages: this.#limits.queueMaxMessages,
      maxBytes: this.#limits.queueMaxBytes,
    });
  }

  get epoch() {
    return this.#opts.epoch ?? 0;
  }
  get state() {
    return this.#state;
  }
  get failure() {
    return this.#failure;
  }
  get limits() {
    return this.#limits;
  }
  get memoryBound() {
    return worstCaseBufferBound(this.#limits, {
      subjectBytes: this.#subjectBytes,
      maxLinks: 1,
    });
  }
  get stats() {
    return {
      state: this.#state,
      queueMessages: this.#queue.length,
      queueBytes: this.#queue.bytes,
      socketBuffered: this.#socket?.bufferedAmount ?? 0,
      peakSocketBuffered: this.#socket?.peakBuffered ?? 0,
      socketSentBytes: this.#socket?.sentBytes ?? 0,
      published: this.#published,
    };
  }

  async open() {
    if (this.#state !== 'idle') {
      throw new TransportError('invalid_state', 'link already used');
    }
    this.#state = 'connecting';
    const o = this.#opts;
    let socketErrno = 'none';
    let removeDiagnosticListener = () => {};
    try {
      const authenticator = buildAuthenticator(o.auth, this.#nats);
      // Default is production `ws` (has terminate). Injected factories that omit
      // terminate() fail closed below — never degrade to undici/browser close().
      const createSocket = o.createSocket ?? createWsSocketFactory();
      const nc = await this.#nats.wsconnect({
        servers: o.url,
        name: o.name ?? 'auriga-dsh',
        reconnect: false,
        timeout: o.connectTimeoutMs ?? 10_000,
        pingInterval: o.pingIntervalMs ?? 30_000,
        maxPingOut: 2,
        inboxPrefix: o.inboxPrefix,
        noEcho: true,
        authenticator,
        wsFactory: async (/** @type {string} */ url) => {
          if (this.#state !== 'connecting') {
            throw new TransportError('closed', 'link closed');
          }
          const inner = await createSocket(url);
          // NATS's WebSocket adapter drops event.error.code. Retain only one
          // allowlisted errno during this handshake, never the event or message.
          const capture = (/** @type {unknown} */ event) => {
            if (socketErrno === 'none')
              socketErrno = socketDiagnosticErrno(event);
          };
          try {
            removeDiagnosticListener();
            inner?.addEventListener?.('error', capture);
            removeDiagnosticListener = () =>
              inner?.removeEventListener?.('error', capture);
          } catch {
            /* Diagnostic support cannot change socket capability. */
          }
          if (typeof inner?.terminate !== 'function') {
            try {
              inner?.close?.();
            } catch {
              // ignore teardown of an incapable socket
            }
            throw new TransportError(
              'socket_incapable',
              'createSocket must return a socket with terminate()',
            );
          }
          this.#socket = new GuardedSocket(inner, {
            hardCapBytes: this.#limits.socketHardCapBytes,
            // SDK's synchronous initial CONNECT/PING must share one WS frame:
            // separate TLS records can lose Broker rejection bytes on reset.
            coalesceInitialWrites: true,
            initialBudgetBytes: Math.min(
              this.#limits.queueMaxBytes,
              pubWireBytes(this.#subjectBytes, this.#limits.maxMessageBytes),
            ),
            onOverflow: (info) =>
              this.#fail(
                new TransportError(
                  'socket_overflow',
                  `socket buffer ${info.buffered}+${info.frame} would exceed ${this.#limits.socketHardCapBytes} bytes`,
                  { outcome: 'unknown' },
                ),
              ),
            onClose: () => {
              // While connecting, wsconnect reports the real cause itself (e.g. the
              // broker's -ERR before it closes); tearing down here would hide it.
              if (this.#state === 'connecting') return;
              // Publishing already stopped (GuardedSocket.closed is checked
              // synchronously). Classification waits one macrotask so the SDK can
              // surface the broker's reason (e.g. User Authentication Expired).
              const timer = setTimeout(
                () =>
                  this.#fail(
                    new TransportError('connection_lost', 'socket closed'),
                  ),
                0,
              );
              timer.unref?.();
            },
          });
          // GuardedSocket implements the slice of WebSocket the SDK uses
          return { socket: /** @type {any} */ (this.#socket), encrypted: true };
        },
      });
      this.#nc = nc;
      // Staging uses the still-empty queue/handoff budget; release every native
      // writer's borrowed snapshot before application publishing can begin.
      await this.#socket?.initialWritesDone();
      if (this.#state !== 'connecting' || this.#socket?.closed) {
        throw (
          this.#failure ??
          new TransportError('connection_lost', 'socket closed during connect')
        );
      }
      nc.closed().then((error) => this.#fail(classifyClose(error)));
      this.#sub = nc.subscribe(o.downSubject, {
        callback: (error, msg) => this.#receive(error, msg),
      });
      await nc.flush();
      if (this.#state !== 'connecting') {
        throw (
          this.#failure ??
          new TransportError('closed', 'link closed during connect')
        );
      }
      this.#state = 'ready';
    } catch (error) {
      const failure =
        error instanceof TransportError
          ? error
          : classifyClose(/** @type {Error} */ (error));
      diagnosticErrno(failure, socketErrno);
      this.#fail(failure);
      throw failure;
    } finally {
      try {
        removeDiagnosticListener();
      } catch {
        /* Best effort diagnostics. */
      }
    }
  }

  /**
   * @param {Error | null} error
   * @param {natsCore.Msg} msg
   */
  #receive(error, msg) {
    if (this.#state !== 'ready') return;
    if (error) {
      this.#fail(
        new TransportError('subscription_error', error.message, {
          cause: error,
        }),
      );
      return;
    }
    if (msg.data.length > this.#limits.maxMessageBytes) {
      this.#fail(
        new TransportError(
          'protocol_error',
          'inbound message exceeds the payload limit',
        ),
      );
      return;
    }
    try {
      // copy: msg.data is a view into the SDK's receive buffer
      this.#opts.onMessage?.(msg.data.slice(), this);
    } catch {
      // consumer errors never break the connection
    }
  }

  /**
   * Queue one message for the up subject. Resolves once the frame is inside the
   * socket buffer (not once the broker has it). Rejects with `not_sent` before
   * that point; nothing is ever buffered across a disconnect.
   * @param {Uint8Array} payload
   * @returns {Promise<void>}
   */
  send(payload) {
    return new Promise((resolve, reject) => {
      if (this.#state !== 'ready' || this.#socket?.closed) {
        reject(new TransportError('not_connected', `link is ${this.#state}`));
        return;
      }
      try {
        assertPayload(payload, this.#limits);
        this.#queue.offer({
          payload,
          wireBytes: pubWireBytes(this.#subjectBytes, payload.byteLength),
          resolve,
          reject,
        });
      } catch (error) {
        reject(error);
        return;
      }
      void this.#pump();
    });
  }

  async #pump() {
    if (this.#pumping) return;
    this.#pumping = true;
    try {
      while (this.#state === 'ready' || this.#state === 'draining') {
        const head = this.#queue.peek();
        const socket = this.#socket;
        const nc = this.#nc;
        if (!head || !socket || !nc || socket.closed) break;
        const buffered = socket.bufferedAmount;
        if (buffered + head.wireBytes > this.#limits.socketHighWaterBytes) {
          const now = Date.now();
          if (
            this.#blockedSince === undefined ||
            buffered < (this.#lastBuffered ?? 0)
          ) {
            this.#blockedSince = now;
          }
          this.#lastBuffered = buffered;
          if (now - this.#blockedSince >= this.#limits.writeStallMs) {
            this.#fail(
              new TransportError(
                'write_stalled',
                `socket made no progress for ${this.#limits.writeStallMs} ms`,
                { outcome: 'unknown' },
              ),
            );
            break;
          }
          await sleep(this.#limits.pollIntervalMs);
          continue;
        }
        this.#blockedSince = undefined;
        this.#lastBuffered = undefined;
        this.#queue.shift();
        const context = publicationContext(head.payload);
        try {
          if (context) logEvent('INFO', 'NATS SDK publish attempted', context);
          nc.publish(this.#opts.upSubject, head.payload);
          if (context)
            logEvent('INFO', 'NATS SDK publish accepted locally', context);
        } catch (cause) {
          if (context)
            logEvent('ERROR', 'NATS SDK publish failed', context, cause);
          const failure = new TransportError(
            'publish_failed',
            'SDK refused the publish',
            {
              cause,
            },
          );
          head.reject(failure);
          this.#fail(failure);
          break;
        }
        this.#published += 1;
        head.resolve();
        // publish() queues the SDK flush as a microtask; yielding one tick makes
        // the frame reach the socket before the next bufferedAmount check, so
        // the SDK's own buffer never holds more than one frame.
        await Promise.resolve();
      }
    } finally {
      this.#pumping = false;
    }
  }

  /**
   * Stop accepting sends and unsubscribe, let already accepted messages reach
   * the socket and the broker (bounded by `timeoutMs`), then close.
   */
  async drainAndClose(timeoutMs = 5_000) {
    if (this.#state !== 'ready') return this.close();
    this.#state = 'draining';
    try {
      this.#sub?.unsubscribe();
    } catch {
      // closing anyway
    }
    const deadline = Date.now() + timeoutMs;
    while (
      this.#state === 'draining' &&
      this.#queue.length > 0 &&
      Date.now() < deadline
    ) {
      await sleep(this.#limits.pollIntervalMs);
    }
    if (this.#state === 'draining') {
      try {
        await Promise.race([
          this.#nc?.flush(),
          sleep(Math.max(0, deadline - Date.now())),
        ]);
      } catch {
        // fall through to close
      }
    }
    return this.close();
  }

  /** Immediate close. Queued, never-published messages are rejected as not_sent. */
  async close() {
    this.#fail(new TransportError('closed', 'link closed'), true);
    // A wedged socket may never report closed; do not wait for it forever.
    await Promise.race([
      Promise.all([
        this.#nc?.closed().catch(() => {}),
        this.#socket?.initialWritesDone(),
      ]),
      sleep(1_000),
    ]);
  }

  /**
   * @param {TransportError} failure
   * @param {boolean} [requested]
   */
  #fail(failure, requested = false) {
    if (this.#state === 'closed') return;
    const wasLive = this.#state === 'ready' || this.#state === 'draining';
    this.#state = 'closed';
    this.#failure = failure;
    for (const item of this.#queue.drain()) {
      item.reject(
        new TransportError(failure.code, failure.message, { cause: failure }),
      );
    }
    this.#socket?.terminate();
    const nc = this.#nc;
    if (nc && !nc.isClosed()) {
      // After terminate() the SDK's own close() only has a closed socket to wait on.
      nc.close().catch(() => {});
    }
    if (wasLive) this.#opts.onClosed?.(requested ? undefined : failure, this);
  }
}
