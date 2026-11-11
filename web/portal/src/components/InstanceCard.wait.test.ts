import { mount, flushPromises } from '@vue/test-utils';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { ApiError, createAurigaI18n } from '@auriga/shared';
import InstanceCard from './InstanceCard.vue';
import { enterInstance, type Instance } from '../workspace';

const api = vi.hoisted(() => ({ list: vi.fn(), open: vi.fn(), get: vi.fn() }));
vi.mock('../workspace', async (original) => ({
  ...(await original<typeof import('../workspace')>()),
  createWorkspaceApi: () => api,
  enterInstance: vi.fn(),
}));
// Only the fields consumed by the card are relevant to this fixture.
const starting = {
  id: 'instance-1',
  name: 'My DSH',
  desired: 'running',
  phase: 'starting',
  ready: false,
  generation: 1,
  observedGeneration: 1,
  error: null,
} as Instance;
const ready = { ...starting, phase: 'running', ready: true } as Instance;
let view: ReturnType<typeof mount>;
function button(text: string) {
  return view.findAll('button').find((item) => item.text() === text)!;
}
async function start() {
  view = mount(InstanceCard, {
    global: { plugins: [createAurigaI18n('zh-CN')] },
  });
  await flushPromises();
  await button('启动并进入').trigger('click');
  await flushPromises();
}
beforeEach(() => {
  vi.useFakeTimers();
  vi.clearAllMocks();
  vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('visible');
  api.list.mockResolvedValue({ items: [] });
  api.open.mockResolvedValue({ instance: starting, created: true });
  api.get.mockResolvedValue(starting);
});
afterEach(() => {
  view?.unmount();
  vi.restoreAllMocks();
  vi.useRealTimers();
});

it('keeps accepted startup beyond 120 seconds and enters exactly once using only reads', async () => {
  await start();
  await vi.advanceTimersByTimeAsync(150000);
  expect(view.find('[role=alert]').exists()).toBe(false);
  expect(view.get('[role=status]').text()).toBe('正在启动');
  expect(api.get.mock.calls.length).toBeLessThan(20);
  api.get.mockResolvedValue(ready);
  await vi.advanceTimersByTimeAsync(30000);
  expect(enterInstance).toHaveBeenCalledExactlyOnceWith(starting.id);
  await vi.advanceTimersByTimeAsync(60000);
  expect(enterInstance).toHaveBeenCalledTimes(1);
  expect(api.open).toHaveBeenCalledTimes(1);
});

it('waits through same-generation recovery diagnostics and transient read failures', async () => {
  api.open.mockResolvedValue({
    instance: { ...starting, error: { code: 'common.unavailable' } },
  });
  await start();
  api.get.mockRejectedValueOnce(new TypeError('network unavailable'));
  await vi.advanceTimersByTimeAsync(2000);
  expect(view.find('[role=alert]').exists()).toBe(false);
  api.get.mockResolvedValue(ready);
  await vi.advanceTimersByTimeAsync(30000);
  expect(enterInstance).toHaveBeenCalledTimes(1);
  expect(api.open).toHaveBeenCalledTimes(1);
});

it.each(['cancel', 'unmount', 'pagehide'])(
  'clears intent and ignores late READY after %s',
  async (action) => {
    await start();
    let resolve!: (value: Instance) => void;
    api.get.mockReturnValueOnce(
      new Promise((done) => {
        resolve = done;
      }),
    );
    await vi.advanceTimersByTimeAsync(2000);
    if (action === 'cancel') await button('取消等待').trigger('click');
    else if (action === 'unmount') view.unmount();
    else window.dispatchEvent(new Event('pagehide'));
    resolve(ready);
    await flushPromises();
    await vi.advanceTimersByTimeAsync(60000);
    expect(enterInstance).not.toHaveBeenCalled();
    expect(vi.getTimerCount()).toBe(0);
  },
);

it.each(['failed', 'stopped', 'stopping', 'deleting'] as const)(
  'clears intent on %s even with stale ready',
  async (phase) => {
    await start();
    api.get.mockResolvedValue({ ...ready, phase });
    await vi.advanceTimersByTimeAsync(2000);
    expect(enterInstance).not.toHaveBeenCalled();
    expect(vi.getTimerCount()).toBe(0);
    expect(view.find('[role=alert]').exists()).toBe(true);
  },
);

