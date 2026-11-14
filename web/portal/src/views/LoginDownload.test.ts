import { mount, flushPromises } from '@vue/test-utils';
import { createMemoryHistory, createRouter } from 'vue-router';
import { createAurigaI18n } from '@auriga/shared';
import { it, expect, vi } from 'vitest';
import LoginView from './LoginView.vue';
import LandingView from './LandingView.vue';
const api = vi.hoisted(() => ({ get: vi.fn(), listLoginProviders: vi.fn() }));
vi.mock('../api', () => ({
  apiClient: { get: api.get },
  iamApi: { listLoginProviders: api.listLoginProviders },
}));
it('公共产品首页显示公开版本与下载入口', async () => {
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
  const view = mount(LandingView, {
    global: { plugins: [router, createAurigaI18n('zh-CN')] },
  });
  await flushPromises();
  expect(view.text()).toContain('0.1.0');
  expect(view.text()).toContain('下载 Windows 版');
  expect(view.get('.download-button').attributes('href')).toBe(
    '/api/resource/v1/desktop/packages/4',
  );
  expect(api.get).toHaveBeenCalledWith('/api/resource/v1/desktop/latest', {
    credentials: 'omit',
  });
  view.unmount();
});

it('登录页仅提供登录与返回首页，不请求下载信息', async () => {
  api.get.mockClear();
  api.listLoginProviders.mockResolvedValue({ items: [] });
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/login', component: LoginView }],
  });
  await router.push('/login');
  const view = mount(LoginView, {
    global: { plugins: [router, createAurigaI18n('zh-CN')] },
  });
  await flushPromises();
  expect(view.text()).toContain('登录团队工作台');
  expect(view.text()).not.toContain('下载 Windows');
  expect(api.get).not.toHaveBeenCalled();
  expect(view.get('a').attributes('href')).toBe('/');
  view.unmount();
});
