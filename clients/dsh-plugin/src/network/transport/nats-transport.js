import { TransportError } from './errors.js';
import { NatsLink } from './nats-link.js';
import { resolveLimits, worstCaseBufferBound } from './limits.js';

/**
 * @typedef {{ auth: import('./nats-link.js').TicketAuth, expiresAtMs: number }} Ticket
 * @typedef {{
 *   state: 'idle' | 'connecting' | 'ready' | 'reconnecting' | 'stopped',
 *   epoch: number,
 *   reason?: string,
 *   error?: unknown,
 * }} TransportState
 * @typedef {{
 *   url: string,
 *   upSubject: string,
 *   downSubject: string,
 *   inboxPrefix: string,
 *   getTicket: (ctx: { reason: 'start' | 'refresh' | 'lost', attempt: number }) => Promise<Ticket>,
 *   onMessage?: (payload: Uint8Array, epoch: number) => void,
 *   onState?: (state: TransportState) => void,
 *   limits?: Partial<import('./limits.js').Limits>,
 *   createSocket?: (url: string) => any,
 *   nats?: typeof import('@nats-io/nats-core'),
 *   name?: string,
 *   connectTimeoutMs?: number,
 *   pingIntervalMs?: number,
 *   refreshLeadMs?: [number, number],
 *   backoff?: { baseMs: number, maxMs: number, stableMs: number },
 *   drainTimeoutMs?: number,
 *   random?: () => number,
 * }} TransportOptions
 */

/**
 * Supervisor around {@link NatsLink}. It owns everything asynchronous that the
 * SDK's synchronous auth/reconnect callbacks must not do: redeeming a fresh
 * ticket, building the replacement connection, backing off and swapping.
 *
 *  - unexpected loss  -> new ticket, new link (the SDK never reconnects itself)
 *  - ticket nearing expiry (random lead window) -> new link first, then the old
 *    one drains what it accepted and closes; at most two links exist
 *  - while no link is ready, send() fails at once with `not_connected`
 */
export class NatsTransport {
  #o;
  #random;
  #backoff;
  #lead;
  #state = 'idle';
  #epoch = 0;
  /** @type {NatsLink | undefined} */
  #current;
  #links = new Set();
  /** @type {Promise<void> | undefined} */
  #establishing;
  /** @type {ReturnType<typeof setTimeout> | undefined} */
  #refreshTimer;
  #sleepers = new Set();
  #stopped = false;
  #liveSince = 0;
  #failedAttempts = 0;

