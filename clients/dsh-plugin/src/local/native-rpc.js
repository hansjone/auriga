export const NATIVE_LOGIN_CHANNEL = '/auriga-native';
export const NATIVE_LOGIN_METHODS = new Set([
  'aurigaAuth/status',
  'aurigaAuth/login',
  'aurigaAuth/cancel',
  'aurigaAuth/logout',
]);
/** Native Peer authentication is supplied by DSH's public carrier.
 * Login is a shell action only; no password/TOTP/transaction input exists.
 * @param {any} ctx @param {()=>any} getRuntime */
export function installNativeLoginRPC(ctx, getRuntime) {
  if (typeof ctx.connection.register !== 'function') return;
  ctx.connection.register(
    ctx,
    NATIVE_LOGIN_CHANNEL,
    async (
      /** @type {string} */ method,
      /** @type {any} */ payload,
      /** @type {AbortSignal} */ signal,
      /** @type {any} */ peer,
    ) => {
      const fail = (code = 'common.unavailable') => ({
        ok: false,
        error: { code, message: 'Shell unavailable' },
      });
      if (peer?.principal?.kind !== 'owner') return fail('common.forbidden');
      if (!NATIVE_LOGIN_METHODS.has(method)) return fail('common.not_found');
      if (
        !payload ||
        Array.isArray(payload) ||
        Object.keys(payload).join(',') !== 'args' ||
        !payload.args ||
        Array.isArray(payload.args) ||
        typeof payload.args !== 'object' ||
        Object.keys(payload.args).length
      )
        return fail('common.validation_failed');
      const runtime = getRuntime();
      if (!runtime) return fail();
      const request = runtime.nativeRequest(peer);
      try {
        signal.throwIfAborted();
        const value =
          method === 'aurigaAuth/status'
            ? runtime.status(request)
            : await runtime.command(
                request,
                method === 'aurigaAuth/login'
                  ? 'open_login'
                  : method === 'aurigaAuth/logout'
                    ? 'logout'
                    : 'cancel_login',
              );
        signal.throwIfAborted();
        const safe = { ...value };
        delete safe.csrf;
        return { ok: true, value: safe };
      } catch {
        return fail();
      }
    },
    { methods: NATIVE_LOGIN_METHODS },
  );
}
