import { PlatformError } from './platform.js';

export const NATIVE_LOGIN_CHANNEL = '/auriga-native';
export const NATIVE_LOGIN_METHODS = new Set([
  'aurigaAuth/status',
  'aurigaAuth/login',
  'aurigaAuth/logout',
  'aurigaAuth/emergency',
]);

const record = (/** @type {any} */ value) =>
  value && typeof value === 'object' && !Array.isArray(value);

/** Same nativeConnect/password/TOTP input contract as the browser route.
 * @param {any} input */
export function validLoginInput(input) {
  if (!record(input) || !['begin', 'advance', 'resume'].includes(input.action))
    return false;
  const keys = Object.keys(input).sort().join(',');
  if (input.action !== 'advance') return keys === 'action';
  const form = input.input;
  return (
    keys === 'action,input' &&
    record(form) &&
    Object.keys(form).sort().join(',') === 'formId,type,values' &&
    form.type === 'form' &&
    typeof form.formId === 'string' &&
    form.formId.length > 0 &&
    form.formId.length <= 128 &&
    record(form.values) &&
    Object.values(form.values).every((value) =>
      ['string', 'boolean'].includes(typeof value),
    )
  );
}

/** Register through the public DSH carrier: it authenticates the live Peer and
 * checks envelope.method against the exact URL endpoint before this handler.
 * @param {any} ctx @param {()=>any} getRuntime */
export function installNativeLoginRPC(ctx, getRuntime) {
  if (typeof ctx.connection.register !== 'function') return;
  // Pass the plugin's declared webServer/connection owner explicitly. In rc.3
  // the convenience rpc getter retains the service scope, which cannot read
  // the plugin owner's webServer inside the deferred registration effect.
  ctx.connection.register(
    ctx,
    NATIVE_LOGIN_CHANNEL,
    async (
      /** @type {string} */ method,
      /** @type {any} */ payload,
      /** @type {AbortSignal} */ signal,
      /** @type {any} */ peer,
    ) => {
      const fail = (/** @type {string} */ code) => ({
        ok: false,
        error: { code, message: 'Local platform login request failed' },
      });
      if (peer?.principal?.kind !== 'owner') return fail('common.forbidden');
      if (!NATIVE_LOGIN_METHODS.has(method)) return fail('common.not_found');
      const args = payload?.args;
      if (
        !record(payload) ||
        Object.keys(payload).join(',') !== 'args' ||
        !record(args) ||
        JSON.stringify(args).length > 16384 ||
        (method === 'aurigaAuth/login'
          ? Object.keys(args).join(',') !== 'input' ||
            !validLoginInput(args.input)
          : Object.keys(args).length !== 0)
      )
        return fail('common.validation_failed');
      const runtime = getRuntime();
      if (!runtime) return fail('common.unavailable');
      const request = runtime.nativeRequest(peer);
      const cancel = () => {
        void runtime.cancelAttempt(request).catch(() => {});
      };
      signal.addEventListener('abort', cancel, { once: true });
      try {
        signal.throwIfAborted();
        let value;
        if (method === 'aurigaAuth/status')
          value = runtime.prepare(request).body;
        else if (method === 'aurigaAuth/login')
          value = { step: (await runtime.login(request, args.input)).step };
        else if (method === 'aurigaAuth/emergency')
          value = await runtime.resetEmergency(request);
        else value = (await runtime.logout(request)).body;
        const safe = { ...value };
        delete safe.csrf;
        return { ok: true, value: safe };
      } catch (error) {
        if (
          error instanceof PlatformError &&
          [
            'iam.invalid_credentials',
            'iam.login_tx_expired',
            'common.rate_limited',
            'common.unauthenticated',
          ].includes(error.code ?? '')
        )
          return fail(error.code ?? 'common.unauthenticated');
        return fail('common.unavailable');
      } finally {
        signal.removeEventListener('abort', cancel);
      }
    },
  );
}
