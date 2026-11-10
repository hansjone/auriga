import { mount, flushPromises } from '@vue/test-utils';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { createAurigaI18n } from '@auriga/shared';
import ProvidersView from './ProvidersView.vue';
import { openAuto } from './autoPoolTestHelper';
import KeysView from './KeysView.vue';
import UsageView from './UsageView.vue';
import { relayApi } from '../../admin/relayApi';
import { adminApi } from '../../admin/api';
import { orgApi } from '../../api';
vi.mock('../../admin/relayApi', () => ({
  relayApi: {
    providers: vi.fn(),
    providerModels: vi.fn(),
    pools: vi.fn(),
    keys: vi.fn(),
    saveProvider: vi.fn(),
    credential: vi.fn(),
    savePool: vi.fn(),
    createPool: vi.fn(),
    createKey: vi.fn(),
    updateKey: vi.fn(),
    usage: vi.fn(),
  },
}));
vi.mock('../../admin/api', () => ({ adminApi: { membership: vi.fn() } }));
vi.mock('../../api', () => ({ orgApi: { listMembers: vi.fn() } }));
const routeQuery: Record<string, string> = {};
vi.mock('vue-router', () => ({ useRoute: () => ({ query: routeQuery }) }));
const perms = [
  'ai.providers.read',
  'ai.providers.write',
  'ai.credentials.rotate',
  'ai.usage.admin',
];
const p = {
  id: '019a0000-0000-7000-8000-000000000001',
  configuration: {
    name: '主供应商',
    kind: 'minimax' as const,
    baseUrl: 'https://api.example.com',
    enabled: true,
  },
  hasCredential: true,
};
const global = {
  plugins: [createAurigaI18n('zh-CN')],
  stubs: {
    RouterLink: {
      props: ['to'],
      template: '<a :data-to="JSON.stringify(to)"><slot /></a>',
    },
    ActionConfirm: {
      props: ['message', 'busy'],
      emits: ['confirm', 'cancel'],
      template:
        '<div v-if="message"><p>{{message}}</p><button data-testid="confirm-action" @click="$emit(\'confirm\')">确认</button></div>',
    },
  },
};
beforeEach(() => {
  vi.mocked(adminApi.membership).mockResolvedValue({
    permissions: perms,
  } as Awaited<ReturnType<typeof adminApi.membership>>);
  vi.mocked(relayApi.providers).mockResolvedValue({ items: [p] });
  vi.mocked(relayApi.pools).mockResolvedValue({ items: [] });
  vi.mocked(relayApi.keys).mockResolvedValue({ items: [] });
});
afterEach(() => {
  vi.resetAllMocks();
  for (const key of Object.keys(routeQuery)) delete routeQuery[key];
});
it('rejects context one before confirmation while retaining zero as unknown', async () => {
  const auto = {
    id: 'auto',
    enabled: true,
    contextWindow: 1,
    maxOutputTokens: 0,
    routes: [{ providerId: p.id, model: 'real', weight: 1 }],
    price: {
      currency: 'USD' as const,
      inputMicrosPerMillionTokens: 1,
      outputMicrosPerMillionTokens: 1,
      version: 'v1',
      effectiveAt: '2026-01-01T00:00:00Z',
    },
  };
  vi.mocked(relayApi.pools).mockResolvedValue({ items: [auto] });
  const w = mount(ProvidersView, { global });
  await flushPromises();
  await openAuto(w);
  await w.get('[data-testid="pool-form"]').trigger('submit');
  expect(w.find('[data-testid="confirm-action"]').exists()).toBe(false);
  expect(relayApi.savePool).not.toHaveBeenCalled();
  await w.get('[name="contextWindow"]').setValue('0');
  await w.get('[data-testid="pool-form"]').trigger('submit');
  expect(w.find('[data-testid="confirm-action"]').exists()).toBe(true);
  await w.get('[data-testid="confirm-action"]').trigger('click');
  await flushPromises();
  expect(relayApi.savePool).toHaveBeenCalledWith(
    {
      ...auto,
      contextWindow: 0,
      capabilities: {
        sourceProviderId: p.id,
        sourceModel: 'real',
        contextWindowMode: 'manual',
        maxOutputTokensMode: 'manual',
      },
    },
    expect.any(String),
    expect.any(AbortSignal),
  );
  w.unmount();
});
it('makes member metering explicit while external keys start without traffic limits', async () => {
  const member = {
    id: 'member',
    name: 'member',
    source: 'member' as const,
    accountId: 'member',
    active: true,
    monthlyBudgetMicros: 0,
    requestsPerMinute: 0,
    models: ['auto'],
    maxConcurrency: 0,
    maxInFlightTokens: 0,
    meteringOnly: true,
    policyOrigin: 'default' as const,
  };
  vi.mocked(relayApi.keys).mockResolvedValue({ items: [member] });
  const w = mount(KeysView, { global });
  await flushPromises();
  await w
    .findAll('button')
    .find((v) => v.text() === '编辑')!
    .trigger('click');
  expect(
    (w.get('[name="memberMeteringOnly"]').element as HTMLInputElement).checked,
  ).toBe(true);
  expect(
    (w.get('[name="keyBudget"]').element as HTMLInputElement).disabled,
  ).toBe(true);
  expect(w.text()).toContain('下面的预算与流量数值保留但不生效');
  await w.get('[data-testid="key-form"]').trigger('submit');
  await w.get('[data-testid="confirm-action"]').trigger('click');
  await flushPromises();
  expect(vi.mocked(relayApi.updateKey).mock.calls[0][1].meteringOnly).toBe(
    true,
  );
  w.unmount();
});
it('saves different models from the same provider in auto and identifies an exact duplicate', async () => {
  const auto = {
    id: 'auto',
    enabled: true,
    contextWindow: 1000000,
    maxOutputTokens: 4096,
    routes: [
      { providerId: p.id, model: 'first', weight: 1 },
      { providerId: p.id, model: 'second', weight: 1 },
    ],
    price: {
      currency: 'USD' as const,
      inputMicrosPerMillionTokens: 1,
      outputMicrosPerMillionTokens: 1,
      version: 'v1',
      effectiveAt: '2026-01-01T00:00:00Z',
    },
  };
  vi.mocked(relayApi.pools).mockResolvedValue({ items: [auto] });
  const w = mount(ProvidersView, { global });
  await flushPromises();
  await openAuto(w);
  await w.get('[data-testid="pool-form"]').trigger('submit');
  expect(w.find('[data-testid="confirm-action"]').exists()).toBe(true);
  await w.get('[data-testid="confirm-action"]').trigger('click');
  await flushPromises();
  expect(relayApi.savePool).toHaveBeenCalledWith(
    {
      ...auto,
      capabilities: {
        sourceProviderId: p.id,
        sourceModel: 'first',
        contextWindowMode: 'manual',
        maxOutputTokensMode: 'manual',
      },
    },
    expect.any(String),
    expect.any(AbortSignal),
  );
  await openAuto(w);
  await w.get('[name="routeModel1"]').setValue('first');
  await w.get('[data-testid="pool-form"]').trigger('submit');
  expect(w.text()).toContain('第 2 条路由');
  expect(w.text()).toContain('供应商和模型重复');
  expect(w.find('[data-testid="confirm-action"]').exists()).toBe(false);
  w.unmount();
});
it('creates auto through the regular pool form without publishing a fake pool', async () => {
  const w = mount(ProvidersView, { global });
  await flushPromises();
  expect(w.find('[data-testid="configure-auto"]').exists()).toBe(false);
  expect(w.text()).toContain('ID 为 auto 的池即 DSH 默认模型');
  await openAuto(w);
  const id = w.get('[name="poolId"]').element as HTMLInputElement;
  expect(id.value).toBe('');
  expect(id.disabled).toBe(false);
  expect(relayApi.savePool).not.toHaveBeenCalled();
  w.unmount();
});

