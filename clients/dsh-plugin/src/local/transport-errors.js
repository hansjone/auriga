// Only failures proven to precede an HTTP write may release a rotating-token
// fence. Resets, response timeouts and mixed AggregateErrors remain uncertain.
const beforeWrite = new Set([
  'ECONNREFUSED',
  'ENOTFOUND',
  'EAI_AGAIN',
  'ENETUNREACH',
  'EHOSTUNREACH',
  'UND_ERR_CONNECT_TIMEOUT',
  'DEPTH_ZERO_SELF_SIGNED_CERT',
  'SELF_SIGNED_CERT_IN_CHAIN',
  'UNABLE_TO_VERIFY_LEAF_SIGNATURE',
  'UNABLE_TO_GET_ISSUER_CERT_LOCALLY',
  'CERT_HAS_EXPIRED',
  'ERR_TLS_CERT_ALTNAME_INVALID',
  'ERR_TLS_HANDSHAKE_TIMEOUT',
  'ERR_SSL_WRONG_VERSION_NUMBER',
  'ERR_SSL_SSLV3_ALERT_HANDSHAKE_FAILURE',
]);

/** @param {unknown} error @param {number} [depth] @returns {boolean} */
export function requestWasNotSent(error, depth = 0) {
  if (!error || typeof error !== 'object' || depth > 8) return false;
  if (error instanceof AggregateError)
    return (
      error.errors.length > 0 &&
      error.errors.length <= 16 &&
      error.errors.every((value) => requestWasNotSent(value, depth + 1))
    );
  const value = /** @type {Error & {code?:string,cause?:unknown}} */ (error);
  if (beforeWrite.has(value.code ?? '')) return true;
  return requestWasNotSent(value.cause, depth + 1);
}
