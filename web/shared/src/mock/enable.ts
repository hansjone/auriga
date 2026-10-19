import { handleMockRequest, resetMockState } from './router';

let originalFetch: typeof fetch | undefined;
let enabled = false;

/**
 * Install a fetch interceptor that answers /api/* from the in-memory mock.
 * Activate when `VITE_AURIGA_MOCK=1` (or call directly in tests).
 */
export async function enableAurigaMock(): Promise<void> {
  if (enabled) return;
  originalFetch = globalThis.fetch.bind(globalThis);
  globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
    const mocked = await handleMockRequest(input, init);
    if (mocked) return mocked;
    return originalFetch!(input, init);
  }) as typeof fetch;
  enabled = true;
}

export function disableAurigaMock(): void {
  if (!enabled || !originalFetch) return;
  globalThis.fetch = originalFetch;
  originalFetch = undefined;
  enabled = false;
  resetMockState();
}

export function isAurigaMockEnabled(): boolean {
  return enabled;
}

export { resetMockState, setMockSession } from './router';
