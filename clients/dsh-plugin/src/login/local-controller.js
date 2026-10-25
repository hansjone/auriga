import { LoginFailure } from './errors.js';

export const LOCAL_LOGIN_BASE = '/auriga/local-login';
/** Browser transport for the existing local host contract, never the platform.
 * @param {{fetch?:typeof fetch,onStatus?:(state:{remoteRevocation?:string})=>void}} [options] */
export function createLocalLoginController(options = {}) {
  const transport = options.fetch ?? fetch;
  let csrf = '';
  /** @param {string} path @param {object | undefined} body @param {AbortSignal} signal */
  async function call(path, body, signal) {
    const controller = new AbortController();
    const abort = () => controller.abort();
    signal.addEventListener('abort', abort, { once: true });
    if (signal.aborted) abort();
    const timer = setTimeout(abort, 15000);
    try {
      const response = await transport(LOCAL_LOGIN_BASE + '/' + path, {
        method: body ? 'POST' : 'GET',
        credentials: 'same-origin',
        cache: 'no-store',
        redirect: 'error',
        signal: controller.signal,
        headers: body
          ? { 'content-type': 'application/json', 'X-Auriga-CSRF': csrf }
          : {},
        body: body ? JSON.stringify(body) : undefined,
      });
      if (response.status === 404 && path === 'status') {
        const bootstrap = await transport('/auriga/bootstrap', {
          credentials: 'same-origin',
          cache: 'no-store',
          redirect: 'error',
          signal: controller.signal,
        });
        if (bootstrap.ok && (await bootstrap.json()).status === 'unconfigured')
          throw new LoginFailure('unconfigured');
      }
      let data;
      try {
        data = await response.json();
      } catch {
        throw new LoginFailure('network');
      }
      if (!response.ok) {
        if (
          ['iam.login_tx_expired', 'common.unauthenticated'].includes(
            data?.code,
          ) ||
          response.status === 401
        )
          throw new LoginFailure('expired');
        if (response.status === 501) throw new LoginFailure('unavailable');
        throw new LoginFailure(response.status >= 500 ? 'network' : 'failed');
      }
      if (!data || typeof data !== 'object' || Array.isArray(data))
        throw new LoginFailure('failed');
      return data;
    } catch (error) {
      if (error instanceof LoginFailure || signal.aborted) throw error;
      throw new LoginFailure('network');
    } finally {
      clearTimeout(timer);
      signal.removeEventListener('abort', abort);
    }
  }
  /** @param {AbortSignal} signal */
  async function status(signal) {
    const state = await call('status', undefined, signal);
    if (
      state.available !== true ||
      typeof state.loggedIn !== 'boolean' ||
      typeof state.connected !== 'boolean'
    )
      throw new LoginFailure('failed');
    csrf = typeof state.csrf === 'string' ? state.csrf : '';
    options.onStatus?.(state);
    return state;
  }
  /** @param {any} state */
  function ready(state) {
    if (state.loggedIn !== true || state.connected !== true)
      throw new LoginFailure('network');
    return /** @type {const} */ ({ type: 'complete', redirectTo: '/' });
  }
  /** An anonymous browser alone says nothing about the device backing.
   * @param {any} state */
  function signedOut(state) {
    if (
      state.loggedIn !== false ||
      state.connected !== false ||
      state.resumeAvailable !== false ||
      !['pending', 'complete'].includes(state.remoteRevocation)
    )
      throw new LoginFailure('failed');
    return { loggedIn: false, remoteRevocation: state.remoteRevocation };
  }
  /** @param {any} result @param {AbortSignal} signal @returns {Promise<import('../generated/steps.js').ClientStep>} */
  async function step(result, signal) {
    if (
      !result.step ||
      typeof result.step !== 'object' ||
      typeof result.step.type !== 'string'
    )
      throw new LoginFailure('failed');
    return result.step.type === 'complete'
      ? ready(await status(signal))
      : result.step;
  }
  /** @param {AbortSignal} signal */
  async function cancel(signal) {
    // Never acquire an authenticated session's proof for an anonymous cancel.
    const state = await status(signal);
    if (state.loggedIn) throw new LoginFailure('expired');
    if (!csrf) throw new LoginFailure('expired');
    const result = await call('logout', {}, signal);
    if (result.loggedIn !== false) throw new LoginFailure('failed');
    csrf = '';
  }
  return {
    status,
    cancel,
    /** @param {{signal:AbortSignal}} context */
    async start({ signal }) {
      const state = await status(signal);
      if (state.loggedIn) return ready(state);
      if (!csrf) throw new LoginFailure('expired');
      return step(
        await call(
          'login',
          { action: state.resumeAvailable ? 'resume' : 'begin' },
          signal,
        ),
        signal,
      );
    },
    /** @param {{signal:AbortSignal}} context */
    async restart({ signal }) {
      const state = await status(signal);
      if (state.loggedIn) return ready(state);
      await cancel(signal);
      const fresh = await status(signal);
      if (fresh.loggedIn) return ready(fresh);
      if (!csrf) throw new LoginFailure('expired');
      return step(await call('login', { action: 'begin' }, signal), signal);
    },
    /** @param {import('../generated/steps.js').StepInput} input @param {{signal:AbortSignal}} context */
    async submit(input, { signal }) {
      if (input.type !== 'form') throw new LoginFailure('unavailable');
      if (!csrf) throw new LoginFailure('expired');
      return step(
        await call('login', { action: 'advance', input }, signal),
        signal,
      );
    },
    /** Called only after the user explicitly confirms global device sign-out.
     * @param {AbortSignal} signal */
    async logout(signal) {
      let state = await status(signal);
      if (!state.loggedIn) {
        if (state.resumeAvailable === false) return signedOut(state);
        if (state.resumeAvailable !== true || !csrf)
          throw new LoginFailure('expired');
        // Confirmation authorizes this recovery only. The host must verify the
        // saved device and establish an owned browser session before logout.
        const resumed = await call('login', { action: 'resume' }, signal);
        if (resumed.step?.type !== 'complete')
          throw new LoginFailure('expired');
        state = await status(signal);
        ready(state);
      }
      if (!csrf) throw new LoginFailure('expired');
      const result = await call('logout', {}, signal);
      if (
        result.loggedIn !== false ||
        !['pending', 'complete'].includes(result.remoteRevocation)
      )
        throw new LoginFailure('failed');
      csrf = '';
      // Verify both browser access and recoverable backing were actually lost;
      // an empty remote revocation queue is not evidence of device logout.
      return signedOut(await status(signal));
    },
  };
}
