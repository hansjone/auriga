import { flushPromises, mount } from '@vue/test-utils';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { createAurigaI18n } from '@auriga/shared';
import ProvidersView from './ProvidersView.vue';
import { relayApi, type RelayPool } from '../../admin/relayApi';
import { adminApi } from '../../admin/api';
vi.mock('../../admin/relayApi', () => ({
  relayApi: {
    providers: vi.fn(),
    pools: vi.fn(),
    providerModels: vi.fn(),
    savePool: vi.fn(),
    createPool: vi.fn(),
  },
}));
vi.mock('../../admin/api', () => ({ adminApi: { membership: vi.fn() } }));
const providerId = '019a0000-0000-7000-8000-000000000001';
const provider = {
  id: providerId,
  configuration: {
    name: 'Provider',
    kind: 'openai' as const,
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
        '<button v-if="message" data-testid="confirm-action" @click="$emit(\'confirm\')">Confirm</button>',
    },
  },
};
const legacy = (): RelayPool => ({
  id: 'auto',
  enabled: true,
  contextWindow: 1000000,
  maxOutputTokens: 4096,
  routes: [
    { providerId, model: 'small', weight: 0 },
    { providerId, model: 'large', weight: 1 },
  ],
  price: {
    currency: 'USD',
    version: 'v1',
    effectiveAt: '2026-01-01T00:00:00Z',
    inputMicrosPerMillionTokens: 1,
    outputMicrosPerMillionTokens: 1,
  },
});
let persisted: RelayPool[];
beforeEach(() => {
  persisted = [legacy()];
  vi.mocked(adminApi.membership).mockResolvedValue({
    permissions: ['ai.providers.read', 'ai.providers.write'],
  } as Awaited<ReturnType<typeof adminApi.membership>>);
  vi.mocked(relayApi.providers).mockResolvedValue({ items: [provider] });
  vi.mocked(relayApi.pools).mockImplementation(async () => ({
    items: structuredClone(persisted),
  }));
  vi.mocked(relayApi.providerModels).mockResolvedValue({
    items: [
      { id: 'large', contextWindow: 2000000, maxOutputTokens: 128000 },
      { id: 'small', contextWindow: 32768, maxOutputTokens: 4096 },
    ],
  });
  vi.mocked(relayApi.savePool).mockImplementation(async (body) => {
    persisted = [structuredClone(body)];
    return body;
  });
});
afterEach(() => vi.resetAllMocks());
const value = (w: ReturnType<typeof mount>, name: string) =>
  (w.get(`[name="${name}"]`).element as HTMLInputElement).value;
