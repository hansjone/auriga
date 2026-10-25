import { registerAurigaAdapter } from './index.js';

/** Native composition only. This import resolves the installed PUBLIC root;
 * never an environment-selected host path or a DSH internal implementation.
 * The first release has no trusted model capabilities/tokenizer port. It can
 * register/discover but MUST reject generation before exchanging or posting.
 * @param {{broker:any, origin:string}} options */
export function createModelLifecycle({ broker, origin }) {
  /** @type {any} */
  let llm;
  /** @type {import('./types.js').HostApi | undefined} */
  let hostApi;
  /** @type {ReturnType<typeof registerAurigaAdapter> | undefined} */
  let registration;
  let signedIn = false,
    closed = false,
    binding = 0;
  let diagnostic = 'llm_unavailable';
  const stop = () => {
    registration?.dispose();
    registration = undefined;
  };
  const start = () => {
    if (closed || !signedIn || registration || !hostApi || !llm) return;
    try {
      registration = registerAurigaAdapter(llm, {
        hostApi,
        apiBase: origin,
        fetch,
        getAccessToken: async (signal) => {
          if (signal.aborted || closed || !signedIn) return null;
          return broker.getAiAccess();
        },
      });
      diagnostic = 'model_metadata_unavailable';
    } catch {
      diagnostic = 'public_api_unavailable';
    }
  };
  return {
    /** Bound by a separate optional ctx.inject(['llm']) child: losing LLM must
     * not tear down local authentication or Team tools.
     * @param {any} service */
    async attach(service) {
      const current = ++binding;
      stop();
      llm = undefined;
      if (typeof service?.registerAdapter !== 'function') {
        diagnostic = 'llm_unavailable';
        return () => {};
      }
      try {
        const api = await import('@deepseek-ai/dsh-llm');
        if (closed || binding !== current) return () => {};
        hostApi = api;
        llm = service;
        diagnostic = 'sign_in_required';
        start();
      } catch {
        diagnostic = 'public_api_unavailable';
      }
      return () => {
        if (binding !== current) return;
        binding++;
        stop();
        llm = undefined;
        diagnostic = 'llm_unavailable';
      };
    },
    login() {
      signedIn = true;
      start();
    },
    logout() {
      signedIn = false;
      stop();
      if (llm) diagnostic = 'sign_in_required';
    },
    close() {
      closed = true;
      binding++;
      signedIn = false;
      stop();
      llm = undefined;
    },
    get status() {
      return {
        registered: !!registration,
        available: false,
        authorized: false,
        state: diagnostic,
        credentialState: broker.status.state,
        reauthenticationRequired:
          broker.status.reauthenticationRequired === true,
      };
    },
  };
}

// No managed adapter until a secure per-instance socket is available. A public
// device token or ait_ credential is never an alternative transport.
export const MANAGED_MODEL_STATUS = Object.freeze({
  registered: false,
  available: false,
  authorized: false,
  state: 'managed_socket_unavailable',
});
