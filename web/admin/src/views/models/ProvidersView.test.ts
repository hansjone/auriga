import { mount, flushPromises } from '@vue/test-utils';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError, createAurigaI18n } from '@auriga/shared';
import ProvidersView from './ProvidersView.vue';
import { modelProvidersApi as api } from '../../admin/modelProvidersApi';
import {
  providerDraft,
  modelDraft,
  validModel,
  validProvider,
  parseGrants,
  modelPatch,
} from './forms';
const pid = '01900000-0000-7000-8000-000000000001';
vi.mock('../../admin/modelProvidersApi', () => ({
  modelProvidersApi: {
    providers: vi.fn(),
    models: vi.fn(),
    provider: vi.fn(),
    model: vi.fn(),
    credential: vi.fn(),
    readGrants: vi.fn(),
    createProvider: vi.fn(),
    patchProvider: vi.fn(),
    createCredential: vi.fn(),
    credentialAction: vi.fn(),
    createModel: vi.fn(),
    patchModel: vi.fn(),
    grants: vi.fn(),
  },
}));
const pass = {
  ...providerDraft(),
  id: pid,
  name: 'Example provider',
  baseUrl: 'https://api.example.com/v1',
  revision: 1,
  activeCredentialId: null,
  etag: '"provider-version"',
};
let wrapper: ReturnType<typeof mount>;
const button = (name: string) =>
  wrapper.findAll('button').find((b) => b.text() === name)!;
beforeEach(() => {
  vi.mocked(api.providers).mockResolvedValue({
    data: { items: [pass] },
    etag: null,
  });
  vi.mocked(api.credential).mockResolvedValue({
    data: {
      id: pid,
      state: 'active',
      revision: 1,
      createdAt: '2026-10-25T00:00:00Z',
    },
    etag: '"opaque"',
  });
  vi.mocked(api.readGrants).mockResolvedValue({
    data: { items: [] },
    etag: '"grant-version"',
  });
  vi.mocked(api.models).mockResolvedValue({ data: { items: [] }, etag: null });
  vi.mocked(api.provider).mockResolvedValue({
    data: pass,
    etag: '"provider-version"',
  });
});
afterEach(() => {
  wrapper?.unmount();
  vi.clearAllMocks();
});
const start = async () => {
  wrapper = mount(ProvidersView, { global: { plugins: [createAurigaI18n()] } });
  await flushPromises();
};
describe('ProvidersView', () => {
  it('renders provider groups and validates empty provider before calling API', async () => {
    await start();
    expect(wrapper.text()).toContain('Example provider');
    button('新建').trigger('click');
    await flushPromises();
    await wrapper.get('form').trigger('submit');
    await flushPromises();
    expect(wrapper.get('[role=alert]').text()).toContain('请检查');
    expect(api.createProvider).not.toHaveBeenCalled();
  });
  it('clears secret immediately and never echoes server secret detail', async () => {
    await start();
    await button('上游凭据').trigger('click');
    await wrapper.get('input[type=password]').setValue('ephemeral-fixture');
    vi.mocked(api.createCredential).mockRejectedValue(
      new Error('ephemeral-fixture'),
    );
    await wrapper.get('[data-testid=create-credential]').trigger('click');
    await flushPromises();
    expect(
      (wrapper.get('input[type=password]').element as HTMLInputElement).value,
    ).toBe('');
    expect(wrapper.html()).not.toContain('ephemeral-fixture');
    expect(wrapper.get('[role=alert]').text()).toContain('操作失败');
  });
  it('requires revoke confirmation and sends entered ETag and reason', async () => {
    await start();
    await button('上游凭据').trigger('click');
    await wrapper.get('[name=credentialId]').setValue(pid);
    await wrapper.get('[data-testid=read-credential]').trigger('click');
    await flushPromises();
    await wrapper.get('[name=reason]').setValue('rotation');
    await button('撤销').trigger('click');
    await flushPromises();
    expect(api.credentialAction).not.toHaveBeenCalled();
    expect(wrapper.text()).toContain('撤销会取消');
    vi.mocked(api.credentialAction).mockResolvedValue({
      data: {
        id: pid,
        state: 'revoked',
        revision: 2,
        createdAt: '2026-10-25T00:00:00Z',
      },
      etag: '"next"',
    });
    await wrapper.get('[data-testid=confirm]').trigger('click');
    await flushPromises();
    expect(api.credentialAction).toHaveBeenCalledWith(
      pid,
      pid,
      'revoke',
      { reason: 'rotation' },
      '"opaque"',
      expect.any(String),
      expect.any(AbortSignal),
    );
  });
  it('renders read errors and empty state on retry', async () => {
    vi.mocked(api.providers).mockRejectedValueOnce(new Error('private-detail'));
    await start();
    expect(wrapper.get('[role=alert]').text()).toContain('操作失败');
    expect(wrapper.text()).not.toContain('private-detail');
    vi.mocked(api.providers).mockResolvedValue({
      data: { items: [] },
      etag: null,
    });
    await button('刷新').trigger('click');
    await flushPromises();
    expect(wrapper.text()).toContain('暂无数据');
  });
  it('routes only models/providers to the new view', async () => {
    const { router } = await import('../../router');
    expect(
      router.resolve('/models/providers').matched.at(-1)?.components?.default,
    ).toBe(ProvidersView);
    expect(
      router.resolve('/models/auto').matched.at(-1)?.components?.default,
    ).not.toBe(ProvidersView);
  });
});
describe('model forms', () => {
  it('checks provider limits, model price/date/ID and grants replacement', () => {
    expect(validProvider(pass)).toBe(true);
    expect(validProvider({ ...pass, maxConcurrency: 0 })).toBe(false);
    const m = modelDraft(pid);
    expect(validModel(m)).toBe(false);
    Object.assign(m, { id: 'model', upstreamModel: 'upstream' });
    Object.assign(m.price, {
      version: 'v1',
      effectiveAt: '2026-10-25T00:00:00Z',
    });
    expect(validModel(m)).toBe(true);
    expect(parseGrants(`member ${pid}`)).toEqual({
      items: [{ kind: 'member', id: pid }],
    });
    expect(parseGrants('')).toEqual({ items: [] });
    expect(parseGrants(`member ${pid}\nmember ${pid}`)).toBeUndefined();
  });
});

