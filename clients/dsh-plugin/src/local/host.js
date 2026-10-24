/** Responses belong to a local browser session, independently of the shared
 * native Peer and keep-alive socket. Closing one must not cancel another.
 * @param {(request:import('node:http').IncomingMessage)=>{key:string}|null} resolve */
export function createHttpResponseTracker(resolve) {
  /** @type {Map<string,Set<import('node:http').ServerResponse>>} */
  const responses = new Map();
  return {
    /** @param {import('node:http').IncomingMessage} request @param {import('node:http').ServerResponse} response */
    bind(request, response) {
      const session = resolve(request);
      if (!session || response.destroyed || response.writableEnded)
        return false;
      const owned = responses.get(session.key) ?? new Set();
      responses.set(session.key, owned);
      if (owned.has(response)) return true;
      owned.add(response);
      const release = () => {
        response.off('close', release);
        response.off('finish', release);
        owned.delete(response);
        if (owned.size === 0 && responses.get(session.key) === owned)
          responses.delete(session.key);
      };
      response.once('close', release);
      response.once('finish', release);
      return true;
    },
    /** @param {string} key */
    close(key) {
      const owned = responses.get(key);
      if (!owned) return;
      responses.delete(key);
      for (const response of owned) response.destroy();
    },
  };
}

/** Register the real rc.3 shared-API waterfall in its owning plugin context.
 * @param {any} ctx
 * @param {(req:import('node:http').IncomingMessage,res:import('node:http').ServerResponse)=>boolean} bind */
export function installHttpResponseTracking(ctx, bind) {
  if (typeof ctx.on !== 'function')
    throw new Error('native connection/request hook required');
  const remove = ctx.on(
    'connection/request',
    (
      /** @type {import('node:http').IncomingMessage} */ request,
      /** @type {import('node:http').ServerResponse} */ response,
      /** @type {()=>Promise<unknown>} */ next,
    ) => {
      if (!bind(request, response)) {
        if (!response.destroyed && !response.writableEnded) {
          response.writeHead(401);
          response.end();
        }
        return;
      }
      return next();
    },
  );
  if (typeof remove !== 'function')
    throw new Error('native HTTP tracking disposer required');
  let removed = false;
  return () => {
    if (removed) return;
    removed = true;
    remove();
  };
}

/** Preserve rc.3 native Peer/launch-token admission; add the local login fence.
 * @param {any} connection @param {any} runtime @param {{protocol:string}} options */
export function installLocalAdmission(connection, runtime, { protocol }) {
  if (!connection.admit || !connection.authorizeIndex || !connection.isTrusted)
    throw new Error('rc.3 native connection admission required');
  if (typeof runtime.installHttpTracking !== 'function')
    throw new Error('native HTTP response tracking required');
  const original = Symbol.for('cordis.original');
  // Cordis shadow method proxies do not carry `original`; inspect the service
  // target only for exact method ownership/restoration, never for tool ownership.
  const target = connection[original] ?? connection;
  const nativeAdmit = target.admit,
    nativeIndex = target.authorizeIndex;
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
  const stopHttpTracking = runtime.installHttpTracking();
  connection.admit = admit;
  connection.authorizeIndex = authorizeIndex;
  return {
    nativeAuth,
    sameOrigin,
    dispose() {
      stopHttpTracking();
      if (target.admit === admit) target.admit = nativeAdmit;
      if (target.authorizeIndex === authorizeIndex)
        target.authorizeIndex = nativeIndex;
    },
  };
}
