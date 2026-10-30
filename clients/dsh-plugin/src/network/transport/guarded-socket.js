/* global queueMicrotask */
/** @typedef {ArrayBuffer & {transfer(byteLength?:number): ArrayBuffer}} TransferBuffer */
/**
 * The only object the NATS SDK ever sees as its WebSocket. `WebSocket.send`
 * has no byte ceiling of its own, so every frame passes this check first:
 * if it would push `bufferedAmount` past the hard cap the frame is dropped,
 * the socket is torn down and `onOverflow` fires. That makes the cap a
 * property of the code path, not of the SDK's behaviour.
 */
export class GuardedSocket {
  #inner;
  #hardCap;
  #onOverflow;
  #onClose;
  #closing = false;
  #initialWrites;
  #initialBudget;
  /** @type {TransferBuffer | undefined} */
  #initial;
  /** @type {Set<TransferBuffer>} */
  #initialInFlight = new Set();
  /** @type {Set<()=>void>} */
  #initialWaiters = new Set();
  /** Wire bytes accepted by the real socket so far (diagnostics and tests). */
  sentBytes = 0;
  /** Highest bufferedAmount observed right after an accepted send. */
  peakBuffered = 0;
  overflowed = false;
  closed = false;

  /**
   * @param {any} inner WebSocket-like: send/close/bufferedAmount/addEventListener
   * @param {{ hardCapBytes: number, coalesceInitialWrites?: boolean, initialBudgetBytes?:number, onOverflow: (info: { buffered: number, frame: number }) => void, onClose: () => void }} opts
   */
  constructor(inner, opts) {
    this.#inner = inner;
    this.#hardCap = opts.hardCapBytes;
    this.#onOverflow = opts.onOverflow;
    this.#onClose = opts.onClose;
    this.#initialWrites =
      !!opts.coalesceInitialWrites &&
      this.#inner.send.length >= 3 &&
      typeof Reflect.get(ArrayBuffer.prototype, 'transfer') === 'function';
    this.#initialBudget = Math.min(
      opts.initialBudgetBytes ?? opts.hardCapBytes,
      opts.hardCapBytes,
    );
    // addEventListener keeps our listeners independent of the SDK's on* slots.
    inner.addEventListener('close', () => {
      this.#markClosed();
      // A closed native socket cannot retain a pending writer's data anymore.
      for (const bytes of this.#initialInFlight) bytes.transfer(0);
      this.#initialInFlight.clear();
      this.#notifyInitialDone();
    });
    inner.addEventListener('error', () => {
      // Node emits close after error; an error alone must also stop publishing.
      this.#markClosed();
    });
  }

