import { mount, flushPromises } from '@vue/test-utils';
import { createMemoryHistory, createRouter } from 'vue-router';
import { createAurigaI18n } from '@auriga/shared';
import { it, expect, vi } from 'vitest';
import LoginView from './LoginView.vue';
const api = vi.hoisted(() => ({ get: vi.fn(), listLoginProviders: vi.fn() }));
vi.mock('../api', () => ({
  apiClient: { get: api.get },
  iamApi: { listLoginProviders: api.listLoginProviders },
}));
it('未登录的登录首页显示公开版本与下载入口', async () => {
  api.listLoginProviders.mockResolvedValue({ items: [] });
  api.get.mockResolvedValue({
    version: '0.1.0',
    sequence: 4,
    sizeBytes: 1048576,
    publishedAt: '2026-11-12T00:00:00Z',
    sha256: 'a'.repeat(64),
  });
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/login', component: LoginView }],
  });
  await router.push('/login');
  const view = mount(LoginView, {
    global: { plugins: [router, createAurigaI18n('zh-CN')] },
  });
  await flushPromises();
  expect(view.text()).toContain('0.1.0');
  expect(view.text()).toContain('下载 Windows 版');
  expect(view.get('a').attributes('href')).toBe(
    '/api/resource/v1/desktop/packages/4',
  );
  expect(api.get).toHaveBeenCalledWith('/api/resource/v1/desktop/latest', {
    credentials: 'omit',
  });
  view.unmount();
});