it('saves provider edits with the ETag from the detail response', async () => {
  await start();
  await button('编辑').trigger('click');
  await flushPromises();
  await wrapper.get('[name=name]').setValue('Updated provider');
  vi.mocked(api.patchProvider).mockResolvedValue({
    data: { ...pass, name: 'Updated provider' },
    etag: '"new-provider"',
  });
  await wrapper.get('form').trigger('submit');
  await flushPromises();
  expect(api.patchProvider).toHaveBeenCalledWith(
    pid,
    expect.objectContaining({ name: 'Updated provider' }),
    '"provider-version"',
    expect.any(AbortSignal),
  );
  expect(wrapper.text()).toContain('已保存');
});

it('shows created credential metadata without rendering its secret', async () => {
  await start();
  await button('上游凭据').trigger('click');
  vi.mocked(api.createCredential).mockResolvedValue({
    data: {
      id: pid,
      state: 'staged',
      revision: 1,
      createdAt: '2026-10-25T00:00:00Z',
    },
    etag: '"credential-version"',
  });
  await wrapper.get('input[type=password]').setValue('new-secret-fixture');
  await wrapper.get('[data-testid=create-credential]').trigger('click');
  await flushPromises();
  expect(wrapper.text()).toContain('待激活');
  expect(wrapper.html()).not.toContain('new-secret-fixture');
  expect(wrapper.find('[name=credentialEtag]').exists()).toBe(false);
});

it('requires confirmation before replacing all model grants', async () => {
  const model = {
    ...modelDraft(pid),
    id: 'example-model',
    upstreamModel: 'upstream',
    revision: 1,
    etag: '"model-version"',
  };
  vi.mocked(api.models).mockResolvedValue({
    data: { items: [model] },
    etag: null,
  });
  await start();
  await button('授权').trigger('click');
  await flushPromises();
  expect(wrapper.find('[name=etag]').exists()).toBe(false);
  await wrapper.get('form').trigger('submit');
  await flushPromises();
  expect(api.grants).not.toHaveBeenCalled();
  expect(wrapper.text()).toContain('遗漏的授权将被收回');
  vi.mocked(api.grants).mockResolvedValue({
    data: { items: [] },
    etag: '"new-grant-version"',
  });
  await wrapper.get('[data-testid=confirm]').trigger('click');
  await flushPromises();
  expect(api.grants).toHaveBeenCalledWith(
    'example-model',
    { items: [] },
    '"grant-version"',
    expect.any(AbortSignal),
  );
});

