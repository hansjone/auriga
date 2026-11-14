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
function setup() {
  return mount(PlacementEntry, {
    global: { plugins: [createAurigaI18n('zh-CN')] },
  });
}
it('opens only from unassigned or ready, never posts during a switch', async () => {
  api.get.mockResolvedValue({ mode: 'shared', state: 'switching' });
  const view = setup();
  await flushPromises();
  expect(view.findAll('button')[0].attributes('disabled')).toBeDefined();
  expect(api.post).not.toHaveBeenCalled();
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
  await view.findAll('button')[0].trigger('click');
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
  expect(view.findAll('button')[0].attributes('disabled')).toBeDefined();
  view.unmount();
});
