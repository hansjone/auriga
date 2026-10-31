import { readFileSync } from 'node:fs';

/** Use the same native boot table for Web and Electron's static carrier.
 * @param {any} ctx */
export function installLocalLoginBootstrap(ctx) {
  if (typeof ctx.webServer.collectIndexInjections !== 'function') return;
  const text = readFileSync(
    new URL('../login/startup.js', import.meta.url),
    'utf8',
  );
  ctx.on('webserver/index-inject', (/** @type {any[]} */ table) => {
    table.push({ kind: 'script', placement: 'head', text });
  });
}

// rc.3 Desktop reads only redacted configuration/credential metadata and its
// native account state before opening a window. These methods cannot mutate
// settings, return credential values, or operate an Auriga/DSH session.
const desktopStartupReads = new Set([
  '/api/settings/describe',
  '/api/llm/listConfigurableProviders',
  '/api/credentials/describe',
  '/api/account/getState',
]);

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
      // Tracking owns session-bound response lifetimes, not authorization.
      // Startup and login requests have no local session yet; their route still
      // owns native admission, origin and CSRF checks.
      bind(request, response);
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
    throw new Error(
      'auriga-dsh: 需要 DSH 0.2.1-rc.3 或更高版本的原生连接准入 API，请升级 DSH。 Native connection admission API required; upgrade DSH to 0.2.1-rc.3 or later.',
    );
  if (typeof runtime.installHttpTracking !== 'function')
    throw new Error('native HTTP response tracking required');
  const original = Symbol.for('cordis.original');
  // Cordis shadow method proxies do not carry `original`; inspect the service
  // target only for exact method ownership/restoration, never for tool ownership.
  const target = connection[original] ?? connection;
  const nativeAdmit = target.admit,
    nativeIndex = target.authorizeIndex;
  /** @type {Set<import('node:net').Socket>} */
  const standaloneSockets = new Set();
  function closeStandaloneSockets() {
    for (const socket of standaloneSockets) {
      standaloneSockets.delete(socket);
      socket.destroy();
    }
  }
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
    if (runtime.isConfigured?.() === false) {
      // Native multiplexed peers survive subsequent admission changes. Keep
      // their transport in the parent scope even without a platform runtime.
      const socket = request.socket;
      if (
        upgrade &&
        socket &&
        !socket.destroyed &&
        !standaloneSockets.has(socket)
      ) {
        standaloneSockets.add(socket);
        socket.once('close', () => standaloneSockets.delete(socket));
      }
      return original;
    }
    if (
      !upgrade &&
      request.method === 'POST' &&
      desktopStartupReads.has(request.url ?? '') &&
      request.headers['content-type']?.split(';')[0].trim().toLowerCase() ===
        'application/json'
    ) {
      // Native Electron fetch has no Origin for these read-only RPCs. A
      // supplied Origin must still be exact; browser cross-site requests fail.
      // The native RPC carrier also binds envelope.method to this exact path.
      return sameOrigin(request) ? original : { rejection: 403 };
    }
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
    if (runtime.isConfigured?.() === false) return true;
    if (!sameOrigin(request)) {
      response.writeHead(403);
      response.end();
      return false;
    }
    // The authenticated native shell must boot before platform login (and
    // before the connect runtime is ready). Serving its static index grants no
    // business access: admit() still requires the local session for API/WS.
    return true;
  };
  const stopHttpTracking = runtime.installHttpTracking();
  connection.admit = admit;
  connection.authorizeIndex = authorizeIndex;
  return {
    nativeAuth,
    sameOrigin,
    closeStandaloneSockets,
    dispose() {
      closeStandaloneSockets();
      stopHttpTracking();
      if (target.admit === admit) target.admit = nativeAdmit;
      if (target.authorizeIndex === authorizeIndex)
        target.authorizeIndex = nativeIndex;
    },
  };
}
