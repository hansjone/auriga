import { registerAurigaAdapter } from './index.js';

/** Native composition only. This import resolves the installed PUBLIC root;
 * never an environment-selected host path or a DSH internal implementation.
 * Generation ports must be supplied by native trusted composition. Browser
 * settings, model-directory ids and model-call inputs cannot supply them.
 * @param {{broker:any, origin:string, generation?: import('./types.js').GenerationPorts}} options */
export function createModelLifecycle({ broker, origin, generation }) {
  // Without both exact capacity and a trusted tokenizer, retain discovery but
  // supply no generation limits. buildRequest then refuses before token exchange.
  const countInputTokens = generation?.countInputTokens;
  const modelLimits = Object.fromEntries(
    Object.entries(
      JSON.parse(JSON.stringify(generation?.modelLimits ?? {})),
    ).filter(
      ([, limits]) =>
        typeof countInputTokens === 'function' &&
        typeof limits?.contextWindow === 'number' &&
        typeof limits?.maxOutputTokens === 'number' &&
        Number.isSafeInteger(limits?.contextWindow) &&
        Number.isSafeInteger(limits?.maxOutputTokens) &&
        limits.maxOutputTokens > 0 &&
        limits.contextWindow > limits.maxOutputTokens,
    ),
  );
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
        modelLimits,
        countInputTokens,
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
      const availability = registration?.adapter.availability;
      return {
        registered: !!registration,
        directoryAvailable: availability?.directoryAvailable === true,
        available: availability?.available === true,
        authorized: availability?.authorized === true,
        state: availability?.available ? 'model_ready' : diagnostic,
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
