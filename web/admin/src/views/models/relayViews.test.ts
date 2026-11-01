import { mount, flushPromises } from '@vue/test-utils';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { createAurigaI18n } from '@auriga/shared';
import ProvidersView from './ProvidersView.vue';
import KeysView from './KeysView.vue';
import UsageView from './UsageView.vue';
import { relayApi } from '../../admin/relayApi';
import { adminApi } from '../../admin/api';
vi.mock('../../admin/relayApi', () => ({
  relayApi: {
    providers: vi.fn(),
    pools: vi.fn(),
    keys: vi.fn(),
    saveProvider: vi.fn(),
    credential: vi.fn(),
    savePool: vi.fn(),
    createKey: vi.fn(),
    updateKey: vi.fn(),
    usage: vi.fn(),
  },
}));
vi.mock('../../admin/api', () => ({ adminApi: { membership: vi.fn() } }));
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
afterEach(() => vi.resetAllMocks());
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
  await w.get('[name="keyBudget"]').setValue('200');
  await w.get('[data-testid="key-form"]').trigger('submit');
  await w.get('[data-testid="confirm-action"]').trigger('click');
  await flushPromises();
  expect(relayApi.updateKey).toHaveBeenCalledWith(
    member.id,
    expect.objectContaining({
      monthlyBudgetMicros: 200,
      maxConcurrency: 8,
      maxInFlightTokens: 33554432,
    }),
    expect.any(AbortSignal),
  );
  w.unmount();
});
