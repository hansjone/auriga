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
import { bindApiRouter, iamApi } from '../src/api';

beforeEach(async () => {
  resetMockState();
  await enableAurigaMock();
  setMockSession('portal');
});

afterEach(() => {
  vi.restoreAllMocks();
  vi.useRealTimers();
  disableAurigaMock();
  vi.unstubAllGlobals();
});

describe('HomeView', () => {
  it('displays department information and updates only the current session projection', async () => {
    const initial = await iamApi.getMe();
    vi.useFakeTimers();
    const read = vi
      .spyOn(iamApi, 'getMe')
      .mockResolvedValueOnce({
        ...initial,
        uacLoginInfo: { purpose: 'login_info', status: 'pending' },
      })
      .mockResolvedValue({
        ...initial,
        uacLoginInfo: {
          purpose: 'login_info',
          status: 'available',
          department: 'Synthetic department',
          updatedAt: '2026-11-14T01:00:00Z',
        },
      });
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [{ path: '/', component: HomeView }],
    });
    await router.push('/');
    const wrapper = mount(HomeView, {
      global: {
        plugins: [router, createAurigaI18n('zh-CN')],
        stubs: { PlacementEntry: true, DesktopDownload: true },
      },
    });
    await flushPromises();
    expect(wrapper.get('[data-testid=uac-department]').text()).toBe(
      '部门信息暂不可用',
    );
    await vi.advanceTimersByTimeAsync(2000);
    await flushPromises();
    expect(wrapper.get('[data-testid=uac-department]').text()).toContain(
      '所属部门：Synthetic department',
    );
    expect(read).toHaveBeenCalledTimes(2);
    wrapper.unmount();
    await vi.advanceTimersByTimeAsync(10000);
    expect(read).toHaveBeenCalledTimes(2);
  });
  it('keeps a signed-in workspace visible when the department projection is unavailable', async () => {
    const initial = await iamApi.getMe();
    vi.spyOn(iamApi, 'getMe').mockResolvedValue({
      ...initial,
      uacLoginInfo: { purpose: 'login_info', status: 'unavailable' },
    });
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [{ path: '/', component: HomeView }],
    });
    await router.push('/');
    const wrapper = mount(HomeView, {
      global: {
        plugins: [router, createAurigaI18n('zh-CN')],
        stubs: { PlacementEntry: true, DesktopDownload: true },
      },
    });
    await flushPromises();
    expect(wrapper.get('[data-testid=uac-department]').text()).toBe(
      '部门信息暂不可用',
    );
    expect(wrapper.find('placement-entry-stub').exists()).toBe(true);
    wrapper.unmount();
  });
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
      const navigate = vi.fn();
      bindApiRouter(router, navigate);
      await router.push('/');
      const wrapper = mount(HomeView, {
        global: { plugins: [router, createAurigaI18n('zh-CN')] },
      });
      await flushPromises();
      expect(router.currentRoute.value.path).toBe('/');
      if (status === 401)
        expect(navigate).toHaveBeenCalledExactlyOnceWith('/login?returnTo=%2F');
      else expect(navigate).not.toHaveBeenCalled();
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