  #markClosed() {
    if (this.closed) return;
    this.closed = true;
    this.#closing = true;
    this.#discardInitial();
    this.#onClose();
  }

  get bufferedAmount() {
    return this.#inner.bufferedAmount + (this.#initial?.byteLength ?? 0);
  }
  get readyState() {
    return this.#inner.readyState;
  }
  get binaryType() {
    return this.#inner.binaryType;
  }
  set binaryType(value) {
    this.#inner.binaryType = value;
  }
  get onopen() {
    return this.#inner.onopen;
  }
  set onopen(fn) {
    this.#inner.onopen = fn;
  }
  get onmessage() {
    return this.#inner.onmessage;
  }
  set onmessage(fn) {
    this.#inner.onmessage = fn;
  }
  get onerror() {
    return this.#inner.onerror;
  }
  set onerror(fn) {
    this.#inner.onerror = fn;
  }
  get onclose() {
    return this.#inner.onclose;
  }
  set onclose(fn) {
    this.#inner.onclose = fn;
  }

  /** @param {ArrayBuffer | Uint8Array} data @returns {void} */
  send(data) {
    if (this.overflowed || this.closed || this.#closing) return;
    const frame = data.byteLength;
    const buffered = this.bufferedAmount;
    if (
      this.#initial &&
      (buffered + frame > this.#hardCap ||
        this.#initial.byteLength + frame > this.#initialBudget)
    ) {
      // Preserve the original per-write admission for a legal pair too large
      // to merge. This fallback adds neither a budget nor a new frame limit.
      this.#flushInitial();
      return this.send(data);
    }
    if (buffered + frame > this.#hardCap) {
      this.overflowed = true;
      this.terminate();
      this.#onOverflow({ buffered, frame });
      return;
    }
    if (this.#initialWrites) {
      if (frame > this.#initialBudget) {
        this.#initialWrites = false;
        return this.send(data);
      }
      const bytes =
        data instanceof Uint8Array
          ? new Uint8Array(data.buffer, data.byteOffset, data.byteLength)
          : new Uint8Array(data);
      if (this.#initial) {
        const offset = this.#initial.byteLength;
        this.#initial = /** @type {TransferBuffer} */ (
          this.#initial.transfer(offset + frame)
        );
        new Uint8Array(this.#initial).set(bytes, offset);
        this.peakBuffered = Math.max(this.peakBuffered, this.bufferedAmount);
        this.#flushInitial();
      } else {
        // Node 22 transfer detaches the old backing storage. Its possible copy
        // peak (old+new <= 2*initialBudget) is accounted before application ready.
        this.#initial = /** @type {TransferBuffer} */ (new ArrayBuffer(frame));
        new Uint8Array(this.#initial).set(bytes);
        this.peakBuffered = Math.max(this.peakBuffered, this.bufferedAmount);
        queueMicrotask(() => {
          try {
            this.#flushInitial();
          } catch {
            this.terminate();
          }
        });
      }
      return;
    }
    this.#inner.send(data);
    this.sentBytes += frame;
    const after = this.#inner.bufferedAmount;
    if (after > this.peakBuffered) this.peakBuffered = after;
  }

  #discardInitial() {
    this.#initial?.transfer(0);
    this.#initial = undefined;
    this.#initialWrites = false;
    this.#notifyInitialDone();
  }

  #notifyInitialDone() {
    if (this.#initial || this.#initialInFlight.size) return;
    for (const done of this.#initialWaiters) done();
    this.#initialWaiters.clear();
  }

  /** @returns {Promise<void>} */
  initialWritesDone() {
    if (!this.#initial && !this.#initialInFlight.size) return Promise.resolve();
    return new Promise((resolve) => this.#initialWaiters.add(resolve));
  }

  #flushInitial() {
    const bytes = this.#initial;
    this.#initial = undefined;
    this.#initialWrites = false;
    if (!bytes || this.closed || this.#closing) return;
    const frame = bytes.byteLength,
      buffered = this.#inner.bufferedAmount;
    if (buffered + frame > this.#hardCap) {
      bytes.transfer(0);
      this.overflowed = true;
      this.terminate();
      this.#onOverflow({ buffered, frame });
      return;
    }
    this.#initialInFlight.add(bytes);
    const complete = (/** @type {unknown} */ error) => {
      if (this.#initialInFlight.delete(bytes)) bytes.transfer(0);
      if (error) this.terminate();
      this.#notifyInitialDone();
    };
    try {
      this.#inner.send(new Uint8Array(bytes), {}, complete);
    } catch (error) {
      complete(error);
      throw error;
    }
    this.sentBytes += frame;
    this.peakBuffered = Math.max(this.peakBuffered, this.bufferedAmount);
  }

  /**
   * @param {number} [code]
   * @param {string} [reason]
   */
  close(code, reason) {
    this.#closing = true;
    this.#discardInitial();
    this.#inner.close(code, reason);
  }

  /**
   * Immediate teardown. Production sockets must expose `terminate()` (enforced
   * by NatsLink at connect); there is no graceful-close fallback.
   */
  terminate() {
    this.#closing = true;
    this.#discardInitial();
    try {
      this.#inner.terminate();
    } catch {
      // already closing
    }
  }
}