it('blocks confirmation while a capability refresh is pending, then saves the visible refreshed snapshot', async () => {
  const pool = legacy();
  pool.contextWindow = 2000000;
  pool.maxOutputTokens = 128000;
  pool.capabilities = {
    sourceProviderId: providerId,
    sourceModel: 'large',
    contextWindowMode: 'inherit',
    maxOutputTokensMode: 'inherit',
    upstream: {
      providerId,
      model: { id: 'large', contextWindow: 2000000, maxOutputTokens: 128000 },
    },
  };
  persisted = [pool];
  let finish!: (
    value: Awaited<ReturnType<typeof relayApi.providerModels>>,
  ) => void;
  vi.mocked(relayApi.providerModels).mockImplementation(
    () =>
      new Promise((resolve) => {
        finish = resolve;
      }),
  );
  const w = mount(ProvidersView, { global });
  try {
    await flushPromises();
    await w.get('[data-testid="configure-auto"]').trigger('click');
    await w.get('[data-testid="apply-capabilities"]').trigger('click');
    await w.get('[data-testid="pool-form"]').trigger('submit');
    expect(w.find('[data-testid="confirm-action"]').exists()).toBe(false);
    expect(relayApi.savePool).not.toHaveBeenCalled();
    finish({
      items: [{ id: 'large', contextWindow: 4000000, maxOutputTokens: 256000 }],
    });
    await flushPromises();
    expect([value(w, 'contextWindow'), value(w, 'maxOutputTokens')]).toEqual([
      '4000000',
      '256000',
    ]);
    await w.get('[data-testid="pool-form"]').trigger('submit');
    await w.get('[data-testid="confirm-action"]').trigger('click');
    await flushPromises();
    expect(persisted[0]).toMatchObject({
      contextWindow: 4000000,
      maxOutputTokens: 256000,
      capabilities: {
        upstream: {
          model: { contextWindow: 4000000, maxOutputTokens: 256000 },
        },
      },
    });
  } finally {
    w.unmount();
    finish?.({ items: [] });
  }
});
it('does not start a refresh after save confirmation has frozen its visible values', async () => {
  const w = mount(ProvidersView, { global });
  await flushPromises();
  await w.get('[data-testid="configure-auto"]').trigger('click');
  await w.get('[data-testid="pool-form"]').trigger('submit');
  expect(w.find('[data-testid="confirm-action"]').exists()).toBe(true);
  await w.get('[data-testid="apply-capabilities"]').trigger('click');
  await flushPromises();
  expect(relayApi.providerModels).not.toHaveBeenCalled();
  await w.get('[data-testid="confirm-action"]').trigger('click');
  await flushPromises();
  expect([persisted[0].contextWindow, persisted[0].maxOutputTokens]).toEqual([
    1000000, 4096,
  ]);
  w.unmount();
});
it('ignores a refresh that arrives after closing and opening another pool', async () => {
  persisted.push({ ...legacy(), id: 'other', contextWindow: 750000 });
  let finish!: (
    value: Awaited<ReturnType<typeof relayApi.providerModels>>,
  ) => void;
  vi.mocked(relayApi.providerModels).mockImplementation(
    () =>
      new Promise((resolve) => {
        finish = resolve;
      }),
  );
  const w = mount(ProvidersView, { global });
  await flushPromises();
  await w.get('[data-testid="configure-auto"]').trigger('click');
  await w.get('[data-testid="apply-capabilities"]').trigger('click');
  await w
    .findAll('button')
    .find((button) => button.text() === '关闭编辑')!
    .trigger('click');
  await w.findAll('[data-testid="edit-pool"]')[1].trigger('click');
  finish({
    items: [{ id: 'large', contextWindow: 4000000, maxOutputTokens: 256000 }],
  });
  await flushPromises();
  expect(value(w, 'poolId')).toBe('other');
  expect(value(w, 'contextWindow')).toBe('750000');
  expect(w.text()).not.toContain('已刷新所选来源的上游快照');
  w.unmount();
});
it('keeps old explicit values, refreshes only inherited fields and persists them through save and reopen', async () => {
  const w = mount(ProvidersView, { global });
  await flushPromises();
  await w.get('[data-testid="configure-auto"]').trigger('click');
  expect(value(w, 'contextCapabilityMode')).toBe('manual');
  expect(value(w, 'contextWindow')).toBe('1000000');
  expect(value(w, 'capabilitySourceRoute')).toBe(
    JSON.stringify([providerId, 'large']),
  );
  await w.get('[data-testid="apply-capabilities"]').trigger('click');
  await flushPromises();
  expect(value(w, 'contextWindow')).toBe('1000000');
  expect(value(w, 'maxOutputTokens')).toBe('4096');
  await w.get('[name="contextCapabilityMode"]').setValue('inherit');
  await w.get('[name="outputCapabilityMode"]').setValue('inherit');
  expect([value(w, 'contextWindow'), value(w, 'maxOutputTokens')]).toEqual([
    '2000000',
    '128000',
  ]);
  await w.get('[name="recommendedContextWindow"]').setValue('900000');
  vi.mocked(relayApi.providerModels).mockResolvedValue({
    items: [{ id: 'large', maxOutputTokens: 256000 }],
  });
  await w.get('[data-testid="apply-capabilities"]').trigger('click');
  await flushPromises();
  expect([value(w, 'contextWindow'), value(w, 'maxOutputTokens')]).toEqual([
    '900000',
    '256000',
  ]);
  await w.get('[data-testid="pool-form"]').trigger('submit');
  await w.get('[data-testid="confirm-action"]').trigger('click');
  await flushPromises();
  expect(
    persisted[0].capabilities?.upstream?.model.contextWindow,
  ).toBeUndefined();
  expect(persisted[0].capabilities?.upstream?.model.maxOutputTokens).toBe(
    256000,
  );
  await w.get('[data-testid="configure-auto"]').trigger('click');
  expect([value(w, 'contextWindow'), value(w, 'maxOutputTokens')]).toEqual([
    '900000',
    '256000',
  ]);
  expect(w.text()).toContain('当前来源：管理员推荐值');
  w.unmount();
});
it('retains the previous snapshot on refresh failure and clears it when switching or removing its source', async () => {
  const pool = legacy();
  pool.capabilities = {
    sourceProviderId: providerId,
    sourceModel: 'large',
    contextWindowMode: 'inherit',
    maxOutputTokensMode: 'inherit',
    recommendedContextWindow: 750000,
    recommendedMaxOutputTokens: 64000,
    upstream: {
      providerId,
      model: { id: 'large', contextWindow: 2000000, maxOutputTokens: 128000 },
    },
  };
  pool.contextWindow = 2000000;
  pool.maxOutputTokens = 128000;
  persisted = [pool];
  const w = mount(ProvidersView, { global });
  await flushPromises();
  await w.get('[data-testid="configure-auto"]').trigger('click');
  vi.mocked(relayApi.providerModels).mockRejectedValue(
    new Error('synthetic network failure'),
  );
  await w.get('[data-testid="apply-capabilities"]').trigger('click');
  await flushPromises();
  expect(w.text()).toContain('刷新失败，已保留原上游快照');
  expect([value(w, 'contextWindow'), value(w, 'maxOutputTokens')]).toEqual([
    '2000000',
    '128000',
  ]);
  await w
    .get('[name="capabilitySourceRoute"]')
    .setValue(JSON.stringify([providerId, 'small']));
  expect([value(w, 'contextWindow'), value(w, 'maxOutputTokens')]).toEqual([
    '750000',
    '64000',
  ]);
  await w.get('[data-testid="pool-form"]').trigger('submit');
  await w.get('[data-testid="confirm-action"]').trigger('click');
  await flushPromises();
  expect(persisted[0].capabilities?.upstream).toBeUndefined();
  expect(persisted[0].capabilities?.sourceModel).toBe('small');
  await w.get('[data-testid="configure-auto"]').trigger('click');
  await w
    .findAll('button')
    .find((button) => button.text() === '移除路由')!
    .trigger('click');
  expect(value(w, 'capabilitySourceRoute')).toBe(
    JSON.stringify([providerId, 'large']),
  );
  expect([value(w, 'contextWindow'), value(w, 'maxOutputTokens')]).toEqual([
    '750000',
    '64000',
  ]);
  w.unmount();
});
it('new model selection inherits known values without taking a later fallback minimum', async () => {
  persisted = [];
  const w = mount(ProvidersView, { global });
  await flushPromises();
  await w.get('[data-testid="add-pool"]').trigger('click');
  const add = () =>
    w.findAll('button').find((button) => button.text() === '添加池路由')!;
  await add().trigger('click');
  await w.get('[name="routeProvider0"]').setValue(providerId);
  await w.get('[data-testid="fetch-provider-models-0"]').trigger('click');
  await flushPromises();
  await w.get('[name="routeModel0"]').setValue('large');
  expect(value(w, 'contextCapabilityMode')).toBe('inherit');
  expect([value(w, 'contextWindow'), value(w, 'maxOutputTokens')]).toEqual([
    '2000000',
    '128000',
  ]);
  await add().trigger('click');
  await w.get('[name="routeProvider1"]').setValue(providerId);
  await w.get('[name="routeModel1"]').setValue('small');
  await w.get('[name="routeWeight1"]').setValue('0');
  expect(value(w, 'contextWindow')).toBe('2000000');
  w.unmount();
});