it('loads all provider cursor pages without dropping the later provider', async () => {
  vi.mocked(api.providers)
    .mockResolvedValueOnce({
      data: { items: [pass], nextCursor: 'next-page' },
      etag: null,
    })
    .mockResolvedValueOnce({
      data: {
        items: [
          {
            ...pass,
            id: '01900000-0000-7000-8000-000000000002',
            name: 'Second provider',
          },
        ],
      },
      etag: null,
    });
  await start();
  expect(api.providers).toHaveBeenCalledWith(
    'next-page',
    expect.any(AbortSignal),
  );
  expect(wrapper.text()).toContain('Second provider');
});

it('edits a freshly read model without hand-entering ETag or resending price', async () => {
  const m = {
    ...modelDraft(pid),
    id: 'example-model',
    upstreamModel: 'upstream',
    revision: 1,
    etag: '"list-version"',
  };
  m.price.version = 'v1';
  m.price.effectiveAt = '2026-10-25T00:00:00Z';
  vi.mocked(api.models).mockResolvedValue({ data: { items: [m] }, etag: null });
  vi.mocked(api.model).mockResolvedValue({ data: m, etag: '"fresh-version"' });
  vi.mocked(api.patchModel).mockResolvedValue({
    data: m,
    etag: '"saved-version"',
  });
  await start();
  const edits = wrapper.findAll('button').filter((b) => b.text() === '编辑');
  await edits[1]!.trigger('click');
  await flushPromises();
  expect(wrapper.find('[name=etag]').exists()).toBe(false);
  await wrapper.get('[name=displayName]').setValue('Changed');
  await wrapper.get('form').trigger('submit');
  await flushPromises();
  expect(api.patchModel).toHaveBeenCalledWith(
    m.id,
    { displayName: 'Changed' },
    '"fresh-version"',
    expect.any(AbortSignal),
  );
});

it('includes price only when changed', () => {
  const before = modelDraft(pid);
  const after = { ...before, price: { ...before.price, version: 'v2' } };
  expect(modelPatch(before, before)).toEqual({});
  expect(modelPatch(before, after)).toEqual({ price: after.price });
});

it('reloads a model after 412 and saves a reviewed edit with the new ETag', async () => {
  const m = {
    ...modelDraft(pid),
    id: 'example-model',
    upstreamModel: 'upstream',
    revision: 1,
    etag: '"v1"',
  };
  m.price.version = 'v1';
  m.price.effectiveAt = '2026-10-25T00:00:00Z';
  vi.mocked(api.models).mockResolvedValue({ data: { items: [m] }, etag: null });
  vi.mocked(api.model)
    .mockResolvedValueOnce({ data: m, etag: '"v1"' })
    .mockResolvedValue({
      data: { ...m, displayName: 'Other edit', revision: 2, etag: '"v2"' },
      etag: '"v2"',
    });
  vi.mocked(api.patchModel)
    .mockRejectedValueOnce(
      new ApiError({
        status: 412,
        code: 'common.precondition_failed',
        title: 'Conflict',
      }),
    )
    .mockResolvedValue({ data: m, etag: '"v3"' });
  await start();
  await wrapper
    .findAll('button')
    .filter((b) => b.text() === '编辑')[1]!
    .trigger('click');
  await flushPromises();
  await wrapper.get('[name=displayName]').setValue('My edit');
  await wrapper.get('form').trigger('submit');
  await flushPromises();
  expect(api.model).toHaveBeenCalledTimes(2);
  expect(api.patchModel).toHaveBeenCalledTimes(1);
  expect(wrapper.get('[role=alert]').text()).toContain('已被他人修改');
  expect(
    (wrapper.get('[name=displayName]').element as HTMLInputElement).value,
  ).toBe('Other edit');
  await wrapper.get('[name=displayName]').setValue('Reviewed edit');
  await wrapper.get('form').trigger('submit');
  await flushPromises();
  expect(api.patchModel).toHaveBeenLastCalledWith(
    m.id,
    { displayName: 'Reviewed edit' },
    '"v2"',
    expect.any(AbortSignal),
  );
});

