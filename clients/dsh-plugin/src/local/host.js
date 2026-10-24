/** Preserve rc.3 native Peer/launch-token admission; add the local login fence.
 * @param {any} connection @param {any} runtime @param {{protocol:string}} options */
export function installLocalAdmission(connection, runtime, { protocol }) {
  if (!connection.admit || !connection.authorizeIndex || !connection.isTrusted)
    throw new Error('rc.3 native connection admission required');
  const nativeAdmit = connection.admit,
    nativeIndex = connection.authorizeIndex;
  const nativeAuth = {
    /** @param {any} request */
    requestRejection: (request) => {
      const result = nativeAdmit.call(connection, request);
      return 'rejection' in result ? result.rejection : undefined;
    },
  };
  /** @param {import('node:http').IncomingMessage} request @param {boolean} [required] */
  function sameOrigin(request, required = false) {
    if (!connection.isTrusted(request)) return false;
    if (
      request.headers['sec-fetch-site'] &&
      !['same-origin', 'none'].includes(request.headers['sec-fetch-site'])
    )
      return false;
    const origin = request.headers.origin,
      host = request.headers.host;
    if (origin === undefined) return !required;
    if (
      typeof origin !== 'string' ||
      typeof host !== 'string' ||
      !request.rawHeaders
    )
      return false;
    const count = (/** @type {string} */ name) =>
      request.rawHeaders.filter(
        (v, i) => i % 2 === 0 && v.toLowerCase() === name,
      ).length;
    if (count('origin') !== 1 || count('host') !== 1) return false;
    try {
      return (
        new URL(origin).origin === origin &&
        origin === new URL(protocol + '//' + host).origin
      );
    } catch {
      return false;
    }
  }
  const admit = function (
    /** @type {import('node:http').IncomingMessage} */ request,
  ) {
    const original = nativeAdmit.call(connection, request);
    if ('rejection' in original) return original;
    const upgrade = request.headers.upgrade?.toLowerCase() === 'websocket';
    if (
      !sameOrigin(
        request,
        upgrade ||
          !['GET', 'HEAD', 'OPTIONS'].includes(request.method ?? 'GET'),
      )
    )
      return { rejection: 403 };
    if (!runtime.resolve(request)) return { rejection: 401 };
    runtime.bindSocket(request);
    return original;
  };
  const authorizeIndex = function (
    /** @type {import('node:http').IncomingMessage} */ request,
    /** @type {import('node:http').ServerResponse} */ response,
  ) {
    if (!nativeIndex.call(connection, request, response)) return false;
    if (!sameOrigin(request)) {
      response.writeHead(403);
      response.end();
      return false;
    }
    if (!runtime.resolve(request)) {
      response.writeHead(303, {
        location: '/auriga/local-login',
        'cache-control': 'no-store',
      });
      response.end();
      return false;
    }
    return true;
  };
  connection.admit = admit;
  connection.authorizeIndex = authorizeIndex;
  return {
    nativeAuth,
    sameOrigin,
    dispose() {
      if (connection.admit === admit) connection.admit = nativeAdmit;
      if (connection.authorizeIndex === authorizeIndex)
        connection.authorizeIndex = nativeIndex;
    },
  };
}
