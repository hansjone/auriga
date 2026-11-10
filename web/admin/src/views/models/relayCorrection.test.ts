import { flushPromises, mount, type VueWrapper } from '@vue/test-utils';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { ElDialog } from 'element-plus';
import { ApiError, createAurigaI18n } from '@auriga/shared';
import ProvidersView from './ProvidersView.vue';
import { openAuto } from './autoPoolTestHelper';
import KeysView from './KeysView.vue';
import { relayApi, type RelayPool } from '../../admin/relayApi';
import { adminApi } from '../../admin/api';
vi.mock('../../admin/relayApi', () => ({
  relayApi: {
    providers: vi.fn(),
    pools: vi.fn(),
    keys: vi.fn(),
    saveProvider: vi.fn(),
    credential: vi.fn(),
    savePool: vi.fn(),
    createPool: vi.fn(),
    createKey: vi.fn(),
    updateKey: vi.fn(),
  },
}));
vi.mock('../../admin/api', () => ({ adminApi: { membership: vi.fn() } }));
const permissions = [
  'ai.providers.read',
  'ai.providers.write',
  'ai.credentials.rotate',
];
const provider = {
  id: '019a0000-0000-7000-8000-000000000001',
  configuration: {
    name: 'Provider',
    kind: 'openai' as const,
    baseUrl: 'https://api.example.com',
    enabled: true,
  },
  hasCredential: true,
};
const pool: RelayPool = {
  id: 'auto',
  enabled: true,
  contextWindow: 64000,
  maxOutputTokens: 8192,
  routes: [{ providerId: provider.id, model: 'real', weight: 1 }],
  price: {
    currency: 'USD',
    version: 'v1',
    effectiveAt: '2026-01-01T00:00:00Z',
    inputMicrosPerMillionTokens: 1,
    outputMicrosPerMillionTokens: 1,
  },
};
const keyRow = {
  id: 'key',
  name: 'External',
  source: 'external' as const,
  accountId: '',
  active: true,
  monthlyBudgetMicros: 1,
  requestsPerMinute: 0,
  maxConcurrency: 0,
  maxInFlightTokens: 0,
  models: ['auto'],
};
const error = (status: number, code: string) =>
  new ApiError({ status, code, title: 'Synthetic failure' });
const global = {
  plugins: [createAurigaI18n('zh-CN')],
  stubs: {
    ActionConfirm: {
      props: ['message', 'busy'],
      emits: ['confirm', 'cancel'],
      template:
        '<div v-if="message"><button data-testid="confirm-action" @click="$emit(\'confirm\')">Confirm</button><button data-testid="cancel-action" @click="$emit(\'cancel\')">Cancel</button></div>',
    },
  },
};
type Kind =
  | 'provider-create'
  | 'provider-edit'
  | 'pool-create'
  | 'pool-edit'
  | 'credential'
  | 'key-create'
  | 'key-edit'
  | 'key-revoke';
type Call = { kind: Kind; body: unknown; key?: string };
let calls: Call[], commits: Call[], firstFailure: unknown, rejectFirst: boolean;
let wrappers: VueWrapper[];
const snapshot = <T>(value: T): T => JSON.parse(JSON.stringify(value));
// Same durable semantics as idempotency.go: claim fingerprint before mutation,
// replay both definite rejection and success; a changed body cannot reuse a key.
let claims: Map<
  string,
  { fingerprint: string; response: unknown; rejected: boolean }
