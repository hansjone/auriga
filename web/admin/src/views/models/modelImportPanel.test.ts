import { mount, flushPromises } from '@vue/test-utils';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { ApiError, createAurigaI18n } from '@auriga/shared';
import ModelImportPanel from './ModelImportPanel.vue';
import { relayApi, type RelayPool } from '../../admin/relayApi';
import { adminApi } from '../../admin/api';
vi.mock('../../admin/relayApi', () => ({
  relayApi: { providerModels: vi.fn(), pools: vi.fn(), createPool: vi.fn() },
}));
vi.mock('../../admin/api', () => ({ adminApi: { membership: vi.fn() } }));
const permissions = ['ai.providers.read', 'ai.providers.write'];
const provider = {
  id: 'p',
  configuration: {
    name: 'Provider',
    kind: 'openai' as const,
    baseUrl: 'https://api.example.com',
    enabled: true,
  },
  hasCredential: true,
};
const global = { plugins: [createAurigaI18n('zh-CN')] };
const make = () =>
  mount(ModelImportPanel, {
    global,
    props: { providers: [provider], pools: [], providerId: 'p', permissions },
  });
let saved: RelayPool[];
beforeEach(() => {
  saved = [];
  vi.mocked(adminApi.membership).mockResolvedValue({ permissions } as Awaited<
    ReturnType<typeof adminApi.membership>
  >);
  vi.mocked(relayApi.providerModels).mockResolvedValue({
    items: [
      { id: 'a', contextWindow: 64000, maxOutputTokens: 8192 },
      { id: 'b' },
    ],
  });
  vi.mocked(relayApi.pools).mockImplementation(async () => ({
    items: [...saved],
  }));
  vi.mocked(relayApi.createPool).mockImplementation(async (body) => {
    saved.push(JSON.parse(JSON.stringify(body)));
    return body;
  });
});
afterEach(() => vi.resetAllMocks());
it('shows retained recommendations when returning to selection and rebuilding the preview', async () => {
  const w = make();
  await flushPromises();
  await w.get('input[value="b"]').setValue(true);
  await w.get('[data-testid="import-preview"]').trigger('click');
  await w.get('[name="importRecommendedContext"]').setValue('1000000');
  await w.get('[name="importRecommendedOutput"]').setValue('64000');
  await w
    .findAll('button')
    .find((button) => button.text() === '返回选择')!
    .trigger('click');
  await w.get('[data-testid="import-preview"]').trigger('click');
  expect(
    (w.get('[name="importContext0"]').element as HTMLInputElement).value,
  ).toBe('1000000');
  expect(
    (w.get('[name="importOutput0"]').element as HTMLInputElement).value,
  ).toBe('64000');
  w.unmount();
});
it('validates a changed model against retained recommendations before freezing a body and allows correcting the row', async () => {
  vi.mocked(relayApi.providerModels).mockResolvedValue({
    items: [{ id: 'b' }, { id: 'small', contextWindow: 32768 }],
  });
  const w = make();
  await flushPromises();
  await w.get('input[value="b"]').setValue(true);
  await w.get('[data-testid="import-preview"]').trigger('click');
  await w.get('[name="importRecommendedContext"]').setValue('1000000');
  await w.get('[name="importRecommendedOutput"]').setValue('64000');
  await w
    .findAll('button')
    .find((button) => button.text() === '返回选择')!
    .trigger('click');
  await w.get('input[value="b"]').setValue(false);
  await w.get('input[value="small"]').setValue(true);
  await w.get('[data-testid="import-preview"]').trigger('click');
  await w.get('[name="importInputPrice"]').setValue('1');
  await w.get('[name="importOutputPrice"]').setValue('1');
  await w.get('[name="importConfirmed"]').setValue(true);
  expect(
    (w.get('[name="importOutput0"]').element as HTMLInputElement).value,
  ).toBe('64000');
  expect(
    (w.get('[data-testid="import-run"]').element as HTMLButtonElement).disabled,
  ).toBe(true);
  await w.get('[data-testid="import-run"]').trigger('click');
  await flushPromises();
  expect(relayApi.createPool).not.toHaveBeenCalled();
  expect(
    (w.get('[name="importOutput0"]').element as HTMLInputElement).disabled,
  ).toBe(false);
  await w.get('[name="importOutput0"]').setValue('4096');
  await w.get('[name="importConfirmed"]').setValue(true);
  await w.get('[data-testid="import-run"]').trigger('click');
  await flushPromises();
  expect(saved).toHaveLength(1);
  expect(saved[0]).toMatchObject({
    contextWindow: 32768,
    maxOutputTokens: 4096,
    capabilities: { maxOutputTokensMode: 'manual' },
  });
  w.unmount();
});
it('persists inherited metadata and configured recommendations while treating a typed row value as an explicit override', async () => {
  vi.mocked(relayApi.providerModels).mockResolvedValue({
    items: [
      { id: 'a', contextWindow: 2000000, maxOutputTokens: 128000 },
      { id: 'b' },
    ],
  });
  const w = make();
  await flushPromises();
  await w.get('input[value="a"]').setValue(true);
  await w.get('input[value="b"]').setValue(true);
  await w.get('[data-testid="import-preview"]').trigger('click');
  await w.get('[name="importRecommendedContext"]').setValue('1000000');
  await w.get('[name="importRecommendedOutput"]').setValue('64000');
  expect(
    (w.get('[name="importContext0"]').element as HTMLInputElement).value,
  ).toBe('2000000');
  expect(
    (w.get('[name="importContext1"]').element as HTMLInputElement).value,
  ).toBe('1000000');
  await w.get('[name="importContext0"]').setValue('750000');
  expect(
    (w.get('[name="importContextMode0"]').element as HTMLSelectElement).value,
  ).toBe('manual');
  await w.get('[name="importInputPrice"]').setValue('1');
  await w.get('[name="importOutputPrice"]').setValue('1');
  await w.get('[name="importConfirmed"]').setValue(true);
  await w.get('[data-testid="import-run"]').trigger('click');
  await flushPromises();
  expect(saved[0]).toMatchObject({
    contextWindow: 750000,
    maxOutputTokens: 128000,
    capabilities: {
      sourceModel: 'a',
      contextWindowMode: 'manual',
      maxOutputTokensMode: 'inherit',
      upstream: {
        model: { id: 'a', contextWindow: 2000000, maxOutputTokens: 128000 },
      },
    },
  });
  expect(saved[1]).toMatchObject({
    contextWindow: 1000000,
    maxOutputTokens: 64000,
    capabilities: {
      sourceModel: 'b',
      contextWindowMode: 'inherit',
      maxOutputTokensMode: 'inherit',
      recommendedContextWindow: 1000000,
      recommendedMaxOutputTokens: 64000,
      upstream: { model: { id: 'b' } },
    },
  });
  w.unmount();
});
it('clears every selected search result including a manually selected existing model', async () => {
  const w = make();
  await w.setProps({ pools: [{ id: 'a' } as RelayPool] });
  await flushPromises();
  await w.get('input[value="a"]').setValue(true);
  await w
    .findAll('button')
    .find((button) => button.text() === '取消搜索结果')!
    .trigger('click');
  expect((w.get('input[value="a"]').element as HTMLInputElement).checked).toBe(
    false,
  );
  w.unmount();
});
it('allows an already published upstream model to receive a new alias without overwriting its existing public ID', async () => {
  const w = make();
  await w.setProps({ pools: [{ id: 'a' } as RelayPool] });
  await flushPromises();
  expect(w.get('input[value="a"]').attributes('disabled')).toBeUndefined();
  await w.get('input[value="a"]').setValue(true);
  await w.get('[data-testid="import-preview"]').trigger('click');
  expect(w.text()).toContain('公开 ID 已存在');
  await w.get('[name="importAlias0"]').setValue('new-alias');
  await w.get('[name="importInputPrice"]').setValue('1');
  await w.get('[name="importOutputPrice"]').setValue('1');
  await w.get('[name="importConfirmed"]').setValue(true);
  await w.get('[data-testid="import-run"]').trigger('click');
  await flushPromises();
  expect(vi.mocked(relayApi.createPool).mock.calls[0]?.[0]).toMatchObject({
    id: 'new-alias',
    routes: [{ providerId: 'p', model: 'a', weight: 1 }],
  });
  w.unmount();
});
async function prepare(w: ReturnType<typeof make>) {
  await flushPromises();
  await w.get('input[type="checkbox"][value="a"]').setValue(true);
  await w.get('input[type="checkbox"][value="b"]').setValue(true);
  await w.get('[data-testid="import-preview"]').trigger('click');
  await w.get('[data-testid="import-use-suggested"]').trigger('click');
  await w.get('[name="importInputPrice"]').setValue('1000000');
  await w.get('[name="importOutputPrice"]').setValue('1000000');
  await w.get('[name="importConfirmed"]').setValue(true);
}
it('previews independent public aliases, inherits known capabilities and leaves unknown values uncapped with positive pricing', async () => {
  const w = make();
  await flushPromises();
  await w.get('input[value="a"]').setValue(true);
  await w.get('input[value="b"]').setValue(true);
  await w.get('[data-testid="import-preview"]').trigger('click');
  expect(
    (w.get('[name="importContext0"]').element as HTMLInputElement).value,
  ).toBe('64000');
  expect(
    (w.get('[name="importOutput0"]').element as HTMLInputElement).value,
  ).toBe('8192');
  expect(
    (w.get('[name="importContext1"]').element as HTMLInputElement).value,
  ).toBe('0');
  await w.get('[name="importConfirmed"]').setValue(true);
  expect(
    w.get('[data-testid="import-run"]').attributes('disabled'),
  ).toBeDefined();
  await w.get('[data-testid="import-use-suggested"]').trigger('click');
  await w.get('[name="importInputPrice"]').setValue('0');
  await w.get('[name="importOutputPrice"]').setValue('1');
  await w.get('[name="importConfirmed"]').setValue(true);
  expect(
    w.get('[data-testid="import-run"]').attributes('disabled'),
  ).toBeDefined();
  await w.get('[name="importInputPrice"]').setValue('1');
  await w.get('[name="importAlias0"]').setValue('public-a');
  await w.get('[name="importConfirmed"]').setValue(true);
  await w.get('[data-testid="import-run"]').trigger('click');
  await flushPromises();
  expect(saved.map((v) => v.id)).toEqual(['public-a', 'b']);
  expect(saved[0].routes).toEqual([{ providerId: 'p', model: 'a', weight: 1 }]);
  expect(saved[1].contextWindow).toBe(0);
  expect(saved[1].maxOutputTokens).toBe(0);
  expect(w.text()).toContain('已保存 2 / 2');
  w.unmount();
});
it('retries only failed items with the same key and never replays successful items', async () => {
  let attempts = 0;
  vi.mocked(relayApi.createPool).mockImplementation(async (body) => {
    if (body.id === 'b' && ++attempts === 1) throw Error('network');
    saved.push(JSON.parse(JSON.stringify(body)));
    return body;
  });
  const w = make();
  await prepare(w);
  await w.get('[data-testid="import-run"]').trigger('click');
  await flushPromises();
  expect(saved.map((v) => v.id)).toEqual(['a']);
  const key = vi.mocked(relayApi.createPool).mock.calls[1][1];
  await w.get('[data-testid="import-run"]').trigger('click');
  await flushPromises();
  expect(vi.mocked(relayApi.createPool).mock.calls.map((v) => v[0].id)).toEqual(
    ['a', 'b', 'b'],
  );
  expect(vi.mocked(relayApi.createPool).mock.calls[2][1]).toBe(key);
  w.unmount();
});
it('recovers a persisted but interrupted save by exact readback without another write', async () => {
  vi.mocked(relayApi.createPool).mockImplementation(async (body) => {
    saved.push(JSON.parse(JSON.stringify(body)));
    if (body.id === 'b') throw Error('connection lost');
    return body;
  });
  const w = make();
  await prepare(w);
  await w.get('[data-testid="import-run"]').trigger('click');
  await flushPromises();
  await w.get('[data-testid="import-run"]').trigger('click');
  await flushPromises();
  expect(relayApi.createPool).toHaveBeenCalledTimes(2);
  expect(w.text()).toContain('已保存 2 / 2');
  w.unmount();
});
it('never overwrites an existing or concurrently created public ID', async () => {
  vi.mocked(relayApi.createPool).mockRejectedValue(
    new ApiError({
      status: 412,
      code: 'common.precondition_failed',
      title: 'Precondition failed',
    }),
  );
  const w = make();
  await prepare(w);
  await w.get('[data-testid="import-run"]').trigger('click');
  await flushPromises();
  expect(w.text()).toContain('已跳过');
  expect(relayApi.createPool).toHaveBeenCalledTimes(2);
  expect(saved).toEqual([]);
  w.unmount();
});
it('allows correcting a preflight conflict to a new alias and preserves the original pool', async () => {
  const original: RelayPool = {
    id: 'a',
    enabled: true,
    contextWindow: 64000,
    maxOutputTokens: 8192,
    routes: [{ providerId: 'original', model: 'original-model', weight: 1 }],
    price: {
      currency: 'USD',
      inputMicrosPerMillionTokens: 1,
      outputMicrosPerMillionTokens: 1,
      version: 'original',
      effectiveAt: '2026-01-01T00:00:00Z',
    },
  };
  const before = structuredClone(original);
  vi.mocked(relayApi.pools).mockResolvedValue({ items: [original] });
  const w = make();
  await flushPromises();
  await w.get('input[value="a"]').setValue(true);
  await w.get('[data-testid="import-preview"]').trigger('click');
  await w.get('[name="importInputPrice"]').setValue('1');
  await w.get('[name="importOutputPrice"]').setValue('1');
  await w.get('[name="importConfirmed"]').setValue(true);
  await w.get('[data-testid="import-run"]').trigger('click');
  await flushPromises();
  expect(relayApi.createPool).not.toHaveBeenCalled();
  expect(w.text()).toContain('已跳过');
  await w.get('[name="importAlias0"]').setValue('new-public');
  await w.get('[name="importConfirmed"]').setValue(true);
  expect(
    w.get('[data-testid="import-run"]').attributes('disabled'),
  ).toBeUndefined();
  await w.get('[data-testid="import-run"]').trigger('click');
  await flushPromises();
  expect(vi.mocked(relayApi.createPool).mock.calls[0][0].id).toBe('new-public');
  expect(original).toEqual(before);
  expect(w.text()).toContain('已保存 1 / 1');
  w.unmount();
});
it.each([
  ['2026-01-01T00:00:00.12Z', true],
  ['2026-01-01T00:00:00.120000001Z', false],
] as const)(
  'handles interrupted-save readback timestamp %s without resending',
  async (effectiveAt, same) => {
    vi.useFakeTimers({ toFake: ['Date'] });
    vi.setSystemTime(new Date('2026-01-01T00:00:00.120Z'));
    try {
      vi.mocked(relayApi.createPool).mockImplementation(async (body) => {
        const persisted = JSON.parse(JSON.stringify(body)) as RelayPool;
        persisted.price.effectiveAt = effectiveAt;
        saved.push(persisted);
        if (body.id === 'b') throw Error('connection lost');
        return body;
      });
      const w = make();
      await prepare(w);
      await w.get('[data-testid="import-run"]').trigger('click');
      await flushPromises();
      await w.get('[data-testid="import-run"]').trigger('click');
      await flushPromises();
      expect(relayApi.createPool).toHaveBeenCalledTimes(2);
      expect(w.text()).toContain(same ? '已保存 2 / 2' : '已跳过');
      w.unmount();
    } finally {
      vi.useRealTimers();
    }
  },
);
it.each([
  ['iam.step_up_required', '请完成管理员身份验证后重试'],
  ['common.forbidden', '没有此操作所需权限'],
])(
  'shows the actionable response to %s and stops subsequent writes',
  async (code, message) => {
    vi.mocked(relayApi.createPool).mockRejectedValue(
      new ApiError({ status: 403, code, title: 'Forbidden' }),
    );
    const w = make();
    await prepare(w);
    await w.get('[data-testid="import-run"]').trigger('click');
    await flushPromises();
    expect(w.text()).toContain(message);
    expect(relayApi.createPool).toHaveBeenCalledTimes(1);
    w.unmount();
  },
);
it('stops subsequent writes when fresh permissions are revoked', async () => {
  vi.mocked(adminApi.membership)
    .mockResolvedValueOnce({ permissions } as Awaited<
      ReturnType<typeof adminApi.membership>
    >)
    .mockResolvedValueOnce({ permissions: [] } as unknown as Awaited<
      ReturnType<typeof adminApi.membership>
    >);
  const w = make();
  await prepare(w);
  await w.get('[data-testid="import-run"]').trigger('click');
  await flushPromises();
  expect(relayApi.createPool).toHaveBeenCalledTimes(1);
  expect(w.text()).toContain('没有此操作所需权限');
  w.unmount();
});
it('stop aborts an in-flight request and does not dispatch later models', async () => {
  vi.mocked(relayApi.createPool).mockImplementation(
    (_body, _key, signal) =>
      new Promise((_resolve, reject) =>
        signal!.addEventListener('abort', () =>
          reject(new DOMException('Aborted', 'AbortError')),
        ),
      ),
  );
  const w = make();
  await prepare(w);
  await w.get('[data-testid="import-run"]').trigger('click');
  await flushPromises();
  await w.get('[data-testid="import-stop"]').trigger('click');
  await flushPromises();
  expect(relayApi.createPool).toHaveBeenCalledTimes(1);
  expect(w.text()).toContain('未确认保存结果');
  w.unmount();
});
