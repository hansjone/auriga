import { createApiClient, createIamApi, createOrgApi } from '@auriga/shared';
import type { Router } from 'vue-router';

let loginRedirect: (() => void) | undefined;

export function bindApiRouter(
  router: Router,
  navigate: (path: string) => void = (path) => globalThis.location.assign(path),
) {
  let navigating = false;
  loginRedirect = () => {
    if (navigating || router.currentRoute.value.path === '/login') return;
    navigating = true;
    navigate(
      `/login?returnTo=${encodeURIComponent(router.currentRoute.value.fullPath)}`,
    );
  };
}

/** Reload the document so edge can issue a fresh navigation transaction. */
export function redirectToLogin() {
  loginRedirect?.();
}

export const apiClient = createApiClient({
  getLocale: () => document.documentElement.lang || 'zh-CN',
  onUnauthenticated: () => loginRedirect?.(),
});

export const iamApi = createIamApi(apiClient);
export const orgApi = createOrgApi(apiClient);
