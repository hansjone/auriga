import { setImmediate } from 'node:timers';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { mountPortalLogout } from '../src/native-logout/page';
import logoutStyles from '../src/native-logout/style.css?raw';
import { ApiError } from '../../shared/src/api/problem';

const me = {
  account: { id: 'account-1', status: 'active' },
  session: { audience: 'portal', current: true },
};
function json(value: unknown, status = 200) {
  return new Response(JSON.stringify(value), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}
function problem(status: number) {
  return json(
    {
      status,
      code: status === 401 ? 'common.unauthenticated' : 'common.forbidden',
      title: 'Private title',
      detail: 'Private detail',
    },
    status,
  );
}
function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason: unknown) => void;
  const promise = new Promise<T>((done, fail) => {
    resolve = done;
    reject = fail;
  });
  return { promise, resolve, reject };
}
const fetcher = vi.fn<typeof fetch>();
const flush = () => new Promise<void>((resolve) => setImmediate(resolve));
let root: HTMLElement;
let style: HTMLStyleElement;
let page: ReturnType<typeof mountPortalLogout> | undefined;
function render() {
  page = mountPortalLogout(root, { fetch: fetcher });
  return page;
}
function button(text: string) {
  const result = [...root.querySelectorAll('button')].find(
    (element) => element.textContent === text,
  );
  expect(result, `button ${text}`).toBeDefined();
  return result!;
}
function loginLink(visible = false) {
  const anchor = root.querySelector<HTMLAnchorElement>('.login-link')!;
  const url = new URL(anchor.href);
  expect(url.origin).toBe(globalThis.location.origin);
  expect(url.pathname).toBe('/instance-login.html');
  expect(url.search).toBe('');
  expect(url.hash).toBe('');
  expect(anchor.hidden).toBe(!visible);
  expect(globalThis.getComputedStyle(anchor).display === 'none').toBe(!visible);
  const back = root.querySelector<HTMLAnchorElement>('.back-link')!;
  expect(back.getAttribute('href')).toBe('/');
  expect(back.hidden).toBe(visible);
  expect(globalThis.getComputedStyle(back).display === 'none').toBe(visible);
  return anchor;
}
async function confirmedSession() {
  fetcher.mockResolvedValueOnce(json(me));
  render();
  await flush();
}
beforeEach(() => {
  vi.useFakeTimers();
  vi.clearAllMocks();
  fetcher.mockReset();
  globalThis.history.replaceState(null, '', '/logout.html');
  document.documentElement.lang = 'zh-CN';
  document.cookie = 'auriga_csrf=logout-test-csrf; path=/';
  root = document.createElement('main');
  style = document.createElement('style');
  style.textContent = logoutStyles;
  document.head.append(style);
  document.body.append(root);
});
afterEach(() => {
  page?.dispose();
  page = undefined;
  root.remove();
  style.remove();
  vi.useRealTimers();
  expect(
    fetcher.mock.calls.every(
      ([path, init]) =>
        (path === '/api/iam/v1/me' && init?.method === 'GET') ||
        (path === '/api/iam/v1/logout' && init?.method === 'POST'),
    ),
  ).toBe(true);
});

