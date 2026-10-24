import { setImmediate } from 'node:timers';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import logoutStyles from '../src/native-logout/style.css?raw';

const me = {
  account: { id: 'account-1', status: 'active' },
  session: { audience: 'portal', current: true },
};
const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
const anonymous = () =>
  json({ status: 401, code: 'common.unauthenticated', title: 'Expired' }, 401);
const starting = {
  id: 'current-instance',
  desired: 'running',
  phase: 'starting',
  ready: false,
};
const ready = { ...starting, phase: 'running', ready: true };
const flush = () => new Promise<void>((resolve) => setImmediate(resolve));
const fetcher = vi.fn<typeof fetch>();
const navigate = vi.fn();
let root: HTMLElement;
let style: HTMLStyleElement;
let cleanup: (() => void) | undefined;
function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}
function transition(type: 'pagehide' | 'pageshow', persisted: boolean) {
  globalThis.dispatchEvent(
    new globalThis.PageTransitionEvent(type, { persisted }),
  );
}
function button(text: string) {
  const element = [...root.querySelectorAll('button')].find(
    (item) => item.textContent === text,
  );
  expect(element, `button ${text}`).toBeDefined();
  return element!;
}
async function bootLogout() {
  root.id = 'portal-logout';
  const page = await import('../src/native-logout/page');
  const mount = vi.spyOn(page, 'mountPortalLogout');
  const entry = await import('../src/native-logout/main');
  // Undefined in the pre-fix entry, so the red tests exercise the old real boot.
  cleanup = Reflect.get(entry, 'disposeEntry');
  await flush();
  return mount;
}
async function bootLogin() {
  root.id = 'instance-login';
  const page = await import('../src/native-login/page');
  const original = page.mountInstanceLogin;
  const mount = vi
    .spyOn(page, 'mountInstanceLogin')
    .mockImplementation((element) => original(element, { navigate }));
  const entry = await import('../src/native-login/main');
  cleanup = Reflect.get(entry, 'disposeEntry');
  await flush();
  return mount;
}
beforeEach(() => {
  vi.useFakeTimers();
  vi.resetModules();
  vi.clearAllMocks();
  fetcher.mockReset();
  vi.stubGlobal('fetch', fetcher);
  globalThis.history.replaceState(null, '', '/logout.html');
  document.documentElement.lang = 'zh-CN';
  document.cookie = 'auriga_csrf=lifecycle-csrf; path=/';
  root = document.createElement('main');
  document.body.append(root);
  style = document.createElement('style');
  style.textContent = logoutStyles;
  document.head.append(style);
});
afterEach(() => {
  cleanup?.();
  cleanup = undefined;
  transition('pagehide', false);
  root.remove();
  style.remove();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

describe('native entry document lifecycle', () => {
  it('restores logout with a fresh GET and no automatic POST, without duplicate initial or repeated pageshow mounts', async () => {
    fetcher.mockImplementation(async () => json(me));
    const mount = await bootLogout();
    transition('pageshow', false);
    await flush();
    expect(mount).toHaveBeenCalledTimes(1);
    expect(fetcher).toHaveBeenCalledTimes(1);
    for (let cycle = 0; cycle < 3; cycle++) {
      transition('pagehide', true);
      expect(root.childElementCount).toBe(0);
      transition('pageshow', true);
      await flush();
      expect(root.querySelectorAll('.portal-logout')).toHaveLength(1);
      expect(root.textContent).toContain('确认退出当前账号？');
      expect(mount).toHaveBeenCalledTimes(cycle + 2);
      expect(fetcher).toHaveBeenCalledTimes(cycle + 2);
      transition('pageshow', true);
      transition('pageshow', false);
      await flush();
      expect(mount).toHaveBeenCalledTimes(cycle + 2);
    }
    expect(
      fetcher.mock.calls.every(
        ([path, init]) => path === '/api/iam/v1/me' && init?.method === 'GET',
      ),
    ).toBe(true);
    cleanup?.();
    transition('pageshow', true);
    await flush();
    expect(root.childElementCount).toBe(0);
    expect(mount).toHaveBeenCalledTimes(4);
  });

  it('does not restore an old successful sign-out view and still needs explicit confirmation after rechecking', async () => {
    fetcher
      .mockResolvedValueOnce(json(me))
      .mockResolvedValueOnce(new Response(null, { status: 204 }));
    await bootLogout();
    button('确认退出').click();
    await flush();
    expect(root.textContent).toContain('已退出登录。');
    transition('pagehide', true);
    const fresh = deferred<Response>();
    fetcher.mockReturnValueOnce(fresh.promise);
    transition('pageshow', true);
    await flush();
    expect(root.textContent).toContain('正在确认登录状态');
    expect(root.textContent).not.toContain('已退出登录。');
    fresh.resolve(json(me));
    await flush();
    expect(root.textContent).toContain('确认退出当前账号？');
    expect(
      fetcher.mock.calls.filter(([, init]) => init?.method === 'POST'),
    ).toHaveLength(1);
  });

  it.each([204, 500])(
    'does not let a pre-hide logout HTTP %i result replace the restored session check',
    async (status) => {
      const late = deferred<Response>();
      fetcher
        .mockResolvedValueOnce(json(me))
        .mockReturnValueOnce(late.promise)
        .mockResolvedValueOnce(anonymous());
      await bootLogout();
      button('确认退出').click();
      const stale = button('确认退出');
      transition('pagehide', true);
      transition('pageshow', true);
      await flush();
      expect(root.textContent).toContain('尚未登录或登录已失效');
      late.resolve(
        status === 204
          ? new Response(null, { status })
          : json({ status: 401 }, status),
      );
      await flush();
      stale.click();
      await flush();
      expect(root.textContent).toContain('尚未登录或登录已失效');
      expect(root.textContent).not.toContain('已退出登录。');
      expect(root.querySelectorAll('.portal-logout')).toHaveLength(1);
      expect(
        fetcher.mock.calls.map(([path, init]) => [path, init?.method]),
      ).toEqual([
        ['/api/iam/v1/me', 'GET'],
        ['/api/iam/v1/logout', 'POST'],
        ['/api/iam/v1/me', 'GET'],
      ]);
    },
  );

  it('ignores a pre-hide me response after the restored logout controller confirms a current session', async () => {
    const late = deferred<Response>();
    fetcher.mockReturnValueOnce(late.promise).mockResolvedValueOnce(json(me));
    await bootLogout();
    transition('pagehide', true);
    transition('pageshow', true);
    await flush();
    expect(root.textContent).toContain('确认退出当前账号？');
    late.resolve(anonymous());
    await flush();
    expect(root.textContent).toContain('确认退出当前账号？');
    expect(root.textContent).not.toContain('尚未登录或登录已失效');
    expect(fetcher).toHaveBeenCalledTimes(2);
  });

  it.each(['GET', 'POST'])(
    'removes entry listeners on explicit disposal and leaves late %s responses inert',
    async (method) => {
      const late = deferred<Response>();
      if (method === 'POST') fetcher.mockResolvedValueOnce(json(me));
      fetcher.mockReturnValueOnce(late.promise);
      const mount = await bootLogout();
      if (method === 'POST') button('确认退出').click();
      cleanup?.();
      cleanup?.();
      transition('pagehide', true);
      transition('pageshow', true);
      late.resolve(
        method === 'POST' ? new Response(null, { status: 204 }) : json(me),
      );
      await flush();
      expect(root.childElementCount).toBe(0);
      expect(mount).toHaveBeenCalledTimes(1);
      expect(fetcher).toHaveBeenCalledTimes(method === 'POST' ? 2 : 1);
      expect(vi.getTimerCount()).toBe(0);
    },
  );

  it('restores the native login entry once per hide and reads identity again instead of leaving it blank', async () => {
    globalThis.history.replaceState(null, '', '/instance-login.html');
    fetcher.mockImplementation(async (path) =>
      String(path).endsWith('/me') ? anonymous() : json({ items: [] }),
    );
    const mount = await bootLogin();
    transition('pageshow', false);
    await flush();
    expect(mount).toHaveBeenCalledTimes(1);
    for (let cycle = 0; cycle < 2; cycle++) {
      transition('pagehide', true);
      transition('pageshow', true);
      await flush();
      expect(root.querySelectorAll('.instance-login')).toHaveLength(1);
      expect(root.textContent).toContain('当前没有可用的登录方式');
      expect(mount).toHaveBeenCalledTimes(cycle + 2);
      transition('pageshow', true);
      await flush();
      expect(mount).toHaveBeenCalledTimes(cycle + 2);
    }
    expect(fetcher).toHaveBeenCalledTimes(6);
    expect(navigate).not.toHaveBeenCalled();
  });

  it('does not let the old login open navigate or overwrite the restored anonymous controller', async () => {
    globalThis.history.replaceState(null, '', '/instance-login.html');
    const late = deferred<Response>();
    fetcher
      .mockResolvedValueOnce(json(me))
      .mockReturnValueOnce(late.promise)
      .mockResolvedValueOnce(anonymous())
      .mockResolvedValueOnce(json({ items: [] }));
    await bootLogin();
    transition('pagehide', true);
    transition('pageshow', true);
    await flush();
    expect(root.textContent).toContain('当前没有可用的登录方式');
    late.resolve(json({ instance: ready }));
    await flush();
    expect(navigate).not.toHaveBeenCalled();
    expect(root.textContent).toContain('当前没有可用的登录方式');
    expect(fetcher).toHaveBeenCalledTimes(4);
    expect(vi.getTimerCount()).toBe(0);
  });

  it('keeps the normal me/open/fixed entry flow when native login resumes with a current portal session', async () => {
    globalThis.history.replaceState(null, '', '/instance-login.html');
    fetcher
      .mockResolvedValueOnce(anonymous())
      .mockResolvedValueOnce(json({ items: [] }));
    const mount = await bootLogin();
    transition('pagehide', true);
    fetcher
      .mockResolvedValueOnce(json(me))
      .mockResolvedValueOnce(json({ instance: ready }));
    transition('pageshow', true);
    await flush();
    expect(navigate).toHaveBeenCalledExactlyOnceWith('/open/current-instance');
    expect(
      fetcher.mock.calls.filter(([path]) =>
        String(path).endsWith('/instance/open'),
      ),
    ).toHaveLength(1);
    transition('pageshow', true);
    await flush();
    expect(mount).toHaveBeenCalledTimes(2);
    expect(fetcher).toHaveBeenCalledTimes(4);
  });
});
