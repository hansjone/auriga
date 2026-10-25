/**
 * Transport failures are always explicit. `outcome` tells the caller whether
 * the message could have reached the broker:
 *  - 'not_sent'  : never handed to the socket, safe to retry after reconnect
 *  - 'unknown'   : handed to the socket, delivery cannot be confirmed
 */
export class TransportError extends Error {
  /**
   * @param {string} code
   * @param {string} message
   * @param {{ outcome?: 'not_sent' | 'unknown', cause?: unknown }} [detail]
   */
  constructor(code, message, detail = {}) {
    super(
      message,
      detail.cause === undefined ? undefined : { cause: detail.cause },
    );
    this.name = 'TransportError';
    this.code = code;
    this.outcome = detail.outcome ?? 'not_sent';
  }
}
