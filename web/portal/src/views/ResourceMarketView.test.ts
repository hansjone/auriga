import { flushPromises, mount } from '@vue/test-utils';
import { beforeEach, expect, it, vi } from 'vitest';
import { ApiError, createAurigaI18n } from '@auriga/shared';
import ResourceMarketView from './ResourceMarketView.vue';
const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }));
vi.mock('../api', () => ({ apiClient: api }));
const identity = {
  tenantId: 't',
  packageId: 'pack',
  versionId: 'v',
  kind: 'skill',
  version: '1.0.0',
  manifestDigest: 'sha256:' + 'a'.repeat(64),
};
const placement = {
  placementId: 'p',
  mode: 'dedicated',
  state: 'ready',
  epoch: 1,
  revision: 2,
};
const row = {
  placementId: 'p',
  identity,
  selectionRevision: 4,
  installationRevision: 6,
  desiredState: 'ENABLED',
  installationMode: 'optional',
  state: 'applied',
  policyOverride: false,
};
let mode = 'dedicated';
beforeEach(() => {
  mode = 'dedicated';
  api.get.mockReset();
  api.post.mockReset();
  api.get.mockImplementation(async (path: string) =>
    path.includes('/me/placement')
      ? { ...placement, mode }
      : path.includes('/installations')
        ? {
            ...placement,
            mode,
            nodeObserved: true,
            viewId: 'view',
            items: [row],
          }
        : { items: [] },
  );
});
const setup = () =>
  mount(ResourceMarketView, {
    global: { plugins: [createAurigaI18n('zh-CN')] },
  });

it('shared mode exposes the controlled resource state and dedicated guidance without personal actions', async () => {
  mode = 'shared';
  const view = setup();
  await flushPromises();
  expect(view.text()).toContain('先向管理员申请独立容器');
  expect(view.findAll('button').map((b) => b.text())).toEqual(['刷新状态']);
  expect(
    api.get.mock.calls.some(([path]) => /\/packages\?/.test(String(path))),
  ).toBe(false);
  expect(api.post).not.toHaveBeenCalled();
  view.unmount();
});

it('a 202 disable remains applying until the current authoritative receipt is observed', async () => {
  const view = setup();
  await flushPromises();
  api.post.mockResolvedValue({
    ...row,
    desiredState: 'DISABLED',
    state: 'applying',
    selectionRevision: 5,
  });
  api.get.mockResolvedValueOnce({
    ...placement,
    nodeObserved: false,
    viewId: 'new',
    items: [
      {
        ...row,
        desiredState: 'DISABLED',
        state: 'applying',
        selectionRevision: 5,
      },
    ],
  });
  await view
    .findAll('button')
    .find((b) => b.text() === '停用')!
    .trigger('click');
  await flushPromises();
  expect(api.post).toHaveBeenCalledWith(
    '/api/resource/v1/catalog/placements/p/installations',
    { selection: { identity, action: 'disable' } },
    expect.objectContaining({
      headers: { 'Idempotency-Key': expect.any(String), 'If-Match': '"4"' },
    }),
  );
  expect(view.text()).toContain('正在应用');
  expect(view.text()).not.toContain('已停用');
  view.unmount();
});

it('an expired session clears controls and offers a fresh login', async () => {
  api.get.mockRejectedValue(
    new ApiError({
      status: 401,
      title: 'Expired',
      code: 'common.unauthenticated',
    }),
  );
  const view = setup();
  await flushPromises();
  expect(view.get('a[href="/login"]').text()).toBe('重新登录');
  expect(view.findAll('button')[0].attributes('disabled')).toBeDefined();
  expect(api.post).not.toHaveBeenCalled();
  view.unmount();
});
