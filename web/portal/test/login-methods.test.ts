import { mount, flushPromises, type VueWrapper } from '@vue/test-utils';
import { afterEach, expect, it, vi } from 'vitest';
import { createMemoryHistory, createRouter } from 'vue-router';
import {
  createAurigaI18n,
  createApiClient,
  createIamApi,
  type LoginProvider,
} from '@auriga/shared';
import LoginView from '../src/views/LoginView.vue';
const provider = (id: string, connectorType: string): LoginProvider => ({
  id,
  connectorType,
  displayName: { 'zh-CN': id, en: id },
  order: 0,
  primaryStep: connectorType === 'uac' ? 'qr' : 'form',
});
const providers = [
  provider('team', 'team-account'),
  provider('emergency', 'emergency'),
  provider('scan', 'uac'),
];
const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
let view: VueWrapper | undefined;
afterEach(async () => {
  view?.unmount();
  view = undefined;
  await flushPromises();
  vi.restoreAllMocks();
});
async function setup(items = providers) {
  const calls: {
    path: string;
    method: string;
    body: Record<string, unknown>;
  }[] = [];
  let serial = 0;
  const txs = new Map<string, ReturnType<typeof transaction>>();
  function transaction(
    providerId: string,
    returnTo: string,
    id: string,
    otp = false,
  ) {
    return {
      id,
      purpose: 'login',
      status: 'pending',
      provider: items.find((p) => p.id === providerId),
      expiresAt: '2099-01-01T00:00:00Z',
      step: {
        type: 'form',
        formId: otp ? 'totp' : 'password',
        fields: otp
          ? [
              {
                name: 'code',
                kind: 'otp',
                required: true,
                label: { 'zh-CN': '验证码', en: 'Code' },
              },
            ]
          : [
              {
                name: 'username',
                kind: 'text',
                required: true,
                label: { 'zh-CN': '账号', en: 'Account' },
              },
              {
                name: 'password',
                kind: 'password',
                required: true,
                label: { 'zh-CN': '密码', en: 'Password' },
              },
            ],
      },
      returnTo,
    };
  }
  const transport = vi.fn<typeof fetch>(async (input, init) => {
    const path = String(input),
      method = init?.method ?? 'GET',
      body = init?.body ? JSON.parse(String(init.body)) : {};
    calls.push({ path, method, body });
    if (path.endsWith('/providers')) return json({ items });
    if (method === 'DELETE') return new Response(null, { status: 204 });
    if (path.endsWith('/challenges'))
      return json({
        id: 'challenge-' + ++serial,
        transactionId: 'unused',
        qrContent: 'public-qr-' + serial,
        expiresAt: '2099-01-01T00:00:00Z',
        pollIntervalSeconds: 30,
      });
    if (path.includes('/challenges/'))
      return json({ id: path.split('/').at(-1), state: 'waiting' });
    if (path.endsWith('/transactions')) {
      const tx = transaction(
        String(body.providerId),
        String(body.returnTo),
        'tx-' + ++serial,
      );
      txs.set(tx.id, tx);
      return json(tx);
    }
    if (path.endsWith('/advance')) {
      const id = path.split('/').at(-2)!;
      const tx = txs.get(id)!;
      if (body.formId === 'password') {
        const next = transaction(tx.provider!.id, tx.returnTo, id, true);
        txs.set(id, next);
        return json(next);
      }
      return json({
        ...tx,
        status: 'completed',
        step: { type: 'complete', redirectTo: tx.returnTo },
      });
    }
    throw new Error('Unexpected ' + path);
  });
  const api = createIamApi(createApiClient({ fetch: transport }));
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/login', component: LoginView },
      {
        path: '/account/security',
        component: { template: '<div>destination</div>' },
      },
    ],
  });
  await router.push(
    '/login?returnTo=%2Faccount%2Fsecurity&invitation=synthetic',
  );
  view = mount(LoginView, {
    props: { authApi: api },
    global: { plugins: [router, createAurigaI18n('zh-CN')] },
  });
  await flushPromises();
  return { calls, transport, router };
}
it('默认 UAC，无列表；当前图标重复点击不重复创建挑战', async () => {
  const { calls } = await setup();
  expect(view!.get('[data-uac-state]').attributes('data-uac-state')).toBe(
    'waiting',
  );
  expect(view!.find('[data-testid=provider-select]').exists()).toBe(false);
  expect(view!.find('form').exists()).toBe(false);
  expect(view!.get('[data-provider-id=scan]').attributes('aria-pressed')).toBe(
    'true',
  );
  await view!.get('[data-provider-id=scan]').trigger('click');
  await flushPromises();
  expect(calls.filter((c) => c.path.endsWith('/challenges'))).toHaveLength(1);
});
it.each(['scan', 'team', 'emergency'])(
  '单一方式 %s 自动进入且隐藏图标行',
  async (id) => {
    await setup(providers.filter((p) => p.id === id));
    expect(view!.find('[data-testid=login-methods]').exists()).toBe(false);
    expect(view!.find('form').exists()).toBe(id !== 'scan');
  },
);
it('切换先取消 UAC 和表单事务；回到 UAC 请求新二维码且不带旧输入', async () => {
  const { calls } = await setup();
  const first = calls.find((c) => c.path.endsWith('/challenges'))!;
  await view!.get('[data-provider-id=team]').trigger('click');
  await flushPromises();
  expect(
    calls.findIndex(
      (c) => c.method === 'DELETE' && c.path.includes('/challenges/'),
    ),
  ).toBeLessThan(calls.findIndex((c) => c.path.endsWith('/transactions')));
  expect(view!.find('form').exists()).toBe(true);
  await view!.get('input[name=username]').setValue('private-name');
  await view!.get('input[name=password]').setValue('private-password');
  await view!.get('[data-provider-id=emergency]').trigger('click');
  await flushPromises();
  expect(
    (view!.get('input[name=username]').element as HTMLInputElement).value,
  ).toBe('');
  expect(
    (view!.get('input[name=password]').element as HTMLInputElement).value,
  ).toBe('');
  await view!.get('[data-provider-id=scan]').trigger('click');
  await flushPromises();
  expect(calls.filter((c) => c.path.endsWith('/challenges'))).toHaveLength(2);
  expect(
    calls.filter(
      (c) => c.method === 'DELETE' && c.path.includes('/transactions/'),
    ),
  ).toHaveLength(2);
  expect(JSON.stringify(calls)).not.toContain('private-password');
  expect(first.body).toMatchObject({
    purpose: 'login',
    invitationCode: 'synthetic',
  });
});
it('取消挑战未完成前不创建新事务，避免方式竞态', async () => {
  const { transport, calls } = await setup();
  const previous = transport.getMockImplementation()!;
  let finish!: (response: Response) => void;
  transport.mockImplementation((input, init) =>
    init?.method === 'DELETE'
      ? new Promise((resolve) => {
          finish = resolve;
        })
      : previous(input, init),
  );
  await view!.get('[data-provider-id=team]').trigger('click');
  await flushPromises();
  expect(calls.some((c) => c.path.endsWith('/transactions'))).toBe(false);
  expect(
    view!.get('[data-provider-id=emergency]').attributes('disabled'),
  ).toBeDefined();
  finish(new Response(null, { status: 204 }));
  await flushPromises();
  expect(view!.find('form').exists()).toBe(true);
  transport.mockImplementation(previous);
});
it.each(['team', 'emergency'])(
  '%s 提交账号密码与 TOTP，保留 returnTo/邀请且不扫码',
  async (id) => {
    const { calls, router } = await setup();
    await view!.get(`[data-provider-id=${id}]`).trigger('click');
    await flushPromises();
    const qrCount = calls.filter((c) => c.path.endsWith('/challenges')).length;
    await view!.get('input[name=username]').setValue('synthetic');
    await view!.get('input[name=password]').setValue('synthetic-password');
    await view!.get('form').trigger('submit');
    await flushPromises();
    expect(view!.find('input[name=password]').exists()).toBe(false);
    await view!.get('input[name=code]').setValue('123456');
    await view!.get('form').trigger('submit');
    await flushPromises();
    expect(router.currentRoute.value.path).toBe('/account/security');
    expect(calls.filter((c) => c.path.endsWith('/challenges'))).toHaveLength(
      qrCount,
    );
    expect(
      calls.find((c) => c.path.endsWith('/transactions'))?.body,
    ).toMatchObject({
      providerId: id,
      purpose: 'login',
      returnTo: '/account/security',
      invitationCode: 'synthetic',
    });
    expect(
      calls.filter((c) => c.path.endsWith('/advance')).map((c) => c.body),
    ).toEqual([
      {
        type: 'form',
        formId: 'password',
        values: { username: 'synthetic', password: 'synthetic-password' },
      },
      { type: 'form', formId: 'totp', values: { code: '123456' } },
    ]);
  },
);
it('凭据错误只显示统一提示，不泄露上游账号存在性信息', async () => {
  const { transport } = await setup();
  await view!.get('[data-provider-id=team]').trigger('click');
  await flushPromises();
  const previous = transport.getMockImplementation()!;
  transport.mockImplementation((input, init) =>
    String(input).endsWith('/advance')
      ? Promise.resolve(
          json(
            {
              status: 401,
              code: 'iam.invalid_credentials',
              detail: 'ACCOUNT_EXISTS_SECRET',
              title: 'ACCOUNT_EXISTS_SECRET',
            },
            401,
          ),
        )
      : previous(input, init),
  );
  await view!.get('input[name=username]').setValue('synthetic');
  await view!.get('input[name=password]').setValue('wrong');
  await view!.get('form').trigger('submit');
  await flushPromises();
  expect(view!.get('[role=alert]').text()).toContain('用户名或口令不正确');
  expect(view!.text()).not.toContain('ACCOUNT_EXISTS_SECRET');
  expect(view!.find('input[name=code]').exists()).toBe(false);
});
