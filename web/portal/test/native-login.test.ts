import { setImmediate } from 'node:timers';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { mountInstanceLogin } from '../src/native-login/page';
import type { LoginTransaction } from '../../shared/src/api/iam';
import type { ClientStep } from '../../shared/src/generated/steps';
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
function enablePlacement() {
  document.head.insertAdjacentHTML(
    'beforeend',
    '<meta name="auriga-shared-placement-enabled" content="true">',
  );
}
const migration = () =>
  json({ status: 409, code: 'shared.legacy_migration_required' }, 409);
async function showMigration() {
  enablePlacement();
  fetcher
    .mockResolvedValueOnce(
      json({
        ...me,
        account: { ...me.account, displayName: 'Demo Member' },
        session: { ...me.session, id: 'private-session-marker' },
      }),
    )
    .mockResolvedValueOnce(migration());
  render();
  await flush();
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
  document
    .querySelectorAll('meta[name="auriga-login-preference"]')
    .forEach((node) => node.remove());
  page?.dispose();
  document
    .querySelector('meta[name="auriga-shared-placement-enabled"]')
    ?.remove();
  page = undefined;
  root.remove();
  vi.useRealTimers();
});

describe('native cloud sign-in', () => {
  it('uses the deployed team preference before UAC while retaining the company choice', async () => {
    document.head.insertAdjacentHTML(
      'beforeend',
      '<meta name="auriga-login-preference" content="team">',
    );
    const uac = {
      ...provider,
      id: txId,
      connectorType: 'uac',
      displayName: { en: 'Company', 'zh-CN': '公司' },
    };
    fetcher.mockImplementation(async (input, init) => {
      const path = String(input);
      if (path.endsWith('/me')) return expired();
      if (path.endsWith('/providers'))
        return json({
          items: [uac, { ...provider, connectorType: 'team-account' }],
        });
      if (path.endsWith('/transactions') && init?.method === 'POST')
        return json(transaction(password));
      if (init?.method === 'DELETE') return new Response(null, { status: 204 });
      throw new Error('Unexpected request');
    });
    render();
    await flush();
    expect(root.querySelector('input[name=username]')).not.toBeNull();
    expect(
      fetcher.mock.calls.some(
        ([path]) =>
          String(path).endsWith('/sso') || String(path).endsWith('/challenges'),
      ),
    ).toBe(false);
    button('更换登录方式').click();
    await flush();
    expect(button('公司').hidden).toBe(false);
  });
  it('defaults to scanning when browser sign-in evidence is unavailable and offers a team account', async () => {
    const uac = {
      ...provider,
      id: txId,
      connectorType: 'uac',
      primaryStep: 'qr',
    };
    const challenge = {
      id: txId,
      transactionId: txId,
      qrContent: 'synthetic-test-qr',
      expiresAt: '2099-01-01T00:00:00Z',
      pollIntervalSeconds: 2,
    };
    fetcher.mockImplementation(async (input) => {
      const path = String(input);
      if (path.endsWith('/me')) return expired();
      if (path.endsWith('/providers')) return json({ items: [provider, uac] });
      if (path.endsWith('/challenges')) return json(challenge);
      if (path.endsWith('/' + txId))
        return json({ ...challenge, state: 'waiting' });
      throw new Error('Unexpected request');
    });
    render();
    await flush();
    expect(
      root.querySelector('[data-uac-state]')?.getAttribute('data-uac-state'),
    ).toBe('waiting');
    expect(
      fetcher.mock.calls.some(([path]) => String(path).endsWith('/sso')),
    ).toBe(false);
    button('更换登录方式').click();
    await flush();
    expect(button('团队账号').hidden).toBe(false);
  });
  it('starts QR sign-in for an enabled UAC provider without a navigation proof', async () => {
    const id = '01900000-0000-7000-8000-000000000009';
    fetcher.mockImplementation(async (input, init) => {
      const path = String(input);
      if (path === '/api/iam/v1/me') return expired();
      if (path.endsWith('/providers'))
        return json({
          items: [
            provider,
            { ...provider, id, connectorType: 'uac', primaryStep: 'qr' },
          ],
        });
      if (path.endsWith('/challenges')) {
        expect(JSON.parse(String(init?.body)).providerId).toBe(id);
        return json({
          id,
          transactionId: txId,
          qrContent: 'synthetic-qr-content',
          expiresAt: '2099-01-01T00:00:00Z',
          pollIntervalSeconds: 2,
        });
      }
      if (path.endsWith('/challenges/' + id) && init?.method !== 'DELETE')
        return json({ id, state: 'waiting' });
      return new Response(null, { status: 204 });
    });
    render();
    await flush();
    expect(
      root.querySelector('[data-uac-state]')?.getAttribute('data-uac-state'),
    ).toBe('waiting');
    expect(root.querySelector('[data-uac-state] svg')).toBeTruthy();
    expect(fetcher.mock.calls.map(([path]) => path)).toEqual([
      '/api/iam/v1/me',
      '/api/iam/v1/login/providers',
      '/api/iam/v1/login/uac/challenges',
      '/api/iam/v1/login/uac/challenges/' + id,
    ]);
  });

  it.each([
    ['automatically after a wrong code', 'iam.invalid_credentials', false],
    ['from the retry button', 'common.not_found', false],
    ['after restoring the transaction by URL', 'iam.invalid_credentials', true],
  ])(
    'restarts UAC first MFA %s through a fresh QR challenge',
    async (_, code, restored) => {
      const id = '01900000-0000-7000-8000-000000000009';
      const first = '01900000-0000-7000-8000-000000000010';
      const second = '01900000-0000-7000-8000-000000000011';
      const replacementTx = '01900000-0000-7000-8000-000000000012';
      const uac = { ...provider, id, connectorType: 'uac', primaryStep: 'qr' };
      const challenges = '/api/iam/v1/login/uac/challenges';
      const transactions = '/api/iam/v1/login/transactions';
      const firstTotp: ClientStep = {
        type: 'form',
        formId: 'first_totp',
        fields: [{ name: 'otp', kind: 'otp', required: true }],
      };
      let created = 0;
      fetcher.mockImplementation(async (input, init) => {
        const path = String(input);
        if (path === '/api/iam/v1/me') return expired();
        if (path === '/api/iam/v1/login/providers')
          return json({ items: [uac] });
        if (path === challenges && init?.method === 'POST') {
          expect(JSON.parse(String(init.body))).toMatchObject({
            providerId: id,
            purpose: 'login',
          });
          created++;
          const initial = !restored && created === 1;
          return json({
            id: initial ? first : second,
            transactionId: initial ? txId : replacementTx,
            qrContent: 'synthetic-qr-' + created,
            expiresAt: '2099-01-01T00:00:00Z',
            pollIntervalSeconds: 2,
          });
        }
        if (path === challenges + '/' + first)
          return json({ id: first, state: 'verified' });
        if (path === challenges + '/' + first + '/complete')
          return json({
            state: 'additional_step_required',
            transactionId: txId,
          });
        if (path === transactions + '/' + txId)
          return json({ ...transaction(firstTotp), provider: uac });
        if (path === transactions + '/' + txId + '/advance') {
          expect(JSON.parse(String(init?.body))).toEqual({
            type: 'form',
            formId: 'first_totp',
            values: { otp: '000000' },
          });
          return json({
            ...transaction({ type: 'fail', code, retryable: true }, 'failed'),
            provider: uac,
          });
        }
        if (path === challenges + '/' + second)
          return init?.method === 'DELETE'
            ? new Response(null, { status: 204 })
            : json({ id: second, state: 'waiting' });
        throw new Error('Unexpected login request: ' + path);
      });
      if (restored)
        globalThis.history.replaceState(
          null,
          '',
          '/instance-login.html?tx=' + txId,
        );
      render();
      await flush();
      field('otp', '000000');
      submit();
      await flush();
      if (code !== 'iam.invalid_credentials') {
        expect(created).toBe(1);
        button('重新开始', '.card button').click();
        await flush();
      }
      expect(created).toBe(restored ? 1 : 2);
      expect(root.querySelector('input[name="otp"]')).toBeNull();
      expect(
        root.querySelector('[data-uac-state]')?.getAttribute('data-uac-state'),
      ).toBe('waiting');
      expect(root.querySelector('[data-uac-state] svg')).toBeTruthy();
      expect(fetcher.mock.calls.some(([path]) => path === transactions)).toBe(
        false,
      );
      expect(navigate).not.toHaveBeenCalled();
    },
  );

  it('offers the configured team sign-in when IAM does not offer UAC', async () => {
    await anonymous();
    expect(button('团队账号')).toBeDefined();
    expect(root.querySelector('[data-uac-state]')).toBeNull();
    expect(fetcher.mock.calls.map(([path]) => path)).toEqual([
      '/api/iam/v1/me',
      '/api/iam/v1/login/providers',
    ]);
  });

  it.each([
    ['ready', 'shared'],
    ['pending', 'shared'],
    ['ready-after-conflict', 'shared'],
    ['switching', 'dedicated'],
    ['ready', 'dedicated'],
  ])(
    'opens an authoritative %s placement in %s mode through its ticket',
    async (initialState, mode) => {
      enablePlacement();
      const pool = '01900000-0000-7000-8000-000000000012';
      const exchangeUrl = `/i/${pool}/_auriga/placement-ticket`;
      const keys: (string | null)[] = [];
      let reads = 0;
      fetcher.mockImplementation(async (input, init) => {
        const path = String(input);
        if (path.endsWith('/me')) return json(me);
        if (path.endsWith('/me/placement')) {
          reads++;
          return json({
            mode,
            state:
              initialState === 'ready-after-conflict' || reads > 1
                ? 'ready'
                : initialState,
          });
        }
        if (path.endsWith('/me/placement/open')) {
          expect(init?.method).toBe('POST');
          const key = new Headers(init?.headers).get('Idempotency-Key');
          expect(key).toMatch(/^[a-f0-9]{32}$/);
          keys.push(key);
          if (initialState !== 'ready' && keys.length === 1) {
            return json({ status: 409, code: 'common.conflict' }, 409);
          }
          return json({
            placement: { mode, state: 'ready' },
            exchangeUrl,
            ticket: 'test-placement-proof',
            expiresAt: Date.now() / 1000 + 60,
          });
        }
        if (path.endsWith(exchangeUrl)) {
          expect(init?.redirect).toBe('manual');
          expect(JSON.parse(String(init?.body))).toEqual({
            ticket: 'test-placement-proof',
          });
          const response = new Response();
          Object.defineProperty(response, 'type', { value: 'opaqueredirect' });
          return response;
        }
        throw new Error('Unexpected cloud request');
      });
      render();
      await flush();
      if (initialState === 'pending' || initialState === 'switching') {
        expect(root.textContent).toContain(
          initialState === 'pending' ? '正在等待 DSH 就绪' : '模式正在切换中',
        );
        expect(button('继续进入现有独享实例').hidden).toBe(true);
        expect(navigate).not.toHaveBeenCalled();
        await vi.advanceTimersByTimeAsync(2000);
        expect(reads).toBe(2);
      }
      expect(keys).toHaveLength(initialState === 'ready' ? 1 : 2);
      expect(new Set(keys).size).toBe(1);
      expect(navigate).toHaveBeenCalledOnce();
      expect(navigate).toHaveBeenCalledWith(
        new URL(`/i/${pool}/`, location.origin).href,
      );
      expect(
        fetcher.mock.calls.some(([path]) =>
          String(path).endsWith('/me/instance/open'),
        ),
      ).toBe(false);
    },
  );
  it.each([
    [() => json({ mode: 'shared', state: 'unavailable' }), '当前实例暂不可用'],
    [() => json({ mode: 'shared', state: 'blocked' }), '当前实例暂不可用'],
    [
      () => json({ status: 404, code: 'common.not_found' }, 404),
      '分配冲突或尚未就绪',
    ],
  ] as const)(
    'stops on a conflict without legacy fallback (%s)',
    async (read, message) => {
      enablePlacement();
      fetcher
        .mockResolvedValueOnce(json(me))
        .mockResolvedValueOnce(
          json({ status: 409, code: 'common.conflict' }, 409),
        )
        .mockResolvedValueOnce(read());
      render();
      await flush();
      expect(root.textContent).toContain(message);
      expect(button('继续进入现有独享实例').hidden).toBe(true);
      await vi.advanceTimersByTimeAsync(6000);
      expect(fetcher.mock.calls.map(([path]) => path)).toEqual([
        '/api/iam/v1/me',
        '/api/workspace/v1/me/placement/open',
        '/api/workspace/v1/me/placement',
      ]);
      expect(navigate).not.toHaveBeenCalled();
    },
  );
  it('stops after one ready retry when opening still conflicts', async () => {
    enablePlacement();
    fetcher.mockResolvedValueOnce(json(me));
    const states = ['ready', 'switching', 'ready'];
    fetcher.mockImplementation(async (path) =>
      String(path).endsWith('/placement/open')
        ? json({ status: 409, code: 'common.conflict' }, 409)
        : json({ mode: 'shared', state: states.shift() }),
    );
    render();
    await flush();
    await vi.advanceTimersByTimeAsync(121_000);
    expect(root.textContent).toContain('分配冲突或尚未就绪');
    expect(button('继续进入现有独享实例').hidden).toBe(true);
    expect(fetcher.mock.calls.map(([path]) => path)).toEqual([
      '/api/iam/v1/me',
      '/api/workspace/v1/me/placement/open',
      '/api/workspace/v1/me/placement',
      '/api/workspace/v1/me/placement/open',
      '/api/workspace/v1/me/placement',
      '/api/workspace/v1/me/placement',
    ]);
    expect(navigate).not.toHaveBeenCalled();
  });

  it('shows migration and account information, and retries only the placement', async () => {
    await showMigration();
    await vi.advanceTimersByTimeAsync(121_000);
    expect(root.textContent).toContain('管理台完成维护迁移');
    expect(root.textContent).toContain('Demo Member · owner');
    expect(root.textContent).not.toContain('private-session-marker');
    page!.setLocale('en');
    expect(root.textContent).toContain('admin console');
    expect(button('Continue to existing dedicated instance').hidden).toBe(
      false,
    );
    fetcher.mockResolvedValueOnce(json(me)).mockResolvedValueOnce(migration());
    button('Retry').click();
    await flush();
    const request = ['/api/iam/v1/me', '/api/workspace/v1/me/placement/open'];
    expect(fetcher.mock.calls.map(([path]) => path)).toEqual([
      ...request,
      ...request,
    ]);
    expect(navigate).not.toHaveBeenCalled();
  });

  it('enters a ready existing instance only after a click and observation', async () => {
    await showMigration();
    fetcher
      .mockResolvedValueOnce(json({ items: [starting] }))
      .mockResolvedValueOnce(json({ instance: starting, created: false }))
      .mockResolvedValueOnce(json(ready));
    const action = button('继续进入现有独享实例');
    action.click();
    action.click();
    await flush();
    expect(navigate).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(2000);
    expect(fetcher.mock.calls.map(([path]) => path)).toEqual([
      '/api/iam/v1/me',
      '/api/workspace/v1/me/placement/open',
      '/api/workspace/v1/me/instances',
      '/api/workspace/v1/me/instance/open',
      '/api/workspace/v1/instances/' + encodeURIComponent(starting.id),
    ]);
    expect(navigate).toHaveBeenCalledExactlyOnceWith(
      '/open/' + encodeURIComponent(ready.id),
    );
  });

  it.each([
    [[{ ...ready, phase: 'stopped' }], '已停止'],
    [[{ ...ready, phase: 'failed' }], '启动失败'],
    [[{ ...ready, desired: 'stopped' }], '已停止'],
    [[], '暂时无法进入'],
  ] as const)(
    'shows why a legacy instance cannot open (%s)',
    async (items, reason) => {
      await showMigration();
      fetcher.mockResolvedValueOnce(json({ items }));
      button('继续进入现有独享实例').click();
      await flush();
      await vi.advanceTimersByTimeAsync(121_000);
      expect(root.textContent).toContain(reason);
      expect(fetcher.mock.calls.map(([path]) => path)).toEqual([
        '/api/iam/v1/me',
        '/api/workspace/v1/me/placement/open',
        '/api/workspace/v1/me/instances',
      ]);
      expect(navigate).not.toHaveBeenCalled();
    },
  );

  it.each([
    [
      () => json({ status: 409, code: 'common.conflict' }, 409),
      '确认维护迁移进度',
    ],
    [
      () => json({ instance: { ...ready, phase: 'stopped' }, created: false }),
      '已停止',
    ],
    [() => json({ instance: ready, created: true }), '暂时无法进入'],
  ] as const)(
    'handles a legacy state change during manual open (%s)',
    async (open, reason) => {
      await showMigration();
      fetcher
        .mockResolvedValueOnce(json({ items: [ready] }))
        .mockResolvedValueOnce(open());
      button('继续进入现有独享实例').click();
      await flush();
      await vi.advanceTimersByTimeAsync(121_000);
      expect(root.textContent).toContain(reason);
      expect(fetcher).toHaveBeenCalledTimes(4);
      expect(navigate).not.toHaveBeenCalled();
    },
  );

  it('opens the workspace without waiting for department information and updates the identity area later', async () => {
    let reads = 0;
    fetcher.mockImplementation(async (input) => {
      const path = String(input);
      if (path === '/api/iam/v1/me') {
        reads++;
        return json({
          ...me,
          uacLoginInfo:
            reads === 1
              ? { purpose: 'login_info', status: 'pending' }
              : {
                  purpose: 'login_info',
                  status: 'available',
                  department: 'Synthetic department',
                  updatedAt: '2026-11-14T01:00:00Z',
                },
        });
      }
      if (path.endsWith('/open')) return json({ instance: starting });
      return json(starting);
    });
    render();
    await flush();
    expect(fetcher.mock.calls.map(([path]) => path)).toContain(
      '/api/workspace/v1/me/instance/open',
    );
    expect(
      root.querySelector('[data-testid=uac-department]')?.textContent,
    ).toBe('部门信息暂不可用');
    await vi.advanceTimersByTimeAsync(2000);
    expect(
      root.querySelector('[data-testid=uac-department]')?.textContent,
    ).toContain('所属部门：Synthetic department');
    expect(
      fetcher.mock.calls.filter(([, init]) => init?.method === 'POST'),
    ).toHaveLength(1);
  });
  it('continues entering a ready workspace when department information is unavailable', async () => {
    fetcher
      .mockResolvedValueOnce(
        json({
          ...me,
          uacLoginInfo: { purpose: 'login_info', status: 'unavailable' },
        }),
      )
      .mockResolvedValueOnce(json({ instance: ready }));
    render();
    await flush();
    expect(
      root.querySelector('[data-testid=uac-department]')?.textContent,
    ).toBe('部门信息暂不可用');
    expect(navigate).toHaveBeenCalledExactlyOnceWith(
      '/open/instance%2Fone%3Fx',
    );
    expect(fetcher).toHaveBeenCalledTimes(2);
  });
  it('offers a real logout anchor only after portal identity confirmation, including preparation failure', async () => {
    const late = deferred<Response>();
    fetcher
      .mockReturnValueOnce(late.promise)
      .mockResolvedValueOnce(
        json(
          { status: 503, title: 'Unavailable', code: 'common.unavailable' },
          503,
        ),
      );
    render();
    const logout = root.querySelector<HTMLAnchorElement>(
      'a[href="/logout.html"]',
    )!;
    expect(logout.hidden).toBe(true);
    late.resolve(json(me));
    await flush();
    expect(logout.hidden).toBe(false);
    expect(logout.textContent).toBe('退出登录');
    expect(logout.getAttribute('href')).toBe('/logout.html');
    page!.setLocale('en');
    expect(logout.textContent).toBe('Sign out');
    expect(fetcher.mock.calls.map(([path]) => path)).toEqual([
      '/api/iam/v1/me',
      '/api/workspace/v1/me/instance/open',
    ]);
  });

  it('does not offer logout for anonymous users or an unrelated admin session', async () => {
    await anonymous();
    expect(
      root.querySelector<HTMLAnchorElement>('a[href="/logout.html"]')!.hidden,
    ).toBe(true);
    page!.dispose();
    fetcher.mockResolvedValueOnce(
      json({ ...me, session: { audience: 'admin', current: true } }),
    );
    render();
    await flush();
    expect(
      root.querySelector<HTMLAnchorElement>('a[href="/logout.html"]')!.hidden,
    ).toBe(true);
    expect(fetcher).toHaveBeenCalledTimes(3);
  });

  it('hides logout after a confirmed session expires while waiting for an instance', async () => {
    fetcher
      .mockResolvedValueOnce(json(me))
      .mockResolvedValueOnce(json({ instance: starting }))
      .mockResolvedValueOnce(expired());
    render();
    await flush();
    const logout = root.querySelector<HTMLAnchorElement>(
      'a[href="/logout.html"]',
    )!;
    expect(logout.hidden).toBe(false);
    await vi.advanceTimersByTimeAsync(2000);
    expect(logout.hidden).toBe(true);
    expect(navigate).toHaveBeenCalledExactlyOnceWith('/instance-login.html');
  });

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
    expect(navigate).toHaveBeenCalledExactlyOnceWith('/instance-login.html');
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
