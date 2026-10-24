import {
  createApiClient,
  createIamApi,
  createOrgApi,
  readAccessMode,
  type ApiClientOptions,
} from '@auriga/shared';
import type { Router } from 'vue-router';

let loginRedirect: (() => void) | undefined;

export function bindApiRouter(router: Router) {
  loginRedirect = () => {
    if (router.currentRoute.value.path !== '/login') {
      void router.push({
        path: '/login',
        query: { returnTo: router.currentRoute.value.fullPath },
      });
    }
  };
}

const clientOptions: ApiClientOptions = {
  getAdminAccessMode: () => readAccessMode(),
  getLocale: () => document.documentElement.lang || 'zh-CN',
  onUnauthenticated: () => loginRedirect?.(),
};
export const apiClient = createApiClient(clientOptions);

// Resource detail editing needs the actual response ETag, not a guessed revision.
// A reader per call keeps concurrent responses isolated; writes use apiClient.
export async function readResource<T>(path: string) {
  let etag: string | null = null;
  const reader = createApiClient({
    ...clientOptions,
    onUnauthenticated: () => {
      apiClient.resetSession();
      loginRedirect?.();
    },
    fetch: async (input, init) => {
      const response = await globalThis.fetch(input, init);
      etag = response.headers.get('ETag');
      return response;
    },
  });
  const data = await reader.get<T>(path, { cache: 'no-store' });
  return { data, etag };
}

export const iamApi = createIamApi(apiClient);
export const orgApi = createOrgApi(apiClient);
