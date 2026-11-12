import { mount, flushPromises } from '@vue/test-utils';
import { it, expect, vi } from 'vitest';
import { createAurigaI18n } from '@auriga/shared';
import DesktopDownload from './DesktopDownload.vue';
const api = vi.hoisted(() => ({ get: vi.fn() }));
vi.mock('../api', () => ({ apiClient: api }));
it('显示平台版本、摘要并链接到固定下载接口', async () => {
  api.get.mockResolvedValue({
    sequence: 4,
    version: '2.1.0',
    sizeBytes: 1048576,
    publishedAt: '2026-11-12T00:00:00Z',
    sha256: 'a'.repeat(64),
  });
  const view = mount(DesktopDownload, {
    global: { plugins: [createAurigaI18n('zh-CN')] },
  });
  await flushPromises();
  expect(view.text()).toContain('2.1.0');
  expect(view.text()).toContain('a'.repeat(64));
  expect(view.get('a').attributes('href')).toBe(
    '/api/resource/v1/desktop/packages/4',
  );
  view.unmount();
});
it('服务失败时不给虚假下载按钮', async () => {
  api.get.mockRejectedValue(new Error('offline'));
  const view = mount(DesktopDownload, {
    global: { plugins: [createAurigaI18n('zh-CN')] },
  });
  await flushPromises();
  expect(view.find('a').exists()).toBe(false);
  expect(view.find('[role=status]').exists()).toBe(true);
  view.unmount();
});
