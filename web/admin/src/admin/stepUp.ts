import { isApiError, type ApiClient } from '@auriga/shared';
let sessionEpoch = 0;
const unavailable = new Error('续验入口不可用 / Verification unavailable');

// Only a server's explicit step-up denial can enter this continuation. The
// original serialized payload and preconditions survive the verification UI.
export function withStepUp(
  base: ApiClient,
  verify: () => Promise<void>,
): ApiClient {
  let flight: Promise<void> | undefined;
  async function request<T>(
    path: string,
    init: Parameters<ApiClient['request']>[1] = {},
  ): Promise<T> {
    const current = sessionEpoch;
    const saved = { ...init, headers: new Headers(init.headers) };
    try {
      return await base.request<T>(path, saved);
    } catch (error) {
      if (
        !isApiError(error) ||
        error.status !== 403 ||
        error.problem.code !== 'iam.step_up_required' ||
        !['POST', 'PUT', 'PATCH', 'DELETE'].includes(
          (init.method ?? 'GET').toUpperCase(),
        ) ||
        init.body instanceof FormData ||
        current !== sessionEpoch
      )
        throw error;
      init.signal?.throwIfAborted();
      if (!flight) {
        const pending = verify();
        flight = pending;
        void pending
          .finally(() => {
            if (flight === pending) flight = undefined;
          })
          .catch(() => {});
      }
      try {
        await flight;
      } catch (failure) {
        if (failure === unavailable) throw error;
        throw failure;
      }
      init.signal?.throwIfAborted();
      if (current !== sessionEpoch) throw error;
      return base.request<T>(path, saved);
    }
  }
  function body<T>(
    method: string,
    path: string,
    value?: unknown,
    init?: Parameters<ApiClient['request']>[1],
  ) {
    return request<T>(path, {
      ...init,
      method,
      body:
        value == null
          ? undefined
          : typeof value === 'string' || value instanceof FormData
            ? value
            : JSON.stringify(value),
    });
  }
  return {
    request,
    resetSession() {
      sessionEpoch++;
      flight = undefined;
      base.resetSession();
    },
    invalidatePendingRequests() {
      sessionEpoch++;
      flight = undefined;
      base.invalidatePendingRequests();
    },
    get: (path, init) => request(path, { ...init, method: 'GET' }),
    delete: (path, init) => request(path, { ...init, method: 'DELETE' }),
    post: (path, value, init) => body('POST', path, value, init),
    put: (path, value, init) => body('PUT', path, value, init),
    patch: (path, value, init) => body('PATCH', path, value, init),
  };
}

let verifier: (() => Promise<void>) | undefined;
export function bindStepUp(handler: () => Promise<void>) {
  verifier = handler;
  return () => {
    if (verifier === handler) verifier = undefined;
  };
}
export function requestStepUp() {
  return verifier ? verifier() : Promise.reject(unavailable);
}
