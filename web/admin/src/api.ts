import {
  createApiClient,
  createIamApi,
  createOrgApi,
  readAccessMode,
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

export const apiClient = createApiClient({
  getAdminAccessMode: () => readAccessMode(),
  getLocale: () => document.documentElement.lang || 'zh-CN',
  onUnauthenticated: () => loginRedirect?.(),
});

export const iamApi = createIamApi(apiClient);
export const orgApi = createOrgApi(apiClient);
