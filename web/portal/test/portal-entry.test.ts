import { mount, flushPromises } from '@vue/test-utils';
import { afterEach, expect, it, vi } from 'vitest';
import { createMemoryHistory, createRouter } from 'vue-router';
import { bindApiRouter } from '../src/api';
import PortalEntry from '../src/views/PortalEntry.vue';

afterEach(() => vi.unstubAllGlobals());
it.each([401, 200, 503])('公共入口正确处理会话状态 %i', async (status) => {
  vi.stubGlobal(
    'fetch',
    vi
      .fn()
      .mockResolvedValue(
        new Response(
          JSON.stringify(
            status === 200
              ? { account: {} }
              : { code: 'common.unauthenticated', status },
          ),
          { status, headers: { 'Content-Type': 'application/json' } },
        ),
      ),
  );
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/', component: PortalEntry }],
  });
  await router.push('/');
  const navigate = vi.fn();
  bindApiRouter(router, navigate);
  const view = mount(PortalEntry, {
    global: { plugins: [router], stubs: { HomeView: true, LandingView: true } },
  });
  await flushPromises();
  expect(view.find('landing-view-stub').exists()).toBe(status === 401);
  expect(view.find('home-view-stub').exists()).toBe(status === 200);
  if (status === 503) expect(view.text()).toContain('重试');
  expect(navigate).not.toHaveBeenCalled();
  view.unmount();
});
