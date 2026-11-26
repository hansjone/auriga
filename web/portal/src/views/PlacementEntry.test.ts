import { mount, flushPromises } from '@vue/test-utils';
import { beforeEach, expect, it, vi } from 'vitest';
import { ApiError, createAurigaI18n } from '@auriga/shared';
import PlacementEntry from './PlacementEntry.vue';
const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }));
vi.mock('../api', () => ({ apiClient: api }));
beforeEach(() => {
  api.get.mockReset();
  api.post.mockReset();
});
function setup(locale: 'zh-CN' | 'en' = 'zh-CN') {
  return mount(PlacementEntry, {
    global: { plugins: [createAurigaI18n(locale)] },
  });
}
function openButton(view: ReturnType<typeof setup>) {
  return view.findAll('button').find((node) => {
    const text = node.text();
    return text.includes('打开') || text.includes('Open');
  })!;
}
it('opens only from unassigned or ready, never posts during a switch', async () => {
  api.get.mockResolvedValue({ mode: 'shared', state: 'switching' });
  const view = setup();
  await flushPromises();
  expect(openButton(view).attributes('disabled')).toBeDefined();
  expect(view.text()).toContain('模式正在切换中');
  expect(api.post).not.toHaveBeenCalled();
  view.unmount();
});
it('shows English risk disclosure and a login action after expiry', async () => {
  api.get.mockRejectedValue(
    new ApiError({
      status: 401,
      title: 'expired',
      code: 'common.unauthenticated',
    }),
  );
  const view = setup('en');
  await flushPromises();
  await view.get('.auriga-hint-trigger').trigger('click');
  expect(view.text()).toContain('audit attributed to that member');
  expect(view.find('a[href="/login"]').text()).toBe('Sign in again');
  view.unmount();
});
it('allows first open after the contractual 404 and reports failure without fallback', async () => {
  api.get.mockRejectedValue(
    new ApiError({ status: 404, title: 'Missing', code: 'common.not_found' }),
  );
  api.post.mockRejectedValue(
    new ApiError({ status: 409, title: 'Capacity', code: 'common.conflict' }),
  );
  const view = setup();
  await flushPromises();
  await openButton(view).trigger('click');
  await flushPromises();
  expect(api.post).toHaveBeenCalledWith(
    '/api/workspace/v1/me/placement/open',
    undefined,
    expect.objectContaining({
      headers: { 'Idempotency-Key': expect.any(String) },
    }),
  );
  expect(view.text()).toContain('common.conflict');
  expect(view.find('[data-testid="fallback-personal"]').exists()).toBe(false);
  expect(api.post).toHaveBeenCalledTimes(1);
  view.unmount();
});
it.each([
  { mode: 'future-mode', state: 'ready' },
  { mode: 'dedicated', state: 'blocked' },
])('rejects an unsupported placement (%o)', async (placement) => {
  api.get.mockResolvedValue(placement);
  const view = setup();
  await flushPromises();
  expect(openButton(view).attributes('disabled')).toBeDefined();
  expect(view.find('[data-testid="fallback-personal"]').exists()).toBe(false);
  view.unmount();
});
it('emits personal fallback when unassigned and hides it once placement exists', async () => {
  api.get.mockRejectedValue(
    new ApiError({ status: 404, title: 'Missing', code: 'common.not_found' }),
  );
  const unassignedView = setup();
  await flushPromises();
  const btn = unassignedView.find('[data-testid="fallback-personal"]');
  expect(btn.exists()).toBe(true);
  await btn.trigger('click');
  expect(unassignedView.emitted('fallback')).toHaveLength(1);
  unassignedView.unmount();

  for (const state of ['pending', 'switching', 'unavailable', 'ready']) {
    api.get.mockResolvedValue({ mode: 'shared', state });
    const existingView = setup();
    await flushPromises();
    expect(
      existingView.find('[data-testid="fallback-personal"]').exists(),
    ).toBe(false);
    existingView.unmount();
  }
});

it.each(['zh-CN', 'en'] as const)(
  'offers explicit legacy entry after the migration code (%s)',
  async (locale) => {
    api.get
      .mockRejectedValueOnce(
        new ApiError({
          status: 404,
          title: 'Missing',
          code: 'common.not_found',
        }),
      )
      .mockResolvedValueOnce({ mode: 'dedicated', state: 'unavailable' });
    api.post.mockRejectedValueOnce(
      new ApiError({
        status: 409,
        title: 'Conflict',
        code: 'shared.legacy_migration_required',
      }),
    );
    const view = setup(locale);
    await flushPromises();
    await openButton(view).trigger('click');
    await flushPromises();
    const english = locale === 'en';
    expect(view.text()).toContain(
      english ? 'admin console' : '管理台完成维护迁移',
    );
    expect(view.emitted('fallback')).toBeUndefined();
    const fallback = view.get('[data-testid="fallback-personal"]');
    expect(fallback.text()).toBe(
      english
        ? 'Continue to existing dedicated instance'
        : '继续进入现有独享实例',
    );
    await fallback.trigger('click');
    expect(view.emitted('fallback')).toHaveLength(1);
    await view
      .findAll('button')
      .find(
        (node) => node.text() === (english ? 'Refresh status' : '刷新状态'),
      )!
      .trigger('click');
    await flushPromises();
    expect(view.text()).toContain(
      english ? 'temporarily unavailable' : '当前实例暂不可用',
    );
    expect(view.find('[data-testid="fallback-personal"]').exists()).toBe(false);
    view.unmount();
  },
);
