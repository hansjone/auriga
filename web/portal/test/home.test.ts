import { mount, flushPromises } from '@vue/test-utils';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createPinia } from 'pinia';
import { createMemoryHistory, createRouter } from 'vue-router';
import ElementPlus from 'element-plus';
import {
  createAurigaI18n,
  disableAurigaMock,
  enableAurigaMock,
  resetMockState,
  setMockSession,
} from '@auriga/shared';
import HomeView from '../src/views/HomeView.vue';
import { bindApiRouter } from '../src/api';

beforeEach(async () => {
  resetMockState();
  await enableAurigaMock();
  setMockSession('portal');
});

afterEach(() => {
  disableAurigaMock();
  vi.unstubAllGlobals();
});

describe('HomeView', () => {
  it.each([401, 403, 503])(
    'only redirects identity HTTP %i when the session expired',
    async (status) => {
      vi.stubGlobal(
        'fetch',
        vi.fn(
          async () =>
            new Response(
              JSON.stringify({
                title: 'Failed',
                status,
                code:
                  status === 401
                    ? 'common.unauthenticated'
                    : 'common.unavailable',
              }),
              {
                status,
                headers: { 'Content-Type': 'application/problem+json' },
              },
            ),
        ),
      );
      const router = createRouter({
        history: createMemoryHistory(),
        routes: [
          { path: '/', component: HomeView },
          { path: '/login', component: { template: '<div />' } },
        ],
      });
      bindApiRouter(router);
      await router.push('/');
      const wrapper = mount(HomeView, {
        global: { plugins: [router, createAurigaI18n('zh-CN')] },
      });
      await flushPromises();
      expect(router.currentRoute.value.path).toBe(
        status === 401 ? '/login' : '/',
      );
      if (status !== 401) {
        expect(wrapper.get('[role=alert]').text()).toContain('出错了');
        expect(wrapper.text()).toContain('重试');
      }
      wrapper.unmount();
    },
  );
  it('shows shell and switches language', async () => {
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: '/', component: HomeView },
        { path: '/login', component: { template: '<div/>' } },
      ],
    });
    await router.push('/');
    const wrapper = mount(HomeView, {
      global: {
        plugins: [
          createPinia(),
          router,
          createAurigaI18n('zh-CN'),
          ElementPlus,
        ],
      },
    });
    await flushPromises();
    expect(wrapper.text()).toContain('首页');
    const buttons = wrapper.findAll('button');
    await buttons[0]!.trigger('click');
    expect(wrapper.text()).toContain('Home');
    wrapper.unmount();
  });
});
