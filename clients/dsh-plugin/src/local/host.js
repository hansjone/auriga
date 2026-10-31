/** DSH owns local authentication. Platform sessions control only Auriga.
 * @param {any} connection @param {any} runtime @param {{protocol:string}} options */
export function installLocalAdmission(connection, runtime, { protocol }) {
  if (!connection.admit || !connection.authorizeIndex || !connection.isTrusted)
    throw new Error(
      'auriga-dsh: 需要 DSH 0.2.1-rc.3 或更高版本的原生连接准入 API，请升级 DSH。 Native connection admission API required; upgrade DSH to 0.2.1-rc.3 or later.',
    );
  const target = connection[Symbol.for('cordis.original')] ?? connection;
  const nativeAdmit = target.admit;
  const admit = (/** @type {any} */ request) => {
    const result = nativeAdmit.call(connection, request);
    if ('rejection' in result) return result;
    if (result.peer?.principal?.kind !== 'owner') return { rejection: 403 };
    runtime.bindNativeRequest?.(result.peer, request);
    return result;
  };
  const nativeAuth = {
    requestRejection(/** @type {any} */ request) {
      const result = admit(request);
      return 'rejection' in result ? result.rejection : undefined;
    },
  };
  // Browser writes still require an exact origin and CSRF at their own route.
  const sameOrigin = (/** @type {any} */ request, required = false) => {
    if (!connection.isTrusted(request)) return false;
    if (
      request.headers['sec-fetch-site'] &&
      !['same-origin', 'none'].includes(request.headers['sec-fetch-site'])
    )
      return false;
    const origin = request.headers.origin;
    if (origin === undefined) return !required;
    const count = (/** @type {string} */ key) =>
      (request.rawHeaders ?? []).filter(
        (/** @type {string} */ x, /** @type {number} */ i) =>
          i % 2 === 0 && x.toLowerCase() === key,
      ).length;
    try {
      return (
        count('host') === 1 &&
        count('origin') === 1 &&
        origin === new URL(protocol + '//' + request.headers.host).origin
      );
    } catch {
      return false;
    }
  };
  connection.admit = admit;
  return {
    nativeAuth,
    sameOrigin,
    dispose() {
      if (target.admit === admit) target.admit = nativeAdmit;
    },
  };
}
