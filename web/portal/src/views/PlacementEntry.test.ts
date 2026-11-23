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
  expect(api.post).toHaveBeenCalledTimes(1);
  view.unmount();
});
it('rejects unknown mode rather than showing a dedicated entry', async () => {
  api.get.mockResolvedValue({ mode: 'future-mode', state: 'ready' });
  const view = setup();
  await flushPromises();
  expect(openButton(view).attributes('disabled')).toBeDefined();
  view.unmount();
});
