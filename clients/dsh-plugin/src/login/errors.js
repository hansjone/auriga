/** Local UI reasons, never wire error codes or server-provided messages.
 * @typedef {'network'|'unconfigured'|'expired'|'unavailable'|'failed'|'cancelled'} LoginFailureReason */
export class LoginFailure extends Error {
  /** @param {LoginFailureReason} reason */
  constructor(reason) {
    super('Local sign-in failed');
    this.reason = reason;
  }
}

/** @param {unknown} error @returns {LoginFailureReason} */
export function failureReason(error) {
  return error instanceof LoginFailure ? error.reason : 'failed';
}