it('auto entry edits the current auto routes while retaining legacy pools', async () => {
  const auto = {
    id: 'auto',
    enabled: true,
    contextWindow: 32000,
    maxOutputTokens: 2000,
    routes: [{ providerId: p.id, model: 'actual-model', weight: 2 }],
    price: {
      currency: 'USD' as const,
      inputMicrosPerMillionTokens: 1,
      outputMicrosPerMillionTokens: 1,
      version: 'v1',
      effectiveAt: '2026-01-01T00:00:00Z',
    },
  };
  vi.mocked(relayApi.pools).mockResolvedValue({
    items: [auto, { ...auto, id: 'legacy' }],
  });
  const w = mount(ProvidersView, { global });
  await flushPromises();
  await openAuto(w);
  expect(w.text()).toContain('legacy');
  expect(
    (w.get('[name="contextWindow"]').element as HTMLInputElement).value,
  ).toBe('32000');
  expect(w.findAll('[data-testid="edit-pool"]')).toHaveLength(2);
  expect(relayApi.savePool).not.toHaveBeenCalled();
  await w.get('[data-testid="pool-form"]').trigger('submit');
  await w.get('[data-testid="confirm-action"]').trigger('click');
  await flushPromises();
  expect(relayApi.savePool).toHaveBeenCalledWith(
    {
      ...auto,
      capabilities: {
        sourceProviderId: p.id,
        sourceModel: 'actual-model',
        contextWindowMode: 'manual',
        maxOutputTokensMode: 'manual',
      },
    },
    expect.any(String),
    expect.any(AbortSignal),
  );
  expect(relayApi.createPool).not.toHaveBeenCalled();
  w.unmount();
});