it('loads existing grants and rereads them on a conflict without replaying the write', async () => {
  const m = {
    ...modelDraft(pid),
    id: 'example-model',
    upstreamModel: 'upstream',
    revision: 1,
    etag: '"v1"',
  };
  vi.mocked(api.models).mockResolvedValue({ data: { items: [m] }, etag: null });
  vi.mocked(api.readGrants)
    .mockResolvedValueOnce({
      data: { items: [{ kind: 'member', id: pid }] },
      etag: '"g1"',
    })
    .mockResolvedValue({
      data: { items: [{ kind: 'group', id: pid }] },
      etag: '"g2"',
    });
  vi.mocked(api.grants).mockRejectedValueOnce(
    new ApiError({
      status: 412,
      code: 'common.precondition_failed',
      title: 'Conflict',
    }),
  );
  await start();
  await button('授权').trigger('click');
  await flushPromises();
  expect(
    (wrapper.get('[name=grants]').element as HTMLTextAreaElement).value,
  ).toBe(`member ${pid}`);
  await wrapper.get('form').trigger('submit');
  await wrapper.get('[data-testid=confirm]').trigger('click');
  await flushPromises();
  expect(api.readGrants).toHaveBeenCalledTimes(2);
  expect(api.grants).toHaveBeenCalledTimes(1);
  expect(
    (wrapper.get('[name=grants]').element as HTMLTextAreaElement).value,
  ).toBe(`group ${pid}`);
});

it.each(['staged', 'active', 'retiring', 'revoked'] as const)(
  'enables credential actions only for the loaded %s state',
  async (state) => {
    await start();
    await button('上游凭据').trigger('click');
    vi.mocked(api.credential).mockResolvedValue({
      data: { id: pid, state, revision: 1, createdAt: '2026-10-25T00:00:00Z' },
      etag: '"c1"',
    });
    await wrapper.get('[name=credentialId]').setValue(pid);
    await wrapper.get('[data-testid=read-credential]').trigger('click');
    await flushPromises();
    expect((button('激活').element as HTMLButtonElement).disabled).toBe(
      state !== 'staged',
    );
    expect((button('撤销').element as HTMLButtonElement).disabled).toBe(
      state === 'revoked',
    );
  },
);

it.each([
  [400, '请检查'],
  [409, '状态冲突'],
  [412, '已被他人修改'],
  [429, '请求过于频繁'],
] as const)(
  'distinguishes HTTP %s errors without echoing details',
  async (status, message) => {
    vi.mocked(api.createCredential).mockRejectedValueOnce(
      new ApiError({
        status,
        code: 'common.validation_failed',
        title: 'private',
        detail: 'private-secret',
      }),
    );
    await start();
    await button('上游凭据').trigger('click');
    await wrapper.get('input[type=password]').setValue('fixture');
    await wrapper.get('[data-testid=create-credential]').trigger('click');
    await flushPromises();
    expect(wrapper.get('[role=alert]').text()).toContain(message);
    expect(wrapper.html()).not.toContain('private-secret');
  },
);

it('rereads a credential after a failed conditional revoke and disables revoked actions', async () => {
  vi.mocked(api.credential)
    .mockResolvedValueOnce({
      data: {
        id: pid,
        state: 'active',
        revision: 1,
        createdAt: '2026-10-25T00:00:00Z',
      },
      etag: '"c1"',
    })
    .mockResolvedValue({
      data: {
        id: pid,
        state: 'revoked',
        revision: 2,
        createdAt: '2026-10-25T00:00:00Z',
      },
      etag: '"c2"',
    });
  vi.mocked(api.credentialAction).mockRejectedValueOnce(
    new ApiError({
      status: 412,
      code: 'common.precondition_failed',
      title: 'Conflict',
    }),
  );
  await start();
  await button('上游凭据').trigger('click');
  await wrapper.get('[name=credentialId]').setValue(pid);
  await wrapper.get('[data-testid=read-credential]').trigger('click');
  await flushPromises();
  await wrapper.get('[name=reason]').setValue('rotation');
  await button('撤销').trigger('click');
  await wrapper.get('[data-testid=confirm]').trigger('click');
  await flushPromises();
  expect(api.credential).toHaveBeenCalledTimes(2);
  expect(api.credentialAction).toHaveBeenCalledTimes(1);
  expect(wrapper.get('[role=alert]').text()).toContain('已被他人修改');
  expect((button('撤销').element as HTMLButtonElement).disabled).toBe(true);
});
