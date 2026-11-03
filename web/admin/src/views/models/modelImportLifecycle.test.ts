import { flushPromises, mount } from '@vue/test-utils';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { createAurigaI18n } from '@auriga/shared';
import ProvidersView from './ProvidersView.vue';
import { relayApi } from '../../admin/relayApi';
import { adminApi } from '../../admin/api';
vi.mock('../../admin/relayApi', () => ({
  relayApi: {
    providers: vi.fn(),
    pools: vi.fn(),
    providerModels: vi.fn(),
    createPool: vi.fn(),
  },
}));
vi.mock('../../admin/api', () => ({ adminApi: { membership: vi.fn() } }));
const permissions = ['ai.providers.read', 'ai.providers.write'];
const providers = ['a', 'b'].map((id) => ({
  id,
  configuration: {
    name: `Provider ${id}`,
    kind: 'openai' as const,
    baseUrl: `https://${id}.example.com`,
    enabled: true,
  },
  hasCredential: true,
}));
const catalog = (id: string) => ({
  items: [{ id: `${id}-model`, contextWindow: 64000, maxOutputTokens: 8192 }],
});
const make = () =>
  mount(ProvidersView, { global: { plugins: [createAurigaI18n('zh-CN')] } });
beforeEach(() => {
  vi.mocked(adminApi.membership).mockResolvedValue({ permissions } as Awaited<
    ReturnType<typeof adminApi.membership>
  >);
  vi.mocked(relayApi.providers).mockResolvedValue({ items: providers });
  vi.mocked(relayApi.pools).mockResolvedValue({ items: [] });
  vi.mocked(relayApi.providerModels).mockImplementation(async (id) =>
    catalog(id),
  );
});
afterEach(() => vi.resetAllMocks());
async function previewA(w: ReturnType<typeof make>) {
  await flushPromises();
  await w.findAll('[data-testid="import-provider"]')[0].trigger('click');
  await flushPromises();
  await w.get('input[value="a-model"]').setValue(true);
  await w.get('[data-testid="import-preview"]').trigger('click');
  await w.get('[name="importInputPrice"]').setValue('1');
  await w.get('[name="importOutputPrice"]').setValue('1');
  await w.get('[name="importConfirmed"]').setValue(true);
}
it.each([0, 1])(
  'starts a fresh provider import session when opening provider row %i after a confirmed preview',
  async (index) => {
    const w = make();
    await previewA(w);
    expect(
      (w.get('[name="importConfirmed"]').element as HTMLInputElement).checked,
    ).toBe(true);
    await w.findAll('[data-testid="import-provider"]')[index].trigger('click');
    await flushPromises();
    const id = index === 0 ? 'a' : 'b';
    expect(
      vi.mocked(relayApi.providerModels).mock.calls.map((call) => call[0]),
    ).toEqual(['a', id]);
    expect(
      (w.get('[name="importProvider"]').element as HTMLSelectElement).value,
    ).toBe(id);
    expect(w.find('[name="importConfirmed"]').exists()).toBe(false);
    expect(w.find(`[value="${id}-model"]`).exists()).toBe(true);
    expect(relayApi.createPool).not.toHaveBeenCalled();
    w.unmount();
  },
);
it('aborts the old provider catalog and discards its late response after a provider switch', async () => {
  let complete!: (
    value: Awaited<ReturnType<typeof relayApi.providerModels>>,
  ) => void;
  let signal!: AbortSignal;
  vi.mocked(relayApi.providerModels).mockImplementation((id, currentSignal) =>
    id === 'a'
      ? new Promise((resolve) => {
          complete = resolve;
          signal = currentSignal!;
        })
      : Promise.resolve(catalog(id)),
  );
  const w = make();
  await flushPromises();
  await w.findAll('[data-testid="import-provider"]')[0].trigger('click');
  await flushPromises();
  await w.findAll('[data-testid="import-provider"]')[1].trigger('click');
  await flushPromises();
  expect(signal.aborted).toBe(true);
  complete(catalog('a'));
  await flushPromises();
  expect(w.find('[value="a-model"]').exists()).toBe(false);
  expect(w.find('[value="b-model"]').exists()).toBe(true);
  w.unmount();
});
it('prevents switching provider rows while a batch is in flight and keeps its payload bound to the original provider', async () => {
  vi.mocked(relayApi.createPool).mockImplementation(
    (_body, _key, signal) =>
      new Promise((_resolve, reject) =>
        signal!.addEventListener('abort', () =>
          reject(new DOMException('Aborted', 'AbortError')),
        ),
      ),
  );
  const w = make();
  await previewA(w);
  await w.get('[data-testid="import-run"]').trigger('click');
  await flushPromises();
  expect(
    w.findAll('[data-testid="import-provider"]')[1].attributes('disabled'),
  ).toBeDefined();
  await w.findAll('[data-testid="import-provider"]')[1].trigger('click');
  await flushPromises();
  expect(
    vi.mocked(relayApi.providerModels).mock.calls.map((call) => call[0]),
  ).toEqual(['a']);
  expect(vi.mocked(relayApi.createPool).mock.calls[0][0].routes).toEqual([
    { providerId: 'a', model: 'a-model', weight: 1 },
  ]);
  await w.get('[data-testid="import-stop"]').trigger('click');
  await flushPromises();
  w.unmount();
});