it('keeps pool editing disabled until a successful current pool snapshot is available', async () => {
  let complete!: (value: Awaited<ReturnType<typeof relayApi.pools>>) => void;
  vi.mocked(relayApi.pools).mockReturnValue(
    new Promise((resolve) => {
      complete = resolve;
    }),
  );
  const w = mount(ProvidersView, { global });
  await flushPromises();
  const button = w.get('[data-testid="add-pool"]');
  expect((button.element as HTMLButtonElement).disabled).toBe(true);
  await button.trigger('click');
  expect(w.find('[name="poolId"]').exists()).toBe(false);
  complete({
    items: [
      {
        id: 'auto',
        enabled: true,
        contextWindow: 64000,
        maxOutputTokens: 8192,
        routes: [{ providerId: p.id, model: 'real-auto', weight: 1 }],
        price: {
          currency: 'USD',
          inputMicrosPerMillionTokens: 1,
          outputMicrosPerMillionTokens: 1,
          version: 'v1',
          effectiveAt: '2026-01-01T00:00:00Z',
        },
      },
    ],
  });
  await flushPromises();
  expect(w.find('[name="poolId"]').exists()).toBe(false);
  await openAuto(w);
  expect(
    (w.get('[name="contextWindow"]').element as HTMLInputElement).value,
  ).toBe('64000');
  expect(w.text()).toContain('real-auto');
  expect(relayApi.savePool).not.toHaveBeenCalled();
  w.unmount();
});

it('never treats a failed pool snapshot as proof that auto is absent', async () => {
  vi.mocked(relayApi.pools).mockRejectedValue(new Error('offline-fixture'));
  const w = mount(ProvidersView, { global });
  await flushPromises();
  const button = w.get('[data-testid="add-pool"]');
  expect((button.element as HTMLButtonElement).disabled).toBe(true);
  await button.trigger('click');
  expect(w.find('[name="poolId"]').exists()).toBe(false);
  expect(relayApi.savePool).not.toHaveBeenCalled();
  expect(relayApi.createPool).not.toHaveBeenCalled();
  w.unmount();
});

it('invalidates an Auto draft before reloading, and a failed reload never leaves a saveable stale draft', async () => {
  const w = mount(ProvidersView, { global });
  await flushPromises();
  await openAuto(w);
  await w.get('[name="poolId"]').setValue('auto');
  expect(w.find('[name="poolId"]').exists()).toBe(true);
  vi.mocked(relayApi.pools).mockRejectedValue(new Error('reload-fixture'));
  await w.findAll('header button')[0]!.trigger('click');
  await flushPromises();
  expect(w.find('[data-testid="pool-form"]').exists()).toBe(false);
  expect(
    (w.get('[data-testid="add-pool"]').element as HTMLButtonElement).disabled,
  ).toBe(true);
  expect(relayApi.savePool).not.toHaveBeenCalled();
  w.unmount();
});