it('pauses reads and navigation while hidden and resumes without replaying open', async () => {
  await start();
  vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('hidden');
  document.dispatchEvent(new Event('visibilitychange'));
  await vi.advanceTimersByTimeAsync(180000);
  expect(api.get).not.toHaveBeenCalled();
  api.get.mockResolvedValue(ready);
  vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('visible');
  document.dispatchEvent(new Event('visibilitychange'));
  await flushPromises();
  expect(enterInstance).toHaveBeenCalledExactlyOnceWith(starting.id);
  expect(api.open).toHaveBeenCalledTimes(1);
});

it.each([401, 403])(
  'clears accepted intent on HTTP %i without another write',
  async (status) => {
    await start();
    api.get.mockRejectedValueOnce(
      new ApiError({
        status,
        title: 'Rejected',
        code: status === 401 ? 'common.unauthenticated' : 'common.forbidden',
      }),
    );
    await vi.advanceTimersByTimeAsync(2000);
    expect(view.get('[role=alert]').text()).toContain(
      status === 401 ? '登录已失效' : '无法完成请求',
    );
    api.get.mockResolvedValue(ready);
    await vi.advanceTimersByTimeAsync(150000);
    expect(enterInstance).not.toHaveBeenCalled();
    expect(api.get).toHaveBeenCalledTimes(1);
    expect(api.open).toHaveBeenCalledTimes(1);
    expect(vi.getTimerCount()).toBe(0);
  },
);

it('defers an in-flight READY received while hidden until a fresh visible read', async () => {
  await start();
  let resolve!: (value: Instance) => void;
  api.get.mockReturnValueOnce(
    new Promise((done) => {
      resolve = done;
    }),
  );
  await vi.advanceTimersByTimeAsync(2000);
  vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('hidden');
  document.dispatchEvent(new Event('visibilitychange'));
  resolve(ready);
  await flushPromises();
  expect(enterInstance).not.toHaveBeenCalled();
  api.get.mockResolvedValue(ready);
  vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('visible');
  document.dispatchEvent(new Event('visibilitychange'));
  await flushPromises();
  expect(enterInstance).toHaveBeenCalledExactlyOnceWith(starting.id);
});

it('bounds hung reads independently and continues waiting without replaying open', async () => {
  await start();
  api.get.mockImplementationOnce(
    (_id: string, signal: AbortSignal) =>
      new Promise((_resolve, reject) => {
        signal.addEventListener('abort', () =>
          reject(new DOMException('Aborted', 'AbortError')),
        );
      }),
  );
  await vi.advanceTimersByTimeAsync(32000);
  expect(view.find('[role=alert]').exists()).toBe(false);
  api.get.mockResolvedValue(ready);
  await vi.advanceTimersByTimeAsync(15000);
  expect(enterInstance).toHaveBeenCalledExactlyOnceWith(starting.id);
  expect(api.open).toHaveBeenCalledTimes(1);
});

it.each(['network', 'server', 'timeout'])(
  'pauses after five consecutive %s failures and retries reads with the original entry intent',
  async (failure) => {
    await start();
    if (failure === 'timeout') {
      api.get.mockImplementation(
        (_id: string, signal: AbortSignal) =>
          new Promise((_resolve, reject) => {
            signal.addEventListener('abort', () =>
              reject(new DOMException('Aborted', 'AbortError')),
            );
          }),
      );
    } else {
      api.get.mockRejectedValue(
        failure === 'server'
          ? new ApiError({
              status: 503,
              code: 'common.unavailable',
              title: 'Unavailable',
            })
          : new TypeError('network unavailable'),
      );
    }
    await vi.advanceTimersByTimeAsync(240000);
    expect(api.get).toHaveBeenCalledTimes(5);
    expect(view.get('[role=alert]').text()).toBe('暂时无法获取状态');
    expect(view.find('[role=status]').exists()).toBe(false);
    expect(vi.getTimerCount()).toBe(0);
    vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('hidden');
    document.dispatchEvent(new Event('visibilitychange'));
    vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('visible');
    document.dispatchEvent(new Event('visibilitychange'));
    await vi.advanceTimersByTimeAsync(60000);
    expect(api.get).toHaveBeenCalledTimes(5);
    api.get.mockResolvedValue(ready);
    await button('重试').trigger('click');
    await flushPromises();
    await vi.advanceTimersByTimeAsync(60000);
    expect(api.open).toHaveBeenCalledTimes(1);
    expect(api.list).toHaveBeenCalledTimes(1);
    expect(enterInstance).toHaveBeenCalledExactlyOnceWith(starting.id);
    expect(vi.getTimerCount()).toBe(0);
  },
);

