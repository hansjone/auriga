import { mount, flushPromises } from '@vue/test-utils';
import { afterEach, expect, it, vi } from 'vitest';
import { createMemoryHistory, createRouter } from 'vue-router';
import { bindApiRouter } from '../src/api';
import { createAurigaI18n } from '@auriga/shared';
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
    global: {
      plugins: [router, createAurigaI18n('zh-CN')],
      stubs: { HomeView: true, LandingView: true },
    },
  });
  await flushPromises();
  expect(view.find('landing-view-stub').exists()).toBe(status === 401);
  expect(view.find('home-view-stub').exists()).toBe(status === 200);
  if (status === 503) expect(view.text()).toContain('重试');
  expect(navigate).not.toHaveBeenCalled();
  view.unmount();
});

it('入口加载和故障状态使用英文翻译', async () => {
  let finish!: (value: Response) => void;
  vi.stubGlobal(
    'fetch',
    vi.fn(
      () =>
        new Promise<Response>((resolve) => {
          finish = resolve;
        }),
    ),
  );
  const view = mount(PortalEntry, {
    global: {
      plugins: [createAurigaI18n('en')],
      stubs: { HomeView: true, LandingView: true },
    },
  });
  expect(view.text()).toContain('Opening Auriga DSH');
  finish(
    new Response(JSON.stringify({ status: 503, code: 'common.unavailable' }), {
      status: 503,
      headers: { 'Content-Type': 'application/json' },
    }),
  );
  await flushPromises();
  expect(view.text()).toContain('Unable to confirm your sign-in status.');
  expect(view.get('button').text()).toBe('Retry');
  expect(view.get('a').text()).toBe('Sign in');
  view.unmount();
});