describe('native portal logout', () => {
  it.each([
    ['GET', 403, 401],
    ['GET', 500, 401],
    ['POST', 403, 401],
    ['POST', 500, 401],
    ['GET', 401, 403],
    ['GET', 401, 500],
    ['POST', 401, 403],
    ['POST', 401, 500],
  ] as const)(
    'uses trusted HTTP transport status for %s %i instead of body status %i',
    async (method, httpStatus, bodyStatus) => {
      if (method === 'POST') fetcher.mockResolvedValueOnce(json(me));
      fetcher.mockResolvedValueOnce(
        json(
          {
            status: bodyStatus,
            code: 'common.unauthenticated',
            title: 'Private mismatch',
            detail: 'Private response',
          },
          httpStatus,
        ),
      );
      render();
      await flush();
      if (method === 'POST') {
        button('确认退出').click();
        await flush();
      }
      if (httpStatus === 401) {
        expect(root.querySelector('[role="status"]')?.textContent).toBe(
          '尚未登录或登录已失效。',
        );
        expect(button('确认退出').hidden).toBe(true);
        loginLink(true);
      } else {
        expect(root.textContent).not.toContain('尚未登录或登录已失效');
        expect(root.querySelector('[role="alert"]')?.textContent).toContain(
          method === 'GET'
            ? '无法确认登录状态'
            : httpStatus === 403
              ? '当前入口无法处理'
              : '退出结果尚未确认',
        );
        expect(button(method === 'GET' ? '确认退出' : '重试退出').hidden).toBe(
          method === 'GET',
        );
        loginLink();
      }
      expect(root.textContent).not.toContain('Private');
      expect(fetcher).toHaveBeenCalledTimes(method === 'POST' ? 2 : 1);
    },
  );

  it.each(['GET', 'POST'])(
    'keeps %s network errors closed even when an untrusted error carries status 401',
    async (method) => {
      if (method === 'POST') fetcher.mockResolvedValueOnce(json(me));
      fetcher.mockRejectedValueOnce(
        new ApiError({
          status: 401,
          title: 'Private network error',
          code: 'common.unauthenticated',
        }),
      );
      render();
      await flush();
      if (method === 'POST') {
        button('确认退出').click();
        await flush();
      }
      expect(root.querySelector('[role="alert"]')?.textContent).toContain(
        method === 'GET' ? '无法确认登录状态' : '退出结果尚未确认',
      );
      expect(root.textContent).not.toContain('尚未登录或登录已失效');
      expect(root.textContent).not.toContain('Private');
      loginLink();
      expect(fetcher).toHaveBeenCalledTimes(method === 'POST' ? 2 : 1);
    },
  );

  it.each(['GET', 'POST'])(
    'does not trust %s redirected HTTP 401 responses as proof of expiry',
    async (method) => {
      if (method === 'POST') fetcher.mockResolvedValueOnce(json(me));
      const response = problem(401);
      Object.defineProperty(response, 'redirected', { value: true });
      fetcher.mockResolvedValueOnce(response);
      render();
      await flush();
      if (method === 'POST') {
        button('确认退出').click();
        await flush();
      }
      expect(root.querySelector('[role="alert"]')?.textContent).toContain(
        method === 'GET' ? '无法确认登录状态' : '退出结果尚未确认',
      );
      expect(root.textContent).not.toContain('尚未登录或登录已失效');
      loginLink();
      expect(fetcher).toHaveBeenCalledTimes(method === 'POST' ? 2 : 1);
    },
  );

  it.each([
    '',
    '?confirm=1&logout=1',
    '?returnTo=https://untrusted.example.com/&entryUrl=//untrusted.example.com/',
  ])(
    'GET %s only checks the session and never signs out automatically',
    async (search) => {
      globalThis.history.replaceState(null, '', `/logout.html${search}`);
      await confirmedSession();
      await vi.advanceTimersByTimeAsync(120_000);
      expect(fetcher).toHaveBeenCalledTimes(1);
      expect(fetcher.mock.calls[0]![0]).toBe('/api/iam/v1/me');
      expect(fetcher.mock.calls[0]![1]?.method).toBe('GET');
      expect(button('确认退出').hidden).toBe(false);
      expect(root.textContent).toContain('确认退出当前账号？');
      expect(root.textContent).not.toMatch(/门户会话|admin|device/);
      loginLink();
    },
  );

  it('requires explicit confirmation, sends current cookie/CSRF and only accepts 204', async () => {
    await confirmedSession();
    const originalCookie = document.cookie;
    fetcher.mockResolvedValueOnce(new Response(null, { status: 204 }));
    button('确认退出').click();
    button('确认退出').click();
    await flush();
    expect(fetcher).toHaveBeenCalledTimes(2);
    const [target, init] = fetcher.mock.calls[1]!;
    expect(target).toBe('/api/iam/v1/logout');
    expect(init?.method).toBe('POST');
    expect(init?.credentials).toBe('same-origin');
    expect(init?.redirect).toBe('error');
    expect(init?.body).toBeUndefined();
    expect(init?.signal).toBeUndefined();
    expect(new Headers(init?.headers).get('X-Auriga-CSRF')).toBe(
      'logout-test-csrf',
    );
    expect(new Headers(init?.headers).has('Authorization')).toBe(false);
    expect(root.querySelector('[role="status"]')?.textContent).toBe(
      '已退出登录。',
    );
    expect(button('确认退出').hidden).toBe(true);
    expect(document.cookie).toBe(originalCookie);
    expect(globalThis.location.pathname).toBe('/logout.html');
    loginLink(true);
    // Explicit login navigation belongs to the user; logout itself does not
    // reload the native login page and accidentally prepare a DSH instance.
    await vi.advanceTimersByTimeAsync(120_000);
    expect(fetcher).toHaveBeenCalledTimes(2);
  });

  it('does not invent a CSRF token when the current cookie is unavailable', async () => {
    document.cookie = 'auriga_csrf=; Max-Age=0; path=/';
    await confirmedSession();
    fetcher.mockResolvedValueOnce(problem(403));
    button('确认退出').click();
    await flush();
    const headers = new Headers(fetcher.mock.calls[1]![1]?.headers);
    expect(headers.has('X-Auriga-CSRF')).toBe(false);
    expect(root.textContent).toContain('当前入口无法处理');
    loginLink();
    expect(fetcher).toHaveBeenCalledTimes(2);
  });

  it.each([
    { audience: 'admin', current: true },
    { audience: 'device', current: true },
    { audience: 'portal', current: false },
  ])(
    'does not sign out a session outside the current portal context: %#',
    async (session) => {
      fetcher.mockResolvedValueOnce(json({ ...me, session }));
      render();
      await flush();
      expect(root.textContent).toContain('当前入口无法处理');
      expect(button('确认退出').hidden).toBe(true);
      button('确认退出').click();
      await flush();
      expect(fetcher).toHaveBeenCalledTimes(1);
      loginLink();
    },
  );

  it('distinguishes an anonymous or expired GET session from successful logout', async () => {
    fetcher.mockResolvedValueOnce(problem(401));
    render();
    await flush();
    expect(root.querySelector('[role="status"]')?.textContent).toBe(
      '尚未登录或登录已失效。',
    );
    expect(button('确认退出').hidden).toBe(true);
    expect(fetcher).toHaveBeenCalledTimes(1);
    loginLink(true);
  });

  it.each([403, 500])(
    'does not POST after failed GET session verification: HTTP %i',
    async (status) => {
      fetcher.mockResolvedValueOnce(problem(status));
      render();
      await flush();
      expect(root.textContent).toContain('无法确认登录状态');
      expect(root.textContent).not.toContain('Private');
      expect(button('确认退出').hidden).toBe(true);
      fetcher.mockResolvedValueOnce(json(me));
      button('刷新登录状态').click();
      await flush();
      expect(
        fetcher.mock.calls.map(([path, init]) => [path, init?.method]),
      ).toEqual([
        ['/api/iam/v1/me', 'GET'],
        ['/api/iam/v1/me', 'GET'],
      ]);
      expect(button('确认退出').hidden).toBe(false);
    },
  );

  it.each([{}, '<html>Private page</html>'])(
    'fails closed on malformed session responses: %#',
    async (value) => {
      fetcher.mockResolvedValueOnce(
        typeof value === 'string'
          ? new Response(value, { headers: { 'Content-Type': 'text/html' } })
          : json(value),
      );
      render();
      await flush();
      expect(root.textContent).toContain('无法确认登录状态');
      expect(root.textContent).not.toContain('Private');
      expect(fetcher).toHaveBeenCalledTimes(1);
      expect(button('确认退出').hidden).toBe(true);
    },
  );

  it.each([
    () =>
      new Response('<html>Private success</html>', {
        status: 200,
        headers: { 'Content-Type': 'text/html' },
      }),
    () => json({ success: true }),
    () => new Response(null, { status: 200 }),
    () => new Response(null, { status: 202 }),
    () =>
      new Response(null, {
        status: 302,
        headers: { Location: 'https://untrusted.example.com/' },
      }),
    () => {
      const response = new Response(null, { status: 204 });
      Object.defineProperty(response, 'redirected', { value: true });
      return response;
    },
  ])(
    'rejects an unexpected logout acknowledgement without claiming success: %#',
    async (response) => {
      await confirmedSession();
      fetcher.mockResolvedValueOnce(response());
      button('确认退出').click();
      await flush();
      expect(root.querySelector('[role="alert"]')?.textContent).toContain(
        '退出结果尚未确认',
      );
      expect(root.textContent).not.toContain('Private');
      expect(button('重试退出').hidden).toBe(false);
      expect(fetcher).toHaveBeenCalledTimes(2);
      expect(globalThis.location.pathname).toBe('/logout.html');
      loginLink();
    },
  );

  it('distinguishes POST 401 from a confirmed 204 sign-out and prevents replay', async () => {
    await confirmedSession();
    fetcher.mockResolvedValueOnce(problem(401));
    button('确认退出').click();
    await flush();
    expect(root.querySelector('[role="status"]')?.textContent).toBe(
      '尚未登录或登录已失效。',
    );
    expect(button('确认退出').hidden).toBe(true);
    await vi.advanceTimersByTimeAsync(120_000);
    expect(fetcher).toHaveBeenCalledTimes(2);
    loginLink(true);
  });

  it.each([403, 500])(
    'keeps POST %i failure explicit and retries only after another confirmation',
    async (status) => {
      await confirmedSession();
      fetcher.mockResolvedValueOnce(problem(status));
      button('确认退出').click();
      await flush();
      expect(root.textContent).toContain(
        status === 403 ? '当前入口无法处理' : '退出结果尚未确认',
      );
      loginLink();
      expect(root.textContent).not.toContain('Private');
      await vi.advanceTimersByTimeAsync(120_000);
      expect(fetcher).toHaveBeenCalledTimes(2);
      fetcher.mockResolvedValueOnce(new Response(null, { status: 204 }));
      const retry = button('重试退出');
      retry.click();
      retry.click();
      await flush();
      expect(fetcher).toHaveBeenCalledTimes(3);
      expect(fetcher.mock.calls[2]![1]?.method).toBe('POST');
      expect(root.querySelector('[role="status"]')?.textContent).toBe(
        '已退出登录。',
      );
    },
  );

  it('keeps network failure details private and lets session recheck precede another explicit logout', async () => {
    await confirmedSession();
    fetcher.mockRejectedValueOnce(new Error('Private network details'));
    button('确认退出').click();
    await flush();
    expect(root.textContent).toContain('退出结果尚未确认');
    expect(root.textContent).not.toContain('Private');
    loginLink();
    fetcher.mockResolvedValueOnce(json(me));
    button('刷新登录状态').click();
    await flush();
    expect(fetcher.mock.calls[2]![1]?.method).toBe('GET');
    expect(fetcher).toHaveBeenCalledTimes(3);
    expect(button('确认退出').hidden).toBe(false);
  });

  it('allows locale changes while a POST is pending without another request or premature success', async () => {
    await confirmedSession();
    const late = deferred<Response>();
    fetcher.mockReturnValueOnce(late.promise);
    button('确认退出').click();
    page!.setLocale('en');
    page!.setLocale('zh-CN');
    page!.setLocale('en');
    expect(root.textContent).toContain('Signing out');
    loginLink();
    expect(button('Confirm sign-out').disabled).toBe(true);
    button('Confirm sign-out').click();
    expect(fetcher).toHaveBeenCalledTimes(2);
    late.resolve(new Response(null, { status: 204 }));
    await flush();
    expect(root.querySelector('[role="status"]')?.textContent).toBe(
      'You have signed out.',
    );
    loginLink(true);
  });

  it('cancels a pending check without accepting a late session or submitting logout', async () => {
    const late = deferred<Response>();
    fetcher.mockReturnValueOnce(late.promise);
    render();
    loginLink();
    button('取消').click();
    expect(button('刷新登录状态').disabled).toBe(true);
    late.resolve(json(me));
    await flush();
    expect(root.textContent).toContain('若要退出，请重新确认');
    expect(button('确认退出').hidden).toBe(true);
    expect(fetcher).toHaveBeenCalledTimes(1);
    expect(button('刷新登录状态').disabled).toBe(false);
  });

  it.each(['success', 'failure'])(
    'does not revive a locally cancelled pending POST after late %s',
    async (result) => {
      await confirmedSession();
      const late = deferred<Response>();
      fetcher.mockReturnValueOnce(late.promise);
      button('确认退出').click();
      button('取消').click();
      expect(root.textContent).toContain('退出结果尚未确认');
      expect(button('刷新登录状态').disabled).toBe(true);
      button('刷新登录状态').click();
      expect(fetcher).toHaveBeenCalledTimes(2);
      if (result === 'success')
        late.resolve(new Response(null, { status: 204 }));
      else late.reject(new Error('Private late failure'));
      await flush();
      expect(root.querySelector('[role="status"]')?.textContent).toContain(
        '已取消等待',
      );
      loginLink();
      expect(root.textContent).not.toContain('已退出登录。');
      expect(root.textContent).not.toContain('Private');
      expect(button('确认退出').hidden).toBe(true);
      fetcher.mockResolvedValueOnce(problem(401));
      button('刷新登录状态').click();
      await flush();
      expect(root.querySelector('[role="status"]')?.textContent).toBe(
        '尚未登录或登录已失效。',
      );
      expect(fetcher).toHaveBeenCalledTimes(3);
    },
  );

  it('cancels a confirmed session locally without signing out and requires a fresh check', async () => {
    await confirmedSession();
    button('取消').click();
    expect(fetcher).toHaveBeenCalledTimes(1);
    expect(button('确认退出').hidden).toBe(true);
    fetcher.mockResolvedValueOnce(json(me));
    button('刷新登录状态').click();
    await flush();
    expect(fetcher).toHaveBeenCalledTimes(2);
    expect(button('确认退出').hidden).toBe(false);
  });

  it.each(['check', 'logout'])(
    'unmount/pagehide makes late %s results inert and removes listeners',
    async (phase) => {
      const late = deferred<Response>();
      if (phase === 'check') {
        fetcher.mockReturnValueOnce(late.promise);
        render();
      } else {
        await confirmedSession();
        fetcher.mockReturnValueOnce(late.promise);
        button('确认退出').click();
      }
      const staleConfirm = button('确认退出');
      const staleLanguage = root.querySelector('select')!;
      globalThis.dispatchEvent(new Event('pagehide'));
      page!.dispose();
      late.resolve(
        phase === 'check' ? json(me) : new Response(null, { status: 204 }),
      );
      await flush();
      staleConfirm.click();
      staleLanguage.dispatchEvent(new Event('change'));
      page!.setLocale('en');
      expect(root.children).toHaveLength(0);
      expect(fetcher).toHaveBeenCalledTimes(phase === 'check' ? 1 : 2);
      expect(vi.getTimerCount()).toBe(0);
    },
  );
});