it('creates a genuinely absent Auto through create-only protection and never falls back to an overwrite', async () => {
  vi.mocked(relayApi.createPool).mockRejectedValue({
    status: 409,
    problem: { code: 'common.conflict' },
  });
  const w = mount(ProvidersView, { global });
  await flushPromises();
  await openAuto(w);
  await w.get('[name="poolId"]').setValue('auto');
  await w
    .findAll('button')
    .find((button) => button.text() === '添加池路由')!
    .trigger('click');
  await w.get('[name="routeProvider0"]').setValue(p.id);
  await w.get('[name="routeModel0"]').setValue('real-auto');
  await w.get('[name="inputPrice"]').setValue('1');
  await w.get('[name="outputPrice"]').setValue('1');
  await w.get('[data-testid="pool-form"]').trigger('submit');
  await w.get('[data-testid="confirm-action"]').trigger('click');
  await flushPromises();
  expect(relayApi.createPool).toHaveBeenCalledWith(
    expect.objectContaining({
      id: 'auto',
      routes: [{ providerId: p.id, model: 'real-auto', weight: 1 }],
    }),
    expect.any(String),
    expect.any(AbortSignal),
  );
  expect(relayApi.savePool).not.toHaveBeenCalled();
  expect(w.find('[data-testid="pool-form"]').exists()).toBe(true);
  w.unmount();
});

