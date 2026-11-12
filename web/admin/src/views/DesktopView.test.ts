import { mount, flushPromises } from '@vue/test-utils';
import { it, expect, vi } from 'vitest';
import { createAurigaI18n } from '@auriga/shared';
import DesktopView from './DesktopView.vue';
const api = vi.hoisted(() => ({
  membership: vi.fn(),
  get: vi.fn(),
  delete: vi.fn(),
}));
vi.mock('../admin/api', () => ({ adminApi: { membership: api.membership } }));
vi.mock('../api', () => ({ apiClient: api }));
it('无发布权限不读取发行记录', async () => {
  api.membership.mockResolvedValue({ permissions: [] });
  const view = mount(DesktopView, {
    global: { plugins: [createAurigaI18n('zh-CN')] },
  });
  await flushPromises();
  expect(api.get).not.toHaveBeenCalled();
  expect(view.find('form').exists()).toBe(false);
  view.unmount();
});
it('显示真实发行记录并下架', async () => {
  api.membership.mockResolvedValue({
    permissions: ['resource.versions.publish'],
  });
  api.get.mockResolvedValue({
    items: [
      {
        withdrawn: false,
        manifest: {
          sequence: 2,
          version: '2.0.0',
          sizeBytes: 1048576,
          publishedAt: '2026-11-12T00:00:00Z',
          sha256: 'a'.repeat(64),
          notes: 'release',
        },
      },
    ],
  });
  api.delete.mockResolvedValue(undefined);
  const view = mount(DesktopView, {
    global: { plugins: [createAurigaI18n('zh-CN')] },
  });
  await flushPromises();
  expect(view.text()).toContain('2.0.0');
  const button = view.findAll('button').find((b) => b.text() === '下架')!;
  await button.trigger('click');
  await flushPromises();
  expect(api.delete).toHaveBeenCalledWith(
    '/api/resource/v1/admin/desktop/releases/2',
  );
  view.unmount();
});