it('resets consecutive failures after a successful non-ready response', async () => {
  await start();
  api.get.mockRejectedValue(new TypeError('offline'));
  for (let i = 0; i < 4; i++) {
    api.get.mockRejectedValueOnce(new TypeError('offline'));
  }
  api.get.mockResolvedValueOnce(starting);
  await vi.advanceTimersByTimeAsync(18000);
  expect(api.get).toHaveBeenCalledTimes(5);
  await vi.advanceTimersByTimeAsync(60000);
  expect(api.get).toHaveBeenCalledTimes(9);
  expect(view.find('[role=alert]').exists()).toBe(false);
  await vi.advanceTimersByTimeAsync(15000);
  expect(api.get).toHaveBeenCalledTimes(10);
  expect(view.get('[role=alert]').text()).toBe('暂时无法获取状态');
});

it.each(['cancel', 'unmount', 'pagehide'])(
  'clears paused entry intent on %s',
  async (action) => {
    await start();
    api.get.mockRejectedValue(new TypeError('offline'));
    await vi.advanceTimersByTimeAsync(60000);
    expect(view.get('[role=alert]').text()).toBe('暂时无法获取状态');
    if (action === 'cancel') await button('取消等待').trigger('click');
    else if (action === 'unmount') view.unmount();
    else window.dispatchEvent(new Event('pagehide'));
    if (action === 'cancel') {
      api.list.mockResolvedValue({ items: [ready] });
      await button('重试').trigger('click');
      await flushPromises();
      expect(api.list).toHaveBeenCalledTimes(2);
    }
    document.dispatchEvent(new Event('visibilitychange'));
    await vi.advanceTimersByTimeAsync(60000);
    expect(enterInstance).not.toHaveBeenCalled();
    expect(api.open).toHaveBeenCalledTimes(1);
    expect(api.get).toHaveBeenCalledTimes(5);
    expect(vi.getTimerCount()).toBe(0);
  },
);

it('keeps a read-only observation read-only after failure and retry', async () => {
  api.list.mockResolvedValue({ items: [starting] });
  api.get.mockRejectedValue(new TypeError('offline'));
  view = mount(InstanceCard, {
    global: { plugins: [createAurigaI18n('zh-CN')] },
  });
  await flushPromises();
  await vi.advanceTimersByTimeAsync(60000);
  expect(view.get('[role=alert]').text()).toBe('暂时无法获取状态');
  api.get.mockResolvedValue(ready);
  await button('重试').trigger('click');
  await flushPromises();
  expect(enterInstance).not.toHaveBeenCalled();
  expect(api.open).not.toHaveBeenCalled();
  expect(vi.getTimerCount()).toBe(0);
});

it.each([408, 425])('retries temporary HTTP %i reads', async (status) => {
  await start();
  api.get.mockRejectedValueOnce(
    new ApiError({ status, title: 'Temporary', code: 'common.unavailable' }),
  );
  await vi.advanceTimersByTimeAsync(2000);
  expect(view.find('[role=alert]').exists()).toBe(false);
  api.get.mockResolvedValue(ready);
  await vi.advanceTimersByTimeAsync(2000);
  expect(enterInstance).toHaveBeenCalledExactlyOnceWith(starting.id);
});

it('clears entry intent and explains a deleted instance on HTTP 404', async () => {
  await start();
  api.get.mockRejectedValueOnce(
    new ApiError({ status: 404, title: 'Missing', code: 'common.not_found' }),
  );
  await vi.advanceTimersByTimeAsync(2000);
  expect(view.get('[role=alert]').text()).toBe('实例已停止。');
  expect(enterInstance).not.toHaveBeenCalled();
  expect(vi.getTimerCount()).toBe(0);
});