it('restores provider editing with confirmation and fresh permission check', async () => {
  const w = mount(ProvidersView, { global });
  await flushPromises();
  await w.get('[data-testid="edit-provider"]').trigger('click');
  await w.get('[name="providerName"]').setValue('新名称');
  await w.get('[data-testid="save-provider"]').trigger('submit');
  expect(relayApi.saveProvider).not.toHaveBeenCalled();
  await w.get('[data-testid="confirm-action"]').trigger('click');
  await flushPromises();
  expect(relayApi.saveProvider).toHaveBeenCalledWith(
    p.id,
    expect.objectContaining({ name: '新名称' }),
    expect.any(String),
    expect.any(AbortSignal),
  );
  expect(adminApi.membership).toHaveBeenCalledTimes(3);
  w.unmount();
});
it('blocks writes when the current permission was revoked after opening the editor', async () => {
  const w = mount(ProvidersView, { global });
  await flushPromises();
  await w.get('[data-testid="edit-provider"]').trigger('click');
  await w.get('[data-testid="save-provider"]').trigger('submit');
  vi.mocked(adminApi.membership).mockResolvedValue({
    permissions: ['ai.providers.read'],
  } as Awaited<ReturnType<typeof adminApi.membership>>);
  await w.get('[data-testid="confirm-action"]').trigger('click');
  await flushPromises();
  expect(relayApi.saveProvider).not.toHaveBeenCalled();
  expect(w.text()).toContain('没有此操作所需权限');
  w.unmount();
});
it('shows an issued external key once and preserves it after failed readback', async () => {
  vi.mocked(relayApi.createKey).mockResolvedValue({
    secret: 'sk-bf-test-secret',
    key: {
      id: 'new',
      name: '对外应用',
      source: 'external',
      accountId: '',
      active: true,
      models: ['*'],
      monthlyBudgetMicros: 0,
      requestsPerMinute: 60,
    },
  });
  const w = mount(KeysView, { global });
  await flushPromises();
  await w.get('[data-testid="new-key"]').trigger('click');
  await w.get('[name="keyName"]').setValue('对外应用');
  await w.get('[name="keyConcurrency"]').setValue('8');
  await w.get('[name="keyInFlightTokens"]').setValue('33554432');
  await w.get('[name="allModels"]').setValue(true);
  await w.get('[data-testid="key-form"]').trigger('submit');
  vi.mocked(relayApi.keys).mockRejectedValue(
    new Error('secret backend detail'),
  );
  await w.get('[data-testid="confirm-action"]').trigger('click');
  await flushPromises();
  expect(
    w.get('[data-testid="issued-key"]').attributes('value') ??
      (w.get('[data-testid="issued-key"]').element as HTMLInputElement).value,
  ).toBe('sk-bf-test-secret');
  expect(w.text()).toContain('不要重复提交');
  expect(relayApi.createKey).toHaveBeenCalledWith(
    expect.objectContaining({ maxConcurrency: 8, maxInFlightTokens: 33554432 }),
    expect.any(String),
    expect.any(AbortSignal),
  );
  expect(w.text()).not.toContain('secret backend detail');
  await w.get('[data-testid="hide-key"]').trigger('click');
  expect(w.find('[data-testid="issued-key"]').exists()).toBe(false);
  w.unmount();
});
it('does not query usage until an explicit identity is selected and clears stale rows', async () => {
  const w = mount(UsageView, { global });
  await flushPromises();
  expect(relayApi.usage).not.toHaveBeenCalled();
  await w.get('[name="usageTarget"]').setValue('__manual');
  await w.get('[name="usageId"]').setValue(p.id);
  vi.mocked(relayApi.usage).mockResolvedValue({ items: [] });
  await w.get('form').trigger('submit');
  await flushPromises();
  expect(relayApi.usage).toHaveBeenCalledWith(
    'accountId',
    p.id,
    undefined,
    expect.any(AbortSignal),
  );
  await w.get('[name="usageKind"]').setValue('keyId');
  expect(w.find('[data-testid="usage-snapshot"]').exists()).toBe(false);
  w.unmount();
});
it('clears provider catalog and credential draft when read access is revoked', async () => {
  const w = mount(ProvidersView, { global });
  await flushPromises();
  const credentialButton = w
    .findAll('button')
    .find((button) => button.text().includes('密钥'));
  expect(credentialButton).toBeTruthy();
  await credentialButton!.trigger('click');
  await w
    .get('[name="providerSecret"]')
    .setValue(globalThis.crypto.randomUUID());
  vi.mocked(adminApi.membership).mockResolvedValue({
    permissions: [] as string[],
  } as Awaited<ReturnType<typeof adminApi.membership>>);
  await w.get('header button').trigger('click');
  await flushPromises();
  expect(w.find('[name="providerSecret"]').exists()).toBe(false);
  expect(w.text()).not.toContain('主供应商');
  expect(relayApi.credential).not.toHaveBeenCalled();
  w.unmount();
});
it('preserves member concurrency and in-flight limits when editing a budget', async () => {
  const member = {
    id: p.id,
    name: 'auriga-member:fixture',
    source: 'member' as const,
    accountId: p.id,
    active: true,
    models: ['*'],
    monthlyBudgetMicros: 100,
    requestsPerMinute: 60,
    maxConcurrency: 8,
    maxInFlightTokens: 33554432,
  };
  vi.mocked(relayApi.keys).mockResolvedValue({ items: [member] });
  vi.mocked(relayApi.updateKey).mockResolvedValue(member);
  const w = mount(KeysView, { global });
  await flushPromises();
  await w.get('[data-testid="edit-key"]').trigger('click');
  expect(w.get('[name="keyName"]').attributes('disabled')).toBeDefined();
  await w.get('[name="keyBudget"]').setValue('2.5');
  await w.get('[data-testid="key-form"]').trigger('submit');
  await w.get('[data-testid="confirm-action"]').trigger('click');
  await flushPromises();
  expect(relayApi.updateKey).toHaveBeenCalledWith(
    member.id,
    expect.objectContaining({
      monthlyBudgetMicros: 2_500_000,
      maxConcurrency: 8,
      maxInFlightTokens: 33554432,
    }),
    expect.any(AbortSignal),
  );
  w.unmount();
});

