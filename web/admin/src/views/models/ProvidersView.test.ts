import { mount, flushPromises } from '@vue/test-utils';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createAurigaI18n } from '@auriga/shared';
import ProvidersView from './ProvidersView.vue';
import { modelProvidersApi as api } from '../../admin/modelProvidersApi';
import {
  providerDraft,
  modelDraft,
  validModel,
  validProvider,
  parseGrants,
} from './forms';
const pid = '01900000-0000-7000-8000-000000000001';
vi.mock('../../admin/modelProvidersApi', () => ({
  modelProvidersApi: {
    providers: vi.fn(),
    models: vi.fn(),
    provider: vi.fn(),
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
};
let wrapper: ReturnType<typeof mount>;
const button = (name: string) =>
  wrapper.findAll('button').find((b) => b.text() === name)!;
beforeEach(() => {
  vi.mocked(api.providers).mockResolvedValue({
    data: { items: [pass] },
    etag: null,
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
    await wrapper.get('[name=credentialEtag]').setValue('"opaque"');
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
  expect(
    (wrapper.get('[name=credentialEtag]').element as HTMLInputElement).value,
  ).toBe('"credential-version"');
});

it('requires confirmation before replacing all model grants', async () => {
  const model = {
    ...modelDraft(pid),
    id: 'example-model',
    upstreamModel: 'upstream',
    revision: 1,
  };
  vi.mocked(api.models).mockResolvedValue({
    data: { items: [model] },
    etag: null,
  });
  await start();
  await button('授权').trigger('click');
  await wrapper.get('[name=etag]').setValue('"grant-version"');
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
