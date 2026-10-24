import { setImmediate } from 'node:timers';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { mountInstanceLogin } from '../src/native-login/page';
import type { LoginTransaction } from '../../shared/src/api/iam';
import type { ClientStep } from '../../../clients/dsh-plugin/src/generated/steps';
import type { Instance } from '../src/workspace';

const txId = '01900000-0000-7000-8000-000000000001';
const providerId = '01900000-0000-7000-8000-000000000002';
const provider = {
  id: providerId,
  connectorType: 'team',
  order: 1,
  primaryStep: 'form',
  displayName: { en: 'Team account', 'zh-CN': '团队账号' },
};
const password: ClientStep = {
  type: 'form',
  formId: 'password',
  fields: [
    {
      name: 'username',
      kind: 'text',
      required: true,
      label: { en: 'Username' },
    },
    {
      name: 'password',
      kind: 'password',
      required: true,
      label: { en: 'Password' },
    },
  ],
};
const otp: ClientStep = {
  type: 'form',
  formId: 'totp',
  fields: [
    { name: 'code', kind: 'otp', required: true, label: { en: 'TOTP' } },
  ],
};
const complete: ClientStep = {
  type: 'complete',
  redirectTo: 'https://untrusted.example.com/',
};
function transaction(
  step: ClientStep,
  status: LoginTransaction['status'] = 'pending',
) {
  return {
    id: txId,
    purpose: 'login',
    status,
    provider: {
      id: providerId,
      connectorType: 'team',
      displayName: provider.displayName,
    },
    step,
    expiresAt: '2099-01-01T00:00:00Z',
  };
}
const me = {
  account: { id: 'owner', status: 'active' },
  session: { audience: 'portal', current: true },
};
const starting: Instance = {
  id: 'instance/one?x',
  tenantId: 'tenant',
  ownerId: 'owner',
  name: 'DSH',
  runtime: 'docker',
  desired: 'running',
  phase: 'starting',
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
const ready: Instance = { ...starting, phase: 'running', ready: true };
function json(
  value: unknown,
  status = 200,
  headers: Record<string, string> = {},
) {
  return new Response(JSON.stringify(value), {
    status,
    headers: { 'Content-Type': 'application/json', ...headers },
  });
}
const expired = () =>
  json({ title: 'Expired', code: 'common.unauthenticated', status: 401 }, 401);
const fetcher = vi.fn<typeof fetch>();
const navigate = vi.fn();
let root: HTMLElement;
let page: ReturnType<typeof mountInstanceLogin> | undefined;
const flush = () => new Promise<void>((resolve) => setImmediate(resolve));
function render() {
  page = mountInstanceLogin(root, { fetch: fetcher, navigate });
  return page;
}
function button(text: string, selector = 'button') {
  const result = [...root.querySelectorAll<HTMLButtonElement>(selector)].find(
    (item) => item.textContent === text,
  );
  expect(result, `button ${text}`).toBeDefined();
  return result!;
}
function field(name: string, value: string) {
  root.querySelector<HTMLInputElement>(`input[name="${name}"]`)!.value = value;
}
function submit() {
  root
    .querySelector('form')!
    .dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
}
function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}
async function anonymous() {
  fetcher
    .mockResolvedValueOnce(expired())
    .mockResolvedValueOnce(json({ items: [provider] }));
  render();
  await flush();
}
async function showPassword() {
  await anonymous();
  fetcher.mockResolvedValueOnce(json(transaction(password)));
  button('团队账号').click();
  await flush();
}
beforeEach(() => {
  vi.useFakeTimers();
  vi.clearAllMocks();
  fetcher.mockReset();
  globalThis.history.replaceState(null, '', '/instance-login.html');
  document.documentElement.lang = 'zh-CN';
  document.cookie = 'auriga_csrf=test-csrf; path=/';
  root = document.createElement('main');
  document.body.append(root);
});
afterEach(() => {
  page?.dispose();
  page = undefined;
  root.remove();
  vi.useRealTimers();
});