it('fetches searchable upstream IDs, retains manual input and saves a public alias', async () => {
  vi.mocked(relayApi.providerModels).mockResolvedValue({
    items: [{ id: 'gpt-real' }, { id: 'other-real' }],
  });
  const w = mount(ProvidersView, { global });
  await flushPromises();
  await w
    .findAll('button')
    .find((b) => b.text() === '添加模型池')!
    .trigger('click');
  await w
    .findAll('button')
    .find((b) => b.text() === '添加池路由')!
    .trigger('click');
  await w.get('[name="routeProvider0"]').setValue(p.id);
  await w.get('[data-testid="fetch-provider-models-0"]').trigger('click');
  await flushPromises();
  expect(relayApi.providerModels).toHaveBeenCalledWith(
    p.id,
    expect.any(AbortSignal),
  );
  expect(
    w.findAll('datalist option').map((v) => v.attributes('value')),
  ).toEqual(['gpt-real', 'other-real']);
  await w.get('[name="routeModel0"]').setValue('gpt-real');
  await w.get('[name="routeModel0"]').trigger('change');
  expect((w.get('[name="poolId"]').element as HTMLInputElement).value).toBe(
    'gpt-real',
  );
  await w.get('[name="poolId"]').setValue('deepseek-public');
  await w.get('[name="routeModel0"]').setValue('manually-entered-real');
  await w.get('[name="routeModel0"]').trigger('change');
  expect(w.get('[data-testid="model-mapping"]').text()).toBe(
    'manually-entered-real → deepseek-public',
  );
  await w.get('[name="inputPrice"]').setValue('1000000');
  await w.get('[name="outputPrice"]').setValue('1000000');
  await w.get('[data-testid="pool-form"]').trigger('submit');
  await w.get('[data-testid="confirm-action"]').trigger('click');
  await flushPromises();
  expect(relayApi.savePool).toHaveBeenCalledWith(
    expect.objectContaining({
      id: 'deepseek-public',
      routes: [{ providerId: p.id, model: 'manually-entered-real', weight: 1 }],
    }),
    expect.any(String),
    expect.any(AbortSignal),
  );
  w.unmount();
});
it('retains manual input on catalog failure and discards late results after close', async () => {
  let finish!: (value: { items: { id: string }[] }) => void;
  vi.mocked(relayApi.providerModels).mockImplementation(
    () =>
      new Promise((resolve) => {
        finish = resolve;
      }),
  );
  const w = mount(ProvidersView, { global });
  await flushPromises();
  await w
    .findAll('button')
    .find((b) => b.text() === '添加模型池')!
    .trigger('click');
  await w
    .findAll('button')
    .find((b) => b.text() === '添加池路由')!
    .trigger('click');
  await w.get('[name="routeProvider0"]').setValue(p.id);
  await w.get('[data-testid="fetch-provider-models-0"]').trigger('click');
  await w
    .findAll('button')
    .find((b) => b.text() === '关闭编辑')!
    .trigger('click');
  finish({ items: [{ id: 'late-model' }] });
  await flushPromises();
  expect(w.text()).not.toContain('late-model');
  vi.mocked(relayApi.providerModels).mockRejectedValue(new Error('upstream'));
  await w
    .findAll('button')
    .find((b) => b.text() === '添加模型池')!
    .trigger('click');
  await w
    .findAll('button')
    .find((b) => b.text() === '添加池路由')!
    .trigger('click');
  await w.get('[name="routeProvider0"]').setValue(p.id);
  await w.get('[data-testid="fetch-provider-models-0"]').trigger('click');
  await flushPromises();
  expect(w.text()).toContain('暂时无法获取供应商模型');
  await w.get('[name="routeModel0"]').setValue('manual-still-works');
  expect(
    (w.get('[name="routeModel0"]').element as HTMLInputElement).value,
  ).toBe('manual-still-works');
  w.unmount();
});

