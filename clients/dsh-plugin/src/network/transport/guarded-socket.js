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
  /** Wire bytes accepted by the real socket so far (diagnostics and tests). */
  sentBytes = 0;
  /** Highest bufferedAmount observed right after an accepted send. */
  peakBuffered = 0;
  overflowed = false;
  closed = false;

  /**
   * @param {any} inner WebSocket-like: send/close/bufferedAmount/addEventListener
   * @param {{ hardCapBytes: number, onOverflow: (info: { buffered: number, frame: number }) => void, onClose: () => void }} opts
   */
  constructor(inner, opts) {
    this.#inner = inner;
    this.#hardCap = opts.hardCapBytes;
    this.#onOverflow = opts.onOverflow;
    this.#onClose = opts.onClose;
    // addEventListener keeps our listeners independent of the SDK's on* slots.
    inner.addEventListener('close', () => this.#markClosed());
    inner.addEventListener('error', () => {
      // Node emits close after error; an error alone must also stop publishing.
      this.#markClosed();
    });
  }

  #markClosed() {
    if (this.closed) return;
    this.closed = true;
    this.#onClose();
  }

  get bufferedAmount() {
    return this.#inner.bufferedAmount;
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

  /** @param {ArrayBuffer | Uint8Array} data */
  send(data) {
    if (this.overflowed || this.closed) return;
    const frame = data.byteLength;
    const buffered = this.#inner.bufferedAmount;
    if (buffered + frame > this.#hardCap) {
      this.overflowed = true;
      this.terminate();
      this.#onOverflow({ buffered, frame });
      return;
    }
    this.#inner.send(data);
    this.sentBytes += frame;
    const after = this.#inner.bufferedAmount;
    if (after > this.peakBuffered) this.peakBuffered = after;
  }

  /**
   * @param {number} [code]
   * @param {string} [reason]
   */
  close(code, reason) {
    this.#inner.close(code, reason);
  }

  /** Immediate teardown when the implementation has one (ws), else a normal close. */
  terminate() {
    try {
      if (typeof this.#inner.terminate === 'function') this.#inner.terminate();
      else this.#inner.close(1008, 'buffer limit');
    } catch {
      // already closing
    }
  }
}
