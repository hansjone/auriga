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

/** DSH owns local authentication. Platform sessions control only Auriga.
 * @param {any} connection @param {any} runtime @param {{protocol:string}} options */
export function installLocalAdmission(connection, runtime, { protocol }) {
  if (!connection.admit || !connection.authorizeIndex || !connection.isTrusted)
    throw new Error('auriga-dsh: native connection admission API required');
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
