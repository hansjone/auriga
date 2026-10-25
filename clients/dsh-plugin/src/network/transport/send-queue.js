import { TransportError } from './errors.js';

/**
 * @typedef {{
 *   wireBytes: number,
 *   payload: Uint8Array,
 *   resolve: () => void,
 *   reject: (error: Error) => void,
 * }} QueueItem
 */

/**
 * FIFO with a message-count and a byte ceiling. `offer` refuses instead of
 * growing, so memory cannot exceed `maxBytes` whatever the producer does.
 */
export class SendQueue {
  /** @type {(QueueItem | undefined)[]} */
  #items = [];
  #head = 0;
  #bytes = 0;

  /**
   * @param {{ maxMessages: number, maxBytes: number }} limits
   */
  constructor(limits) {
    this.maxMessages = limits.maxMessages;
    this.maxBytes = limits.maxBytes;
  }

  get length() {
    return this.#items.length - this.#head;
  }

  get bytes() {
    return this.#bytes;
  }

  /**
   * @param {QueueItem} item
   */
  offer(item) {
    if (
      this.length + 1 > this.maxMessages ||
      this.#bytes + item.wireBytes > this.maxBytes
    ) {
      throw new TransportError(
        'queue_full',
        `send queue full (${this.length} messages, ${this.#bytes} bytes)`,
      );
    }
    this.#items.push(item);
    this.#bytes += item.wireBytes;
  }

  peek() {
    return this.#items[this.#head];
  }

  shift() {
    const item = this.#items[this.#head];
    if (item === undefined) return undefined;
    this.#items[this.#head] = undefined;
    this.#head += 1;
    this.#bytes -= item.wireBytes;
    if (this.#head > 64 && this.#head * 2 > this.#items.length) {
      this.#items = this.#items.slice(this.#head);
      this.#head = 0;
    }
    return item;
  }

  /** Remove everything, returning the items for rejection. */
  drain() {
    const rest = /** @type {QueueItem[]} */ (this.#items.slice(this.#head));
    this.#items = [];
    this.#head = 0;
    this.#bytes = 0;
    return rest;
  }
}