  /** @param {TransportOptions} options */
  constructor(options) {
    this.#o = options;
    this.#random = options.random ?? Math.random;
    this.#backoff = options.backoff ?? {
      baseMs: 250,
      maxMs: 30_000,
      stableMs: 10_000,
    };
    this.#lead = options.refreshLeadMs ?? [60_000, 120_000];
  }

  get state() {
    return this.#state;
  }
  get epoch() {
    return this.#epoch;
  }
  get link() {
    return this.#current;
  }
  /** Live links, newest last (two only while a refresh is draining the old one). */
  get links() {
    return [...this.#links];
  }
  get memoryBound() {
    const subjectBytes = new TextEncoder().encode(this.#o.upSubject).length;
    return worstCaseBufferBound(
      resolveLimits(this.#o.limits, { subjectBytes }),
      {
        subjectBytes,
        maxLinks: 2,
      },
    );
  }

  /** Resolves when the first link is ready (or rejects if stopped first). */
  start() {
    if (this.#state !== 'idle') {
      return Promise.reject(
        new TransportError('invalid_state', 'transport already started'),
      );
    }
    this.#emit('connecting', 'start');
    return this.#ensureLink('start').then(() => {});
  }

  /**
   * @param {Uint8Array} payload
   * @returns {Promise<void>} resolved when the frame is inside the socket buffer
   */
  send(payload) {
    const link = this.#current;
    if (!link || this.#state !== 'ready') {
      return Promise.reject(
        new TransportError('not_connected', `transport is ${this.#state}`),
      );
    }
    return link.send(payload);
  }

  async stop() {
    if (this.#stopped) return;
    this.#stopped = true;
    clearTimeout(this.#refreshTimer);
    for (const wake of this.#sleepers) wake();
    this.#sleepers.clear();
    this.#current = undefined;
    this.#emit('stopped', 'stop');
    await Promise.all([...this.#links].map((link) => link.close()));
  }

  /**
   * @param {TransportState['state']} state
   * @param {string} reason
   * @param {unknown} [error]
   */
  #emit(state, reason, error) {
    this.#state = state;
    this.#o.onState?.({ state, epoch: this.#epoch, reason, error });
  }

  /** @param {number} ms */
  #sleep(ms) {
    return new Promise((resolve) => {
      const wake = () => {
        clearTimeout(timer);
        this.#sleepers.delete(wake);
        resolve(undefined);
      };
      const timer = setTimeout(wake, ms);
      timer.unref?.();
      this.#sleepers.add(wake);
    });
  }

  /** @param {number} attempt */
  #backoffMs(attempt) {
    const { baseMs, maxMs } = this.#backoff;
    return Math.floor(
      Math.min(maxMs, baseMs * 2 ** attempt) * (0.5 + this.#random() / 2),
    );
  }

  /** One establishing loop at a time; it retries with backoff until a link is ready or stop(). */
  /** @param {'start' | 'refresh' | 'lost'} reason */
  #ensureLink(reason) {
    if (!this.#establishing) {
      this.#establishing = this.#establish(reason).finally(() => {
        this.#establishing = undefined;
      });
    }
    return this.#establishing;
  }

  /** @param {'start' | 'refresh' | 'lost'} reason */
  async #establish(reason) {
    let attempt = this.#failedAttempts;
    while (!this.#stopped) {
      /** @type {NatsLink | undefined} */
      let link;
      try {
        const ticket = await this.#o.getTicket({ reason, attempt });
        if (this.#stopped) return;
        if (!Number.isFinite(ticket?.expiresAtMs)) {
          throw new TransportError('ticket_invalid', 'ticket has no expiry');
        }
        const issuedAt = Date.now();
        const o = this.#o;
        link = new NatsLink({
          url: o.url,
          auth: ticket.auth,
          upSubject: o.upSubject,
          downSubject: o.downSubject,
          inboxPrefix: o.inboxPrefix,
          epoch: this.#epoch + 1,
          name: o.name,
          limits: o.limits,
          connectTimeoutMs: o.connectTimeoutMs,
          pingIntervalMs: o.pingIntervalMs,
          createSocket: o.createSocket,
          nats: o.nats,
          onMessage: (payload, from) => o.onMessage?.(payload, from.epoch),
          onClosed: (error, closed) => this.#linkClosed(error, closed),
        });
        this.#links.add(link);
        await link.open();
        if (this.#stopped) {
          await link.close();
          return;
        }
        this.#adopt(link, ticket, issuedAt, reason);
        return;
      } catch (error) {
        if (link) this.#links.delete(link);
        if (this.#stopped) return;
        attempt += 1;
        this.#failedAttempts = attempt;
        this.#o.onState?.({
          state: this.#state === 'ready' ? 'ready' : 'reconnecting',
          epoch: this.#epoch,
          reason:
            error instanceof TransportError ? error.code : 'connect_failed',
          error,
        });
        await this.#sleep(this.#backoffMs(attempt));
      }
    }
  }

  /**
   * @param {NatsLink} link
   * @param {Ticket} ticket
   * @param {number} issuedAt
   * @param {'start' | 'refresh' | 'lost'} reason
   */
  #adopt(link, ticket, issuedAt, reason) {
    const previous = this.#current;
    this.#epoch = link.epoch;
    this.#current = link;
    this.#liveSince = Date.now();
    this.#emit('ready', reason);
    clearTimeout(this.#refreshTimer);
    const validity = ticket.expiresAtMs - issuedAt;
    const [lo, hi] = this.#lead;
    let delay = validity - (lo + this.#random() * (hi - lo));
    if (delay < validity * 0.1) delay = validity * 0.1;
    this.#refreshTimer = setTimeout(
      () => {
        if (!this.#stopped && this.#current === link)
          void this.#ensureLink('refresh');
      },
      Math.max(0, delay),
    );
    this.#refreshTimer.unref?.();
    if (previous && previous !== link) {
      void previous.drainAndClose(this.#o.drainTimeoutMs ?? 5_000);
    }
  }

  /**
   * @param {TransportError | undefined} error
   * @param {NatsLink} link
   */
  #linkClosed(error, link) {
    this.#links.delete(link);
    if (this.#stopped || link !== this.#current) return;
    this.#current = undefined;
    clearTimeout(this.#refreshTimer);
    // A link that died young counts as a failed attempt so a flapping broker is backed off.
    const lived = Date.now() - this.#liveSince;
    this.#failedAttempts =
      lived >= this.#backoff.stableMs ? 0 : this.#failedAttempts + 1;
    this.#emit('reconnecting', error?.code ?? 'closed', error);
    void (async () => {
      await this.#sleep(this.#backoffMs(Math.max(0, this.#failedAttempts - 1)));
      if (!this.#stopped) await this.#ensureLink('lost');
    })();
  }
}
