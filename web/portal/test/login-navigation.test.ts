import { afterEach, describe, expect, it, vi } from 'vitest';
import { createRouter, createMemoryHistory } from 'vue-router';
import { apiClient, bindApiRouter } from '../src/api';

afterEach(() => vi.unstubAllGlobals());
describe('portal document login navigation', () => {
  it('navigates the full document once for concurrent expired requests and preserves only a local return path', async () => {
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: '/account/security', component: { template: '<div />' } },
        { path: '/login', component: { template: '<div />' } },
      ],
    });
    await router.push('/account/security?tab=sessions');
    const navigate = vi.fn();
    bindApiRouter(router, navigate);
    vi.stubGlobal(
      'fetch',
      vi.fn(
        async () =>
          new Response(
            JSON.stringify({
              status: 401,
              title: 'Expired',
              code: 'common.unauthenticated',
            }),
            {
              status: 401,
              headers: { 'Content-Type': 'application/problem+json' },
            },
          ),
      ),
    );
    await Promise.allSettled([
      apiClient.get('/api/iam/v1/me'),
      apiClient.get('/api/iam/v1/me/sessions'),
    ]);
    expect(navigate).toHaveBeenCalledExactlyOnceWith(
      '/login?returnTo=%2Faccount%2Fsecurity%3Ftab%3Dsessions',
    );
    expect(router.currentRoute.value.path).toBe('/account/security');
  });
  it('keeps an anonymous 401 on the login document without reloading it', async () => {
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [{ path: '/login', component: { template: '<div />' } }],
    });
    await router.push('/login');
    const navigate = vi.fn();
    bindApiRouter(router, navigate);
    vi.stubGlobal(
      'fetch',
      vi.fn(
        async () =>
          new Response(
            JSON.stringify({
              status: 401,
              title: 'Expired',
              code: 'common.unauthenticated',
            }),
            {
              status: 401,
              headers: { 'Content-Type': 'application/problem+json' },
            },
          ),
      ),
    );
    await expect(apiClient.get('/api/iam/v1/me')).rejects.toThrow();
    expect(navigate).not.toHaveBeenCalled();
  });
});
