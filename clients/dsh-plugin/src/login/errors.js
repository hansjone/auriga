/** Local UI reasons, never wire error codes or server-provided messages.
 * @typedef {'network'|'unconfigured'|'expired'|'unavailable'|'failed'|'cancelled'|'platform'|'platformConfig'|'busy'|'limited'} LoginFailureReason */
export class LoginFailure extends Error {
  /** @param {LoginFailureReason} reason @param {unknown} [status] */
  constructor(reason, status) {
    super('Local sign-in failed');
    this.reason = reason;
    this.httpStatus =
      typeof status === 'number' &&
      Number.isInteger(status) &&
      status >= 400 &&
      status <= 599
        ? status
        : undefined;
  }
}

/** Interpret only finite classifications, never upstream messages or values.
 * @param {any} data @param {number} [status] */
export function wireFailure(data, status) {
  const code = data?.code;
  if (code === 'iam.invalid_credentials') return new LoginFailure('failed');
  if (['iam.login_tx_expired', 'common.unauthenticated'].includes(code))
    return new LoginFailure('expired');
  if (code === 'common.rate_limited') return new LoginFailure('limited');
  const diagnostic = data?.diagnostic;
  if (['dns', 'connection', 'tls', 'timeout'].includes(diagnostic?.category))
    return new LoginFailure('network');
  if (diagnostic?.category === 'busy') return new LoginFailure('busy');
  if (diagnostic?.category === 'configuration')
    return new LoginFailure('platformConfig');
  return new LoginFailure(
    'platform',
    diagnostic?.category
      ? diagnostic.category === 'platform_http'
        ? diagnostic.status
        : undefined
      : status,
  );
}

/** @param {unknown} error */
export function failureStatus(error) {
  return error instanceof LoginFailure ? error.httpStatus : undefined;
}

/** @param {unknown} error @returns {LoginFailureReason} */
export function failureReason(error) {
  return error instanceof LoginFailure ? error.reason : 'failed';
}
