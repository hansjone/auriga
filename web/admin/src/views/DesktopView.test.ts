import { mount, flushPromises } from '@vue/test-utils';
import { it, expect, vi } from 'vitest';
import { createAurigaI18n } from '@auriga/shared';
import DesktopView from './DesktopView.vue';
const api = vi.hoisted(() => ({
  membership: vi.fn(),
  get: vi.fn(),
  delete: vi.fn(),
  request: vi.fn(),
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

it('发布成功后续验过期只提示刷新台账，不误报失败或再次发布', async () => {
  api.membership.mockResolvedValue({
    permissions: ['resource.versions.publish'],
  });
  api.get.mockResolvedValue({ items: [] });
  api.request.mockResolvedValue({});
  const view = mount(DesktopView, {
    global: { plugins: [createAurigaI18n('zh-CN')] },
  });
  await flushPromises();
  const manifest = new File(['{}'], 'release.json', {
    type: 'application/json',
  });
  Object.defineProperty(manifest, 'text', { value: async () => '{}' });
  const archive = new File(['MZ'], 'AurigaDSH-Setup-2.0.0.exe');
  for (const [index, file] of [manifest, archive].entries()) {
    const input = view.findAll('input')[index]!;
    Object.defineProperty(input.element, 'files', { value: [file] });
    await input.trigger('change');
  }
  await flushPromises();
  api.get.mockRejectedValueOnce(new Error('iam.step_up_required'));
  await view.find('form').trigger('submit');
  await flushPromises();
  expect(api.request).toHaveBeenCalledTimes(1);
  expect(api.request).toHaveBeenCalledWith(
    '/api/resource/v1/admin/desktop/releases',
    expect.objectContaining({ method: 'POST', body: archive }),
  );
  expect(view.find('[role="alert"]').exists()).toBe(false);
  expect(view.text()).toContain('发行已发布，台账刷新失败，请验证身份并刷新。');
  view.unmount();
});

it('下架已提交但台账刷新失败时仍说明下架完成', async () => {
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
          sizeBytes: 3,
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
  api.get.mockRejectedValueOnce(new Error('iam.step_up_required'));
  await view
    .findAll('button')
    .find((b) => b.text() === '下架')!
    .trigger('click');
  await flushPromises();
  expect(api.delete).toHaveBeenCalledTimes(1);
  expect(view.find('[role="alert"]').exists()).toBe(false);
  expect(view.text()).toContain('发行已下架，台账刷新失败，请验证身份并刷新。');
  view.unmount();
});
