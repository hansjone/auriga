export class PlatformError extends Error {
  /** @param {number} status @param {string} [code] */
  constructor(status, code) {
    super('Auriga DSH platform request failed');
    this.status = status;
    this.code = code;
  }
}