const publicPool = (id: string, enabled = true) => ({
  id,
  enabled,
  contextWindow: 64000,
  maxOutputTokens: 8192,
  routes: [{ providerId: p.id, model: 'real', weight: 1 }],
  price: {
    currency: 'USD' as const,
    inputMicrosPerMillionTokens: 1,
    outputMicrosPerMillionTokens: 1,
    version: 'v',
    effectiveAt: '2026-01-01T00:00:00Z',
  },
});
it('selects external key models with visible checkboxes and keeps current selection distinct from future wildcard access', async () => {
  vi.mocked(relayApi.pools).mockResolvedValue({
    items: [
      publicPool('alpha'),
      publicPool('beta'),
      publicPool('disabled', false),
    ],
  });
  vi.mocked(relayApi.createKey).mockResolvedValue({
    secret: 'test-fixture-key',
    key: {
      id: 'key',
      name: 'test',
      source: 'external',
      accountId: '',
      active: true,
      models: ['alpha', 'beta'],
      monthlyBudgetMicros: 100,
      requestsPerMinute: 60,
    },
  });
  const w = mount(KeysView, { global });
  await flushPromises();
  await w.get('[data-testid="new-key"]').trigger('click');
  expect(w.text()).toContain('预算为0，将禁止调用');
  await w.get('[name="keyModelSearch"]').setValue('alp');
  await w.get('[data-testid="key-select-visible"]').trigger('click');
  await w.get('[name="keyModelSearch"]').setValue('beta');
  await w.get('[name="keyModels"][value="beta"]').setValue(true);
  expect(w.find('input[value="disabled"]').exists()).toBe(false);
  await w.get('[name="keyName"]').setValue('test');
  await w.get('[name="keyBudget"]').setValue('100');
  await w.get('[data-testid="key-form"]').trigger('submit');
  await w.get('[data-testid="confirm-action"]').trigger('click');
  await flushPromises();
  expect(relayApi.createKey).toHaveBeenCalledWith(
    expect.objectContaining({ models: ['alpha', 'beta'] }),
    expect.any(String),
    expect.any(AbortSignal),
  );
  w.unmount();
});
it('preserves historical key grants until explicitly removed and clears wildcard when switching to explicit model selection', async () => {
  const key = {
    id: 'key',
    name: 'test',
    source: 'external' as const,
    accountId: '',
    active: true,
    models: ['old-model'],
    monthlyBudgetMicros: 100,
    requestsPerMinute: 60,
  };
  vi.mocked(relayApi.keys).mockResolvedValue({ items: [key] });
  vi.mocked(relayApi.pools).mockResolvedValue({ items: [publicPool('alpha')] });
  vi.mocked(relayApi.updateKey).mockResolvedValue(key);
  const w = mount(KeysView, { global });
  await flushPromises();
  await w.get('[data-testid="edit-key"]').trigger('click');
  expect(w.text()).toContain('历史授权');
  await w.get('[data-testid="key-form"]').trigger('submit');
  await w.get('[data-testid="confirm-action"]').trigger('click');
  await flushPromises();
  expect(relayApi.updateKey).toHaveBeenCalledWith(
    'key',
    expect.objectContaining({ models: ['old-model'] }),
    expect.any(AbortSignal),
  );
  w.unmount();
  vi.mocked(relayApi.keys).mockResolvedValue({
    items: [{ ...key, models: ['*'] }],
  });
  const wildcard = mount(KeysView, { global });
  await flushPromises();
  await wildcard.get('[data-testid="edit-key"]').trigger('click');
  await wildcard.get('[name="allModels"]').setValue(false);
  await wildcard.get('[name="keyModels"][value="alpha"]').setValue(true);
  await wildcard.get('[data-testid="key-form"]').trigger('submit');
  await wildcard.get('[data-testid="confirm-action"]').trigger('click');
  await flushPromises();
  expect(vi.mocked(relayApi.updateKey).mock.calls.at(-1)?.[1].models).toEqual([
    'alpha',
  ]);
  wildcard.unmount();
});
it('explicitly refreshes larger upstream capabilities and rejects zero prices before confirmation', async () => {
  vi.mocked(relayApi.providerModels).mockResolvedValue({
    items: [{ id: 'real', contextWindow: 128000, maxOutputTokens: 8192 }],
  });
  const w = mount(ProvidersView, { global });
  await flushPromises();
  await w.get('[data-testid="add-pool"]').trigger('click');
  await w
    .findAll('button')
    .find((v) => v.text() === '添加池路由')!
    .trigger('click');
  await w.get('[name="routeProvider0"]').setValue(p.id);
  await w.get('[name="routeModel0"]').setValue('real');
  await w.get('[data-testid="fetch-provider-models-0"]').trigger('click');
  await flushPromises();
  await w.get('[data-testid="apply-capabilities"]').trigger('click');
  expect(
    (w.get('[name="contextWindow"]').element as HTMLInputElement).value,
  ).toBe('128000');
  await w.get('[name="contextCapabilityMode"]').setValue('manual');
  await w.get('[name="outputCapabilityMode"]').setValue('manual');
  await w.get('[name="contextWindow"]').setValue('16000');
  await w.get('[name="maxOutputTokens"]').setValue('1024');
  await w.get('[data-testid="apply-capabilities"]').trigger('click');
  await flushPromises();
  expect(
    (w.get('[name="contextWindow"]').element as HTMLInputElement).value,
  ).toBe('16000');
  expect(
    (w.get('[name="maxOutputTokens"]').element as HTMLInputElement).value,
  ).toBe('1024');
  await w.get('[name="contextCapabilityMode"]').setValue('inherit');
  await w.get('[name="outputCapabilityMode"]').setValue('inherit');
  expect(
    (w.get('[name="contextWindow"]').element as HTMLInputElement).value,
  ).toBe('128000');
  expect(
    (w.get('[name="maxOutputTokens"]').element as HTMLInputElement).value,
  ).toBe('8192');
  await w.get('[data-testid="pool-form"]').trigger('submit');
  expect(w.find('[data-testid="confirm-action"]').exists()).toBe(false);
  expect(relayApi.savePool).not.toHaveBeenCalled();
  w.unmount();
});