describe('native cloud sign-in', () => {
  it('uses real IAM form/TOTP shapes, verifies the session, opens and waits for ready before entering', async () => {
    await showPassword();
    expect(JSON.parse(String(fetcher.mock.calls[2]![1]?.body))).toEqual({
      providerId,
      purpose: 'login',
      returnTo: '/instance-login.html',
    });
    fetcher.mockResolvedValueOnce(json(transaction(otp)));
    field('username', 'alice');
    field('password', 'test-password');
    submit();
    submit();
    await flush();
    expect(fetcher).toHaveBeenCalledTimes(4);
    expect(JSON.parse(String(fetcher.mock.calls[3]![1]?.body))).toEqual({
      type: 'form',
      formId: 'password',
      values: { username: 'alice', password: 'test-password' },
    });
    expect(root.querySelector('input[type="password"]')).toBeNull();
    fetcher
      .mockResolvedValueOnce(json(transaction(complete, 'completed')))
      .mockResolvedValueOnce(json(me))
      .mockResolvedValueOnce(json({ instance: starting, created: true }));
    field('code', '123456');
    submit();
    await flush();
    expect(fetcher.mock.calls.slice(4).map(([path]) => path)).toEqual([
      `/api/iam/v1/login/transactions/${txId}/advance`,
      '/api/iam/v1/me',
      '/api/workspace/v1/me/instance/open',
    ]);
    expect(navigate).not.toHaveBeenCalled();
    fetcher.mockResolvedValueOnce(json({ ...ready, ready: false }));
    await vi.advanceTimersByTimeAsync(2000);
    expect(navigate).not.toHaveBeenCalled();
    fetcher.mockResolvedValueOnce(json(ready));
    await vi.advanceTimersByTimeAsync(2000);
    expect(navigate).toHaveBeenCalledExactlyOnceWith(
      '/open/instance%2Fone%3Fx',
    );
    for (const [, init] of fetcher.mock.calls) {
      expect(init?.credentials).toBe('same-origin');
      expect(init?.redirect).toBe('error');
      expect(new Headers(init?.headers).has('Authorization')).toBe(false);
    }
    const open = fetcher.mock.calls.find(([path]) =>
      String(path).endsWith('/instance/open'),
    )!;
    expect(open[1]?.method).toBe('POST');
    expect(open[1]?.body).toBeUndefined();
    expect(new Headers(open[1]?.headers).get('X-Auriga-CSRF')).toBe(
      'test-csrf',
    );
    expect(open[1]?.signal).toBeInstanceOf(globalThis.AbortSignal);
    expect(vi.getTimerCount()).toBe(0);
  });

  it('refreshes an authenticated portal session directly and allows pending_restart only when ready', async () => {
    fetcher.mockResolvedValueOnce(json(me)).mockResolvedValueOnce(
      json({
        instance: { ...ready, phase: 'pending_restart' },
        created: false,
      }),
    );
    render();
    await flush();
    expect(fetcher.mock.calls.map(([path]) => path)).toEqual([
      '/api/iam/v1/me',
      '/api/workspace/v1/me/instance/open',
    ]);
    expect(navigate).toHaveBeenCalledOnce();
  });

  it.each(['admin', 'device'])(
    'refuses a %s session instead of opening a personal instance',
    async (audience) => {
      fetcher.mockResolvedValueOnce(
        json({ ...me, session: { audience, current: true } }),
      );
      render();
      await flush();
      expect(fetcher).toHaveBeenCalledTimes(1);
      expect(navigate).not.toHaveBeenCalled();
      expect(root.textContent).toContain('暂时无法完成');
    },
  );

  it.each([{}, { items: [{}] }, '<html>sign in elsewhere</html>'])(
    'keeps malformed provider responses controlled: %#',
    async (value) => {
      fetcher
        .mockResolvedValueOnce(expired())
        .mockResolvedValueOnce(
          typeof value === 'string'
            ? new Response(value, { headers: { 'Content-Type': 'text/html' } })
            : json(value),
        );
      render();
      await flush();
      expect(fetcher).toHaveBeenCalledTimes(2);
      expect(root.textContent).toContain('暂时无法完成');
      expect(root.textContent).not.toContain('sign in elsewhere');
      expect(navigate).not.toHaveBeenCalled();
    },
  );

  it.each(['redirect', 'qr', 'webauthn', 'device', 'done'])(
    'keeps unsupported %s steps closed and offers the existing portal',
    async (type) => {
      await anonymous();
      fetcher.mockResolvedValueOnce(
        json(
          transaction({
            type,
            url: 'https://untrusted.example.com/',
          } as ClientStep),
        ),
      );
      button('团队账号').click();
      await flush();
      expect(root.textContent).toContain('此步骤尚未接入');
      expect(root.querySelector('form')).toBeNull();
      expect(
        root.querySelector<HTMLAnchorElement>('a')?.getAttribute('href'),
      ).toBe('/login');
      // The fallback is a real same-origin anchor, not the mocked DSH navigator
      // or a legacy router.replace target pretending to load a static HTML.
      const fallback = new URL(
        root.querySelector<HTMLAnchorElement>('a')!.href,
      );
      expect(fallback.origin).toBe(globalThis.location.origin);
      expect(fallback.search).toBe('');
      expect(fetcher).toHaveBeenCalledTimes(3);
      expect(navigate).not.toHaveBeenCalled();
    },
  );

  it('does not trust a complete step with pending status or a link transaction', async () => {
    globalThis.history.replaceState(
      null,
      '',
      `/instance-login.html?tx=${txId}`,
    );
    fetcher.mockResolvedValueOnce(
      json({ ...transaction(complete, 'completed'), purpose: 'link' }),
    );
    render();
    await flush();
    expect(fetcher).toHaveBeenCalledTimes(1);
    expect(navigate).not.toHaveBeenCalled();
    page!.dispose();
    fetcher.mockResolvedValueOnce(json(transaction(complete)));
    render();
    await flush();
    expect(fetcher).toHaveBeenCalledTimes(2);
    expect(navigate).not.toHaveBeenCalled();
  });

  it.each(['expired', 'cancelled', 'completed'])(
    'restores %s callback transactions with explicit state handling',
    async (status) => {
      globalThis.history.replaceState(
        null,
        '',
        `/instance-login.html?tx=${txId}&returnTo=https://untrusted.example.com/`,
      );
      fetcher.mockResolvedValueOnce(
        json(
          transaction(
            status === 'completed' ? complete : password,
            status as LoginTransaction['status'],
          ),
        ),
      );
      if (status === 'completed')
        fetcher
          .mockResolvedValueOnce(json(me))
          .mockResolvedValueOnce(json({ instance: ready }));
      render();
      await flush();
      expect(fetcher.mock.calls[0]![0]).toBe(
        `/api/iam/v1/login/transactions/${txId}`,
      );
      expect(navigate).toHaveBeenCalledTimes(status === 'completed' ? 1 : 0);
      if (status === 'completed')
        expect(navigate).toHaveBeenCalledWith('/open/instance%2Fone%3Fx');
    },
  );

  it('restores a pending form without probing a prior session or starting DSH', async () => {
    globalThis.history.replaceState(
      null,
      '',
      `/instance-login.html?tx=${txId}`,
    );
    fetcher.mockResolvedValueOnce(json(transaction(otp)));
    render();
    await flush();
    expect(root.querySelector('input[name="code"]')).not.toBeNull();
    expect(fetcher).toHaveBeenCalledTimes(1);
    expect(navigate).not.toHaveBeenCalled();
  });

  it('does not cancel a different-purpose transaction returned after pagehide', async () => {
    globalThis.history.replaceState(
      null,
      '',
      `/instance-login.html?tx=${txId}`,
    );
    const late = deferred<Response>();
    fetcher.mockReturnValueOnce(late.promise);
    render();
    globalThis.dispatchEvent(new Event('pagehide'));
    late.resolve(json({ ...transaction(password), purpose: 'link' }));
    await flush();
    expect(fetcher).toHaveBeenCalledTimes(1);
    expect(navigate).not.toHaveBeenCalled();
  });

  it.each(['?tx=bad/../id', `?tx=${txId}&tx=${txId}`])(
    'refuses invalid callback input %s before HTTP',
    async (search) => {
      globalThis.history.replaceState(
        null,
        '',
        `/instance-login.html${search}`,
      );
      render();
      await flush();
      expect(fetcher).not.toHaveBeenCalled();
      expect(root.textContent).toContain('已失效');
    },
  );

  it('does not treat completed credentials as a verified session when me returns 401', async () => {
    globalThis.history.replaceState(
      null,
      '',
      `/instance-login.html?tx=${txId}`,
    );
    fetcher
      .mockResolvedValueOnce(json(transaction(complete, 'completed')))
      .mockResolvedValueOnce(expired());
    render();
    await flush();
    expect(fetcher).toHaveBeenCalledTimes(2);
    expect(root.textContent).toContain('已失效');
    expect(navigate).not.toHaveBeenCalled();
  });

  it.each([403, 429, 503])(
    'keeps authenticated state after HTTP %i and retries preparation without credentials',
    async (status) => {
      fetcher.mockResolvedValueOnce(json(me)).mockResolvedValueOnce(
        json(
          {
            title: '<private detail>',
            detail: 'secret',
            status,
            code: 'common.unavailable',
          },
          status,
          { 'Retry-After': '3' },
        ),
      );
      render();
      await flush();
      expect(root.textContent).not.toContain('secret');
      expect(root.textContent).not.toContain('<private detail>');
      const retry = button('重试', '.actions button');
      if (status === 429) {
        retry.click();
        await flush();
        expect(fetcher).toHaveBeenCalledTimes(2);
        await vi.advanceTimersByTimeAsync(3000);
      }
      fetcher
        .mockResolvedValueOnce(json(me))
        .mockResolvedValueOnce(json({ instance: ready }));
      retry.click();
      retry.click();
      await flush();
      expect(
        fetcher.mock.calls.filter(([path]) =>
          String(path).includes('/login/transactions'),
        ),
      ).toHaveLength(0);
      expect(navigate).toHaveBeenCalledOnce();
    },
  );

  it.each([
    {
      ...ready,
      phase: 'failed',
      error: { code: 'workspace.failed', message: 'private' },
    },
    { ...ready, desired: 'stopped' },
    { ...ready, phase: 'deleting' },
  ])('does not enter an inconsistent ready instance %#', async (instance) => {
    fetcher
      .mockResolvedValueOnce(json(me))
      .mockResolvedValueOnce(json({ instance }));
    render();
    await flush();
    expect(navigate).not.toHaveBeenCalled();
    expect(root.textContent).not.toContain('private');
    expect(vi.getTimerCount()).toBe(0);
  });

  it('reports expiry during polling and never automatically repeats an open write', async () => {
    fetcher
      .mockResolvedValueOnce(json(me))
      .mockResolvedValueOnce(json({ instance: starting }))
      .mockResolvedValueOnce(expired());
    render();
    await flush();
    await vi.advanceTimersByTimeAsync(2000);
    expect(root.textContent).toContain('已失效');
    await vi.advanceTimersByTimeAsync(120_000);
    expect(fetcher).toHaveBeenCalledTimes(3);
    expect(navigate).not.toHaveBeenCalled();
  });

  it.each(['me', 'open', 'poll'])(
    'cancels late %s responses without starting another phase',
    async (phase) => {
      const late = deferred<Response>();
      if (phase === 'me') fetcher.mockReturnValueOnce(late.promise);
      else {
        fetcher.mockResolvedValueOnce(json(me));
        if (phase === 'open') fetcher.mockReturnValueOnce(late.promise);
        else
          fetcher
            .mockResolvedValueOnce(json({ instance: starting }))
            .mockReturnValueOnce(late.promise);
      }
      render();
      await flush();
      if (phase === 'poll') await vi.advanceTimersByTimeAsync(2000);
      button('取消', '.actions button').click();
      const count = fetcher.mock.calls.length;
      late.resolve(
        json(
          phase === 'me' ? me : phase === 'open' ? { instance: ready } : ready,
        ),
      );
      await flush();
      await vi.advanceTimersByTimeAsync(120_000);
      expect(fetcher).toHaveBeenCalledTimes(count);
      expect(navigate).not.toHaveBeenCalled();
      expect(root.textContent).toContain('已取消');
    },
  );

  it('times out a pending me, ignores its late success and permits explicit retry', async () => {
    const late = deferred<Response>();
    fetcher.mockReturnValueOnce(late.promise);
    render();
    await vi.advanceTimersByTimeAsync(120_000);
    expect(root.textContent).toContain('等待超时');
    late.resolve(json(me));
    await flush();
    expect(fetcher).toHaveBeenCalledTimes(1);
    fetcher
      .mockResolvedValueOnce(json(me))
      .mockResolvedValueOnce(json({ instance: ready }));
    button('重试', '.actions button').click();
    await flush();
    expect(navigate).toHaveBeenCalledOnce();
  });

  it('cancels the server transaction after a late create before enabling replacement sign-in', async () => {
    await anonymous();
    const late = deferred<Response>();
    fetcher
      .mockReturnValueOnce(late.promise)
      .mockResolvedValueOnce(new Response(null, { status: 204 }));
    button('团队账号').click();
    await flush();
    button('取消', '.actions button').click();
    expect(button('团队账号').disabled).toBe(true);
    late.resolve(json(transaction(password)));
    await flush();
    expect(fetcher.mock.calls[3]![0]).toBe(
      `/api/iam/v1/login/transactions/${txId}`,
    );
    expect(fetcher.mock.calls[3]![1]?.method).toBe('DELETE');
    expect(button('团队账号').disabled).toBe(false);
    expect(root.querySelector('form')).toBeNull();
    expect(navigate).not.toHaveBeenCalled();
  });

  it('cancels from the renderer during advance and discards late completed credentials', async () => {
    await showPassword();
    const late = deferred<Response>();
    fetcher.mockReturnValueOnce(late.promise);
    field('username', 'alice');
    field('password', 'test-password');
    submit();
    button('取消', '.card button').click();
    late.resolve(json(transaction(complete, 'completed')));
    await flush();
    expect(fetcher).toHaveBeenCalledTimes(4);
    expect(navigate).not.toHaveBeenCalled();
    expect(root.textContent).toContain('已取消');
  });

  it('cancels a current pending transaction before allowing a replacement provider', async () => {
    await showPassword();
    const deletion = deferred<Response>();
    fetcher.mockReturnValueOnce(deletion.promise);
    button('更换登录方式', '.actions button').click();
    expect(root.querySelector('form')).toBeNull();
    expect(button('团队账号').disabled).toBe(true);
    expect(fetcher.mock.calls[3]![1]?.method).toBe('DELETE');
    deletion.resolve(new Response(null, { status: 204 }));
    await flush();
    fetcher.mockResolvedValueOnce(json(transaction(password)));
    button('团队账号').click();
    await flush();
    expect(root.querySelector('input[name="password"]')).not.toBeNull();
    expect(fetcher).toHaveBeenCalledTimes(5);
  });

  it('restarts a retryable failed transaction through a new real IAM transaction', async () => {
    await anonymous();
    fetcher.mockResolvedValueOnce(
      json(
        transaction(
          { type: 'fail', code: 'iam.invalid_credentials', retryable: true },
          'failed',
        ),
      ),
    );
    button('团队账号').click();
    await flush();
    fetcher.mockResolvedValueOnce(json(transaction(password)));
    button('重新开始', '.card button').click();
    await flush();
    expect(fetcher.mock.calls[3]![0]).toBe('/api/iam/v1/login/transactions');
    expect(fetcher.mock.calls[3]![1]?.method).toBe('POST');
    expect(root.querySelector('input[name="password"]')).not.toBeNull();
    expect(navigate).not.toHaveBeenCalled();
  });

  it('does not create a transaction if cancelled before the prior cleanup continuation', async () => {
    await anonymous();
    button('团队账号').click();
    button('取消', '.actions button').click();
    await flush();
    expect(fetcher).toHaveBeenCalledTimes(2);
    expect(button('团队账号').disabled).toBe(false);
    expect(navigate).not.toHaveBeenCalled();
  });

  it('keeps a single lifecycle across language changes, repeated clicks and pagehide', async () => {
    const late = deferred<Response>();
    fetcher.mockResolvedValueOnce(json(me)).mockReturnValueOnce(late.promise);
    render();
    await flush();
    page!.setLocale('en');
    page!.setLocale('zh-CN');
    page!.setLocale('en');
    expect(root.textContent).toContain('Preparing my DSH');
    globalThis.dispatchEvent(new Event('pagehide'));
    late.resolve(json({ instance: ready }));
    await flush();
    expect(fetcher).toHaveBeenCalledTimes(2);
    expect(navigate).not.toHaveBeenCalled();
    expect(root.children).toHaveLength(0);
    expect(vi.getTimerCount()).toBe(0);
  });
});
