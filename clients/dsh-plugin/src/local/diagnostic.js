import { PlatformError } from './platform.js';

const tlsCodes = new Set([
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
const networkCodes = new Set([
  'ECONNREFUSED',
  'ECONNRESET',
  'ENOTFOUND',
  'EAI_AGAIN',
  'ETIMEDOUT',
  'UND_ERR_CONNECT_TIMEOUT',
  'UND_ERR_SOCKET',
  'UND_ERR_HEADERS_TIMEOUT',
]);
const platformCodes = new Set([
  'common.forbidden',
  'common.validation_failed',
  'common.internal',
  'common.unavailable',
  'invalid_client',
  'invalid_request',
  'unauthorized_client',
  'invalid_config',
  'invalid_provider',
  'invalid_scope',
  'not_found',
]);
const nativeMessages = new Map([
  ['native login response incomplete', 'native_response_incomplete'],
  ['native login not complete', 'native_not_complete'],
  ['login attempt superseded', 'attempt_superseded'],
  ['invalid login input', 'invalid_input'],
  ['invalid device token response', 'invalid_device_token_response'],
  ['native device grant expired', 'native_grant_expired'],
]);

// Whitelist classifications only. Error message/stack, URL, headers, request
// body and arbitrary upstream codes may contain credentials and never leave.
/** @param {unknown} error */
export function localFailureDiagnostic(error) {
  if (error instanceof PlatformError)
    return {
      category: 'platform_http',
      ...(Number.isInteger(error.status) &&
      error.status >= 400 &&
      error.status <= 599
        ? { status: error.status }
        : {}),
      ...(typeof error.code === 'string' && platformCodes.has(error.code)
        ? { code: error.code }
        : {}),
    };
  let current = error;
  for (let depth = 0; depth < 4 && current instanceof Error; depth++) {
    const value = /** @type {Error & {code?:string,cause?:unknown}} */ (
      current
    );
    if (typeof value.code === 'string' && tlsCodes.has(value.code))
      return { category: 'tls', code: value.code };
    if (typeof value.code === 'string' && networkCodes.has(value.code))
      return {
        category: ['ENOTFOUND', 'EAI_AGAIN'].includes(value.code)
          ? 'dns'
          : [
                'ETIMEDOUT',
                'UND_ERR_CONNECT_TIMEOUT',
                'UND_ERR_HEADERS_TIMEOUT',
              ].includes(value.code)
            ? 'timeout'
            : 'connection',
        code: value.code,
      };
    if (value.name === 'TimeoutError') return { category: 'timeout' };
    if (value.name === 'AbortError') return { category: 'cancelled' };
    current = value.cause;
  }
  if (error instanceof Error && nativeMessages.has(error.message))
    return { category: 'native_step', code: nativeMessages.get(error.message) };
  return { category: 'unexpected' };
}
