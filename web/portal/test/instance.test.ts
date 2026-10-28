import { mount, flushPromises } from '@vue/test-utils';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createAurigaI18n } from '@auriga/shared';
import InstanceCard from '../src/components/InstanceCard.vue';
import { enterInstance, instanceEntry, type Instance } from '../src/workspace';

vi.mock('../src/workspace', async (original) => ({
  ...(await original<typeof import('../src/workspace')>()),
  enterInstance: vi.fn(),
}));
const stopped: Instance = {
  id: 'instance-1',
  tenantId: 'tenant-1',
  ownerId: 'owner-1',
  name: 'My DSH',
  runtime: 'docker',
  desired: 'stopped',
  phase: 'stopped',
  ready: false,
  generation: 1,
  observedGeneration: 1,
  pendingRestart: false,
  alwaysOn: false,
  dshVersion: '1',
  resources: {},
  node: null,
  error: null,
  entryUrl: 'https://untrusted.example.com/',
  createdAt: '',
  updatedAt: '',
};
const starting: Instance = {
  ...stopped,
  desired: 'running',
  phase: 'starting',
};
const running: Instance = { ...starting, phase: 'running', ready: true };
const json = (data: unknown, status = 200) =>
  new Response(JSON.stringify(data), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
const fetcher = vi.fn<typeof fetch>();
let wrapper: ReturnType<typeof mount> | undefined;
function render(locale: 'en' | 'zh-CN' = 'zh-CN') {
  wrapper = mount(InstanceCard, {
    global: { plugins: [createAurigaI18n(locale)] },
  });
  return wrapper;
}
function button(text: string) {
  return wrapper!.findAll('button').find((item) => item.text() === text)!;
}
beforeEach(() => {
  vi.useFakeTimers();
  vi.clearAllMocks();
  vi.stubGlobal('fetch', fetcher);
  document.cookie = 'auriga_csrf=test-csrf; path=/';
});
afterEach(() => {
  wrapper?.unmount();
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe('instance lifecycle', () => {
  it.each([
    { params: undefined, disabled: false },
    { params: { service: 'workspace', reason: 'disabled' }, disabled: true },
    { params: { service: 'iam', reason: 'disabled' }, disabled: false },
    { params: { service: 'workspace', reason: 'other' }, disabled: false },
    { params: { reason: 'disabled' }, disabled: false },
  ])(
    'explains explicit service unavailability in both languages: %j',
    async ({ params, disabled }) => {
      const status = 503;
      const code = 'common.unavailable';
      fetcher.mockResolvedValueOnce(
        json({ title: 'Failed', status, code, params }, status),
      );
      const view = render();
      await flushPromises();
      expect(view.get('[role=alert]').text()).toBe(
        (disabled
          ? '实例服务未启用，请联系管理员。'
          : '实例服务暂时不可用，请稍后重试。') + ' (common.unavailable)',
      );
      expect(view.text()).not.toContain('尚无实例');
      expect(button('启动并进入')).toBeUndefined();
      expect(vi.getTimerCount()).toBe(0);
      await vi.advanceTimersByTimeAsync(120000);
      expect(fetcher).toHaveBeenCalledTimes(1);
      view.unmount();

      fetcher.mockResolvedValueOnce(
        json({ title: 'Failed', status, code, params }, status),
      );
      const english = render('en');
      await flushPromises();
      expect(english.get('[role=alert]').text()).toBe(
        (disabled
          ? 'The instance service is not enabled. Please contact an administrator.'
          : 'The instance service is temporarily unavailable. Please retry later.') +
          ' (common.unavailable)',
      );
      fetcher.mockResolvedValueOnce(json({ items: [] }));
      await button('Retry').trigger('click');
      await flushPromises();
      expect(english.find('[role=alert]').exists()).toBe(false);
      expect(button('Start and enter').exists()).toBe(true);
      expect(enterInstance).not.toHaveBeenCalled();
    },
  );

  it.each([
    [500, 'common.internal'],
    [502, 'common.internal'],
    [404, 'common.internal'],
    [404, 'common.not_found'],
    [503, 'common.internal'],
    [500, 'common.unavailable'],
    [404, 'common.unavailable'],
  ])('preserves diagnostics for HTTP %i (%s)', async (status, code) => {
    fetcher.mockResolvedValueOnce(
      json(
        {
          title: 'Failed',
          status,
          code,
          params: { service: 'workspace', reason: 'disabled' },
        },
        status,
      ),
    );
    render();
    await flushPromises();
    expect(wrapper!.get('[role=alert]').text()).toBe(
      `无法完成请求，请重试。 (${code})`,
    );
    expect(wrapper!.text()).not.toContain('实例服务');
    expect(vi.getTimerCount()).toBe(0);
  });

  it('keeps permission failures distinct from service availability', async () => {
    fetcher.mockResolvedValueOnce(
      json({ title: 'Forbidden', status: 403, code: 'common.forbidden' }, 403),
    );
    render();
    await flushPromises();
    expect(wrapper!.get('[role=alert]').text()).toContain('无法完成请求');
    expect(wrapper!.text()).not.toContain('实例服务未启用');
  });

  it('loads an empty list without creating, then explicitly opens with CSRF and waits for both running and ready', async () => {
    fetcher.mockResolvedValueOnce(json({ items: [] }));
    render();
    await flushPromises();
    expect(wrapper!.text()).toContain('尚无实例');
    expect(fetcher).toHaveBeenCalledTimes(1);
    fetcher.mockResolvedValueOnce(json({ instance: starting, created: true }));
    const start = button('启动并进入');
    await Promise.all([start.trigger('click'), start.trigger('click')]);
    await flushPromises();
    expect(fetcher).toHaveBeenCalledTimes(2);
    const [path, init] = fetcher.mock.calls[1]!;
    expect(path).toBe('/api/workspace/v1/me/instance/open');
    expect(init?.method).toBe('POST');
    expect(init?.credentials).toBe('same-origin');
    expect(new Headers(init?.headers).get('X-Auriga-CSRF')).toBe('test-csrf');
    fetcher.mockResolvedValueOnce(json({ ...running, ready: false }));
    await vi.advanceTimersByTimeAsync(2000);
    expect(enterInstance).not.toHaveBeenCalled();
    fetcher.mockResolvedValueOnce(json({ ...starting, ready: true }));
    await vi.advanceTimersByTimeAsync(2000);
    expect(enterInstance).not.toHaveBeenCalled();
    fetcher.mockResolvedValueOnce(json(running));
    await vi.advanceTimersByTimeAsync(2000);
    expect(enterInstance).toHaveBeenCalledExactlyOnceWith('instance-1');
  });

  it('refreshes an existing ready instance through explicit open; localizes English', async () => {
    fetcher.mockResolvedValueOnce(json({ items: [running] }));
    render('en');
    await flushPromises();
    fetcher.mockResolvedValueOnce(json({ instance: starting, created: false }));
    await button('Enter instance').trigger('click');
    await flushPromises();
    expect(enterInstance).not.toHaveBeenCalled();
    expect(fetcher.mock.calls[1]![1]?.method).toBe('POST');
    fetcher.mockResolvedValueOnce(json(running));
    await vi.advanceTimersByTimeAsync(2000);
    expect(enterInstance).toHaveBeenCalledWith(running.id);
    expect(fetcher).toHaveBeenCalledTimes(3);
  });

  it('allows a ready instance pending restart through formal open', async () => {
    const pending: Instance = {
      ...running,
      phase: 'pending_restart',
      pendingRestart: true,
    };
    fetcher.mockResolvedValueOnce(json({ items: [pending] }));
    render();
    await flushPromises();
    expect(wrapper!.text()).toContain('待重启');
    expect(vi.getTimerCount()).toBe(0);
    fetcher.mockResolvedValueOnce(json({ instance: pending, created: false }));
    await button('进入实例').trigger('click');
    await flushPromises();
    expect(enterInstance).toHaveBeenCalledExactlyOnceWith(pending.id);
  });

  it('never retries a failed enter write automatically', async () => {
    fetcher.mockResolvedValueOnce(json({ items: [running] }));
    render();
    await flushPromises();
    fetcher.mockResolvedValueOnce(
      json(
        { title: 'Unavailable', status: 503, code: 'common.unavailable' },
        503,
      ),
    );
    await button('进入实例').trigger('click');
    await flushPromises();
    await vi.advanceTimersByTimeAsync(120000);
    expect(fetcher).toHaveBeenCalledTimes(2);
    expect(enterInstance).not.toHaveBeenCalled();
    expect(wrapper!.text()).toContain('实例服务暂时不可用');
  });

  it.each([403, 429, 503])(
    'keeps HTTP %i on the page and supports manual retry',
    async (status) => {
      const code =
        status === 403
          ? 'workspace.not_allowed'
          : status === 429
            ? 'workspace.admission_full'
            : 'common.unavailable';
      fetcher.mockResolvedValueOnce(json({ items: [stopped] }));
      render();
      await flushPromises();
      fetcher.mockResolvedValueOnce(
        json({ title: 'Rejected', code, status }, status),
      );
      await button('启动并进入').trigger('click');
      await flushPromises();
      expect(wrapper!.get('[role=alert]').text()).toContain(
        status === 503 ? '实例服务暂时不可用' : code,
      );
      expect(enterInstance).not.toHaveBeenCalled();
      expect(vi.getTimerCount()).toBe(0);
      fetcher.mockResolvedValueOnce(
        json({ instance: running, created: false }),
      );
      await button('启动并进入').trigger('click');
      await flushPromises();
      expect(enterInstance).toHaveBeenCalledWith(running.id);
    },
  );

  it('stops on session expiry during polling', async () => {
    fetcher.mockResolvedValueOnce(json({ items: [starting] }));
    render();
    await flushPromises();
    fetcher.mockResolvedValueOnce(
      json(
        { title: 'Expired', code: 'common.unauthenticated', status: 401 },
        401,
      ),
    );
    await vi.advanceTimersByTimeAsync(2000);
    expect(wrapper!.text()).toContain('登录已失效');
    expect(wrapper!.findAll('button')).toHaveLength(0);
    await vi.advanceTimersByTimeAsync(120000);
    expect(fetcher).toHaveBeenCalledTimes(2);
    expect(enterInstance).not.toHaveBeenCalled();
  });

  it('bounds polling and allows a fresh read after timeout', async () => {
    fetcher.mockImplementation(async () => json({ items: [starting] }));
    fetcher.mockResolvedValueOnce(json({ items: [starting] }));
    render();
    await flushPromises();
    fetcher.mockImplementation(async () => json(starting));
    await vi.advanceTimersByTimeAsync(120000);
    expect(wrapper!.text()).toContain('等待超时');
    expect(enterInstance).not.toHaveBeenCalled();
    expect(vi.getTimerCount()).toBe(0);
    fetcher.mockResolvedValueOnce(json({ items: [running] }));
    await button('重试').trigger('click');
    await flushPromises();
    expect(button('进入实例').exists()).toBe(true);
  });

  it.each(['cancel', 'unmount'])(
    'aborts and ignores a stale open response on %s',
    async (action) => {
      fetcher.mockResolvedValueOnce(json({ items: [stopped] }));
      render();
      await flushPromises();
      let resolve!: (value: Response) => void;
      fetcher.mockReturnValueOnce(
        new Promise((done) => {
          resolve = done;
        }),
      );
      await button('启动并进入').trigger('click');
      const signal = fetcher.mock.calls[1]![1]!.signal!;
      if (action === 'cancel') await button('取消等待').trigger('click');
      else wrapper!.unmount();
      expect(signal.aborted).toBe(true);
      resolve(json({ instance: running, created: false }));
      await flushPromises();
      expect(enterInstance).not.toHaveBeenCalled();
      expect(vi.getTimerCount()).toBe(0);
    },
  );

  it('shows failed instance state without entering or polling forever', async () => {
    fetcher.mockResolvedValueOnce(
      json({
        items: [
          {
            ...starting,
            phase: 'failed',
            error: { code: 'common.unavailable' },
          },
        ],
      }),
    );
    render();
    await flushPromises();
    expect(wrapper!.text()).toContain('实例启动失败');
    expect(enterInstance).not.toHaveBeenCalled();
    expect(vi.getTimerCount()).toBe(0);
  });

  it('encodes IDs into the fixed formal entry and rejects dot segments', () => {
    expect(instanceEntry('id/../../?url=https://evil.example')).toBe(
      '/open/id%2F..%2F..%2F%3Furl%3Dhttps%3A%2F%2Fevil.example',
    );
    expect(() => instanceEntry('..')).toThrow();
    expect(() => instanceEntry('')).toThrow();
  });
});