>;
async function write<T>(
  kind: Kind,
  body: unknown,
  result: T,
  requestKey?: string,
): Promise<T> {
  const call = { kind, body: snapshot(body), key: requestKey };
  calls.push(call);
  const fingerprint = JSON.stringify([kind, body]);
  const claim = requestKey ? claims.get(requestKey) : undefined;
  if (claim) {
    if (claim.fingerprint !== fingerprint) throw error(409, 'common.conflict');
    if (claim.rejected) throw claim.response;
    return snapshot(claim.response) as T;
  }
  if (!calls.slice(0, -1).length && rejectFirst) {
    if (requestKey)
      claims.set(requestKey, {
        fingerprint,
        response: firstFailure,
        rejected: true,
      });
    throw firstFailure;
  }
  commits.push(call);
  if (requestKey)
    claims.set(requestKey, { fingerprint, response: result, rejected: false });
  if (calls.length === 1 && firstFailure) throw firstFailure;
  return snapshot(result);
}
beforeEach(() => {
  calls = [];
  commits = [];
  wrappers = [];
  claims = new Map();
  rejectFirst = true;
  firstFailure = error(400, 'common.validation_failed');
  vi.mocked(adminApi.membership).mockResolvedValue({ permissions } as Awaited<
    ReturnType<typeof adminApi.membership>
  >);
  vi.mocked(relayApi.providers).mockResolvedValue({ items: [provider] });
  vi.mocked(relayApi.pools).mockResolvedValue({ items: [pool] });
  vi.mocked(relayApi.keys).mockResolvedValue({ items: [keyRow] });
  vi.mocked(relayApi.saveProvider).mockImplementation((id, body, requestKey) =>
    write(
      id ? 'provider-edit' : 'provider-create',
      body,
      provider,
      id ? undefined : requestKey,
    ),
  );
  vi.mocked(relayApi.credential).mockImplementation((_id, secret, requestKey) =>
    write('credential', { secret }, provider, requestKey),
  );
  vi.mocked(relayApi.savePool).mockImplementation((body, requestKey) =>
    write('pool-edit', body, body, requestKey),
  );
  vi.mocked(relayApi.createPool).mockImplementation((body, requestKey) =>
    write('pool-create', body, body, requestKey),
  );
  vi.mocked(relayApi.createKey).mockImplementation((body, requestKey) =>
    write(
      'key-create',
      body,
      { secret: 'synthetic-issued-secret', key: keyRow },
      requestKey,
    ),
  );
  vi.mocked(relayApi.updateKey).mockImplementation((_id, body) =>
    write(body.active ? 'key-edit' : 'key-revoke', body, keyRow),
  );
});
afterEach(() => {
  wrappers.forEach((w) => w.unmount());
  vi.resetAllMocks();
});
async function open(kind: Kind) {
  const w = mount(kind.startsWith('key-') ? KeysView : ProvidersView, {
    global,
  });
  wrappers.push(w);
  await flushPromises();
  if (kind === 'provider-create' || kind === 'provider-edit') {
    await w
      .get(
        `[data-testid="${kind === 'provider-create' ? 'add-provider' : 'edit-provider'}"]`,
      )
      .trigger('click');
    await w.get('[name="providerName"]').setValue('Provider');
    await w.get('[name="providerUrl"]').setValue('https://api.example.com/v1');
  } else if (kind === 'credential') {
    await w
      .findAll('button')
      .find((b) => b.text() === '更新密钥')!
      .trigger('click');
    await w.get('[name="providerSecret"]').setValue('synthetic-first-secret');
  } else if (kind === 'pool-create') {
    // Auto create-only is distinct from ordinary saves.
    vi.mocked(relayApi.pools).mockResolvedValue({ items: [] });
    await w.get('header button').trigger('click');
    await flushPromises();
    await openAuto(w);
    await w
      .findAll('button')
      .find((b) => b.text() === '添加池路由')!
      .trigger('click');
    await w.get('[name="routeProvider0"]').setValue(provider.id);
    await w.get('[name="routeModel0"]').setValue('real');
    await w.get('[name="inputPrice"]').setValue('1');
    await w.get('[name="outputPrice"]').setValue('1');
  } else if (kind === 'pool-edit') {
    await openAuto(w);
  } else if (kind === 'key-create') {
    await w.get('[data-testid="new-key"]').trigger('click');
    await w.get('[name="keyName"]').setValue('External');
    await w.get('[name="allModels"]').setValue(true);
  } else
    await w
      .get(`[data-testid="${kind === 'key-edit' ? 'edit-key' : 'revoke-key'}"]`)
      .trigger('click');
  return w;
}
async function submit(w: VueWrapper, kind: Kind) {
  if (kind !== 'key-revoke')
    await w
      .get(
        kind.startsWith('key-')
          ? '[data-testid="key-form"]'
          : kind.startsWith('pool-')
            ? '[data-testid="pool-form"]'
            : kind === 'credential'
              ? 'form'
              : '[data-testid="save-provider"]',
      )
      .trigger('submit');
  await w.get('[data-testid="confirm-action"]').trigger('click');
  await flushPromises();
}
async function correct(w: VueWrapper, kind: Kind) {
  await w
    .get(
      kind.startsWith('provider-')
        ? '[name="providerUrl"]'
        : kind.startsWith('pool-')
          ? '[name="routeModel0"]'
          : kind === 'credential'
            ? '[name="providerSecret"]'
            : '[name="keyName"]',
    )
    .setValue(
      kind.startsWith('provider-')
        ? 'https://api.example.com'
        : kind === 'credential'
          ? 'synthetic-corrected-secret'
          : 'corrected',
    );
}
const forms: Kind[] = [
  'provider-create',
  'provider-edit',
  'pool-create',
  'pool-edit',
  'credential',
  'key-create',
  'key-edit',
];
it.each(forms)(
  '%s: definite rejection permits correction with a fresh operation instead of durable conflict',
  async (kind) => {
    const w = await open(kind);
    await submit(w, kind);
    expect(commits).toHaveLength(0);
    expect(w.text()).toContain('请检查填写内容');
    await correct(w, kind);
    await submit(w, kind);
    expect(commits).toHaveLength(1);
    expect(calls).toHaveLength(2);
    expect(calls[1].body).not.toEqual(calls[0].body);
    if (calls[0].key) expect(calls[1].key).not.toBe(calls[0].key);
    expect(w.text()).not.toContain('发生冲突');
  },
);
it.each(forms.filter((kind) => !['provider-edit', 'key-edit'].includes(kind)))(
  '%s: definite rejection can retry the same body without replaying the cached 400',
  async (kind) => {
    const w = await open(kind);
    await submit(w, kind);
    if (kind === 'credential')
      await w.get('[name="providerSecret"]').setValue('synthetic-first-secret');
    await submit(w, kind);
    expect(commits).toHaveLength(1);
    expect(calls[1].body).toEqual(calls[0].body);
    expect(calls[1].key).not.toBe(calls[0].key);
  },
);
it.each(forms)(
  '%s: unknown committed response blocks changed writes, then retries the original action and key',
  async (kind) => {
    rejectFirst = false;
    firstFailure = new TypeError('synthetic transport detail');
    const w = await open(kind);
    await submit(w, kind);
    expect(commits).toHaveLength(1);
    expect(w.get('[data-testid="relay-write-uncertain"]').text()).toContain(
      '结果尚未确认',
    );
    await correct(w, kind);
    await w.get('form').trigger('submit');
    await flushPromises();
    expect(calls).toHaveLength(1);
    await w.get('[data-testid="retry-relay-write"]').trigger('click');
    await w.get('[data-testid="confirm-action"]').trigger('click');
    await flushPromises();
    expect(calls).toHaveLength(2);
    expect(calls[1]).toEqual(calls[0]);
    expect(commits).toHaveLength(
      kind === 'provider-edit' || kind === 'key-edit' ? 2 : 1,
    );
    expect(w.text()).not.toContain('synthetic transport detail');
    if (kind === 'key-create')
      expect(w.find('[data-testid="issued-key"]').exists()).toBe(true);
  },
);
it.each([
  error(503, 'common.unavailable'),
  new DOMException('synthetic cancellation', 'AbortError'),
  error(409, 'ai.request_in_progress'),
  error(409, 'ai.replay_unavailable'),
])(
  'does not treat uncertain %s as definite rejection or rotate the original key',
  async (failure) => {
    rejectFirst = false;
    firstFailure = failure;
    const w = await open('provider-create');
    await submit(w, 'provider-create');
    expect(w.find('[data-testid="relay-write-uncertain"]').exists()).toBe(true);
    await w.get('[data-testid="retry-relay-write"]').trigger('click');
    await w.get('[data-testid="cancel-action"]').trigger('click');
    expect(calls).toHaveLength(1);
    await w.get('[data-testid="retry-relay-write"]').trigger('click');
    await w.get('[data-testid="confirm-action"]').trigger('click');
    await flushPromises();
    expect(calls[1]).toEqual(calls[0]);
    expect(commits).toHaveLength(1);
  },
);
it('preserves an unknown Auto action through readback and only retries its original body', async () => {
  rejectFirst = false;
  firstFailure = new TypeError('offline');
  const w = await open('pool-edit');
  await submit(w, 'pool-edit');
  await w.get('header button').trigger('click');
  await flushPromises();
  expect(w.find('[data-testid="pool-form"]').exists()).toBe(true);
  await w.get('[data-testid="retry-relay-write"]').trigger('click');
  await w.get('[data-testid="confirm-action"]').trigger('click');
  await flushPromises();
  expect(calls[1]).toEqual(calls[0]);
  expect(commits).toHaveLength(1);
});
it('clears an uncertain credential on permission loss and never submits a stale retry', async () => {
  rejectFirst = false;
  firstFailure = new TypeError('offline');
  const w = await open('credential');
  await submit(w, 'credential');
  vi.mocked(adminApi.membership).mockResolvedValue({
    permissions: [],
  } as unknown as Awaited<ReturnType<typeof adminApi.membership>>);
  await w.get('header button').trigger('click');
  await flushPromises();
  expect(w.find('[name="providerSecret"]').exists()).toBe(false);
  expect(w.find('[data-testid="retry-relay-write"]').exists()).toBe(false);
  expect(w.html()).not.toContain('synthetic-first-secret');
  expect(calls).toHaveLength(1);
});
it('retries the original action even when the unknown transport failure occurred before any mutation', async () => {
  firstFailure = undefined;
  rejectFirst = false;
  vi.mocked(relayApi.saveProvider).mockRejectedValueOnce(
    new TypeError('synthetic pre-dispatch failure'),
  );
  const w = await open('provider-create');
  await submit(w, 'provider-create');
  expect(commits).toHaveLength(0);
  const original = snapshot(
    vi.mocked(relayApi.saveProvider).mock.calls[0].slice(0, 3),
  );
  await w.get('[data-testid="retry-relay-write"]').trigger('click');
  await w.get('[data-testid="confirm-action"]').trigger('click');
  await flushPromises();
  expect(vi.mocked(relayApi.saveProvider).mock.calls[1].slice(0, 3)).toEqual(
    original,
  );
  expect(commits).toHaveLength(1);
});
it('does not resolve the original unknown outcome merely because a later retry is rejected', async () => {
  rejectFirst = false;
  firstFailure = new TypeError('original reply lost');
  const w = await open('provider-create');
  await submit(w, 'provider-create');
  vi.mocked(relayApi.saveProvider).mockRejectedValueOnce(
    error(400, 'common.validation_failed'),
  );
  await w.get('[data-testid="retry-relay-write"]').trigger('click');
  await w.get('[data-testid="confirm-action"]').trigger('click');
  await flushPromises();
  expect(w.find('[data-testid="relay-write-uncertain"]').exists()).toBe(true);
  await correct(w, 'provider-create');
  await w.get('form').trigger('submit');
  expect(relayApi.saveProvider).toHaveBeenCalledTimes(2);
  await w.get('[data-testid="retry-relay-write"]').trigger('click');
  await w.get('[data-testid="confirm-action"]').trigger('click');
  await flushPromises();
  const requests = vi.mocked(relayApi.saveProvider).mock.calls;
  expect(requests[2].slice(0, 3)).toEqual(requests[0].slice(0, 3));
  expect(commits).toHaveLength(1);
});
it('does not retry a successful key issue after its readback fails and preserves the one-time secret', async () => {
  firstFailure = undefined;
  rejectFirst = false;
  const w = await open('key-create');
  vi.mocked(relayApi.keys).mockRejectedValue(
    new Error('synthetic readback failure'),
  );
  await submit(w, 'key-create');
  expect(calls).toHaveLength(1);
  expect(w.find('[data-testid="retry-relay-write"]').exists()).toBe(false);
  expect(w.get('[data-testid="issued-key"]').element).toHaveProperty(
    'value',
    'synthetic-issued-secret',
  );
  expect(w.text()).toContain('不要重复提交');
});
it('allows correcting a cancelled confirmation before any request has been sent', async () => {
  firstFailure = undefined;
  rejectFirst = false;
  const w = await open('provider-create');
  await w.get('form').trigger('submit');
  await w.get('[data-testid="cancel-action"]').trigger('click');
  await correct(w, 'provider-create');
  await submit(w, 'provider-create');
  expect(calls).toHaveLength(1);
  expect(commits).toHaveLength(1);
});
it('rechecks current permissions before retrying an unknown save and clears the retry on denial', async () => {
  rejectFirst = false;
  firstFailure = new TypeError('offline');
  const w = await open('key-create');
  await submit(w, 'key-create');
  await w.get('[data-testid="retry-relay-write"]').trigger('click');
  vi.mocked(adminApi.membership).mockResolvedValue({
    permissions: ['ai.providers.read'],
  } as Awaited<ReturnType<typeof adminApi.membership>>);
  await w.get('[data-testid="confirm-action"]').trigger('click');
  await flushPromises();
  expect(calls).toHaveLength(1);
  expect(w.find('[data-testid="retry-relay-write"]').exists()).toBe(false);
  expect(w.find('[data-testid="issued-key"]').exists()).toBe(false);
});
it('never dispatches after unmount while the final permission read is pending', async () => {
  const w = await open('credential');
  let finish!: (value: Awaited<ReturnType<typeof adminApi.membership>>) => void;
  vi.mocked(adminApi.membership).mockImplementationOnce(
    () =>
      new Promise((resolve) => {
        finish = resolve;
      }),
  );
  await w.get('form').trigger('submit');
  await w.get('[data-testid="confirm-action"]').trigger('click');
  await flushPromises();
  w.unmount();
  wrappers = [];
  finish({ permissions } as Awaited<ReturnType<typeof adminApi.membership>>);
  await flushPromises();
  expect(relayApi.credential).not.toHaveBeenCalled();
  expect(w.html()).not.toContain('synthetic-first-secret');
});
it('keeps an uncertain revoke as the same PUT instead of starting a different key operation', async () => {
  rejectFirst = false;
  firstFailure = new TypeError('offline');
  const w = await open('key-revoke');
  await submit(w, 'key-revoke');
  await w.get('[data-testid="new-key"]').trigger('click');
  expect(w.find('[data-testid="key-form"]').exists()).toBe(false);
  await w.get('[data-testid="retry-relay-write"]').trigger('click');
  await w.get('[data-testid="confirm-action"]').trigger('click');
  await flushPromises();
  expect(calls).toHaveLength(2);
  expect(calls[1]).toEqual(calls[0]);
});
it.each(['pool-create', 'provider-create', 'key-create'] as const)(
  '%s: an unconfirmed write keeps its dialog open with the retry inside it',
  async (kind) => {
    rejectFirst = false;
    firstFailure = new TypeError('synthetic transport detail');
    const w = await open(kind);
    await submit(w, kind);
    const dialog = w.get('.admin-dialog');
    expect(dialog.find('[data-testid="relay-write-uncertain"]').exists()).toBe(
      true,
    );
    expect(w.findAll('[data-testid="retry-relay-write"]')).toHaveLength(1);
    // The header close button and Escape both end in update:modelValue=false.
    w.getComponent(ElDialog).vm.$emit('update:modelValue', false);
    await flushPromises();
    expect(w.find('.admin-dialog form').exists()).toBe(true);
    expect(w.find('[data-testid="relay-write-uncertain"]').exists()).toBe(true);
    await w.get('[data-testid="retry-relay-write"]').trigger('click');
    await w.get('[data-testid="confirm-action"]').trigger('click');
    await flushPromises();
    expect(calls[1]).toEqual(calls[0]);
  },
);
it('accepts micro-dollar budgets in the key form', async () => {
  const w = await open('key-create');
  const budget = w.get('[name="keyBudget"]');
  expect(budget.attributes('step')).toBe('0.000001');
  expect(Number(budget.attributes('max'))).toBeCloseTo(
    Number.MAX_SAFE_INTEGER / 1_000_000,
    6,
  );
});
