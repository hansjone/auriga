/**
 * Session-layer failures (application frames over an already-open transport).
 * Distinct from TransportError: these are protocol/epoch/mapping issues.
 */
export class SessionError extends Error {
  /**
   * @param {string} code
   * @param {string} message
   * @param {{ cause?: unknown }} [detail]
   */
  constructor(code, message, detail = {}) {
    super(
      message,
      detail.cause === undefined ? undefined : { cause: detail.cause },
    );
    this.name = 'SessionError';
    this.code = code;
  }
}