it('offers relay keys by member name and queries the selected key', async () => {
  const member = {
    id: p.id,
    name: 'auriga-member:fixture',
    source: 'member' as const,
    accountId: p.id,
    active: true,
    models: ['*'],
    monthlyBudgetMicros: 0,
    requestsPerMinute: 0,
  };
  vi.mocked(relayApi.keys).mockResolvedValue({ items: [member] });
  vi.mocked(orgApi.listMembers).mockResolvedValue({
    items: [{ id: p.id, displayName: '张三' }],
  } as unknown as Awaited<ReturnType<typeof orgApi.listMembers>>);
  vi.mocked(relayApi.usage).mockResolvedValue({ items: [] });
  const w = mount(UsageView, { global });
  await flushPromises();
  expect(w.get('[name="usageTarget"]').text()).toContain('张三');
  expect(w.find('[name="usageId"]').exists()).toBe(false);
  await w.get('[name="usageTarget"]').setValue(p.id);
  await w.get('form').trigger('submit');
  await flushPromises();
  expect(relayApi.usage).toHaveBeenCalledWith(
    'keyId',
    p.id,
    undefined,
    expect.any(AbortSignal),
  );
  expect(w.get('[data-testid="usage-snapshot"]').text()).toContain('0');
  w.unmount();
});
it('queries usage immediately for a key linked from the key list', async () => {
  routeQuery.key = p.id;
  vi.mocked(relayApi.usage).mockResolvedValue({ items: [] });
  const w = mount(UsageView, { global });
  await flushPromises();
  expect(relayApi.usage).toHaveBeenCalledWith(
    'keyId',
    p.id,
    undefined,
    expect.any(AbortSignal),
  );
  w.unmount();
});
it('shows key budgets in dollars and links each key to its usage', async () => {
  const key = {
    id: 'key',
    name: 'partner',
    source: 'external' as const,
    accountId: '',
    active: true,
    models: ['*'],
    monthlyBudgetMicros: 12_500_000,
    requestsPerMinute: 60,
  };
  vi.mocked(relayApi.keys).mockResolvedValue({ items: [key] });
  const w = mount(KeysView, { global });
  await flushPromises();
  expect(w.text()).toContain('$12.50');
  expect(w.get('[data-testid="key-usage"]').attributes('data-to')).toContain(
    '"key":"key"',
  );
  await w.get('[data-testid="edit-key"]').trigger('click');
  expect((w.get('[name="keyBudget"]').element as HTMLInputElement).value).toBe(
    '12.5',
  );
  w.unmount();
});

it('marks the auto pool as the default model', async () => {
  vi.mocked(relayApi.pools).mockResolvedValue({
    items: [
      {
        id: 'auto',
        enabled: true,
        contextWindow: 0,
        maxOutputTokens: 0,
        routes: [{ providerId: p.id, model: 'real', weight: 1 }],
        price: {
          currency: 'USD',
          inputMicrosPerMillionTokens: 1,
          outputMicrosPerMillionTokens: 1,
          version: 'v1',
          effectiveAt: '2026-01-01T00:00:00Z',
        },
      },
    ],
  });
  const w = mount(ProvidersView, { global });
  await flushPromises();
  expect(w.get('tr[data-pool="auto"]').text()).toContain('默认');
  await openAuto(w);
  const id = w.get('[name="poolId"]').element as HTMLInputElement;
  expect(id.value).toBe('auto');
  expect(id.disabled).toBe(true);
  w.unmount();
});
