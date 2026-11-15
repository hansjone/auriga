import { mount, flushPromises } from '@vue/test-utils';
import { afterEach, it, expect, vi } from 'vitest';
import { createAurigaI18n } from '@auriga/shared';
import DesktopDownload from './DesktopDownload.vue';
const api = vi.hoisted(() => ({ get: vi.fn() }));
vi.mock('../api', () => ({ apiClient: api }));
afterEach(() => vi.unstubAllGlobals());
const manifest = {
  sequence: 4,
  version: '2.1.0',
  sizeBytes: 1048576,
  publishedAt: '2026-11-12T00:00:00Z',
  sha256: 'a'.repeat(64),
};
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

it.each([true, false])('复制校验值的成功/失败反馈：%s', async (success) => {
  api.get.mockResolvedValue(manifest);
  const writeText = success
    ? vi.fn().mockResolvedValue(undefined)
    : vi.fn().mockRejectedValue(new Error('denied'));
  vi.stubGlobal('navigator', { clipboard: { writeText } });
  const view = mount(DesktopDownload, {
    global: { plugins: [createAurigaI18n('zh-CN')] },
  });
  await flushPromises();
  await view.get('.release-panel button').trigger('click');
  await flushPromises();
  expect(writeText).toHaveBeenCalledExactlyOnceWith(manifest.sha256);
  expect(view.get('[role=status]').text()).toBe(
    success ? '已复制' : '复制失败，请手动复制',
  );
  view.unmount();
});

it('剪贴板不可用时仍保留可手动复制的完整校验值', async () => {
  api.get.mockResolvedValue(manifest);
  vi.stubGlobal('navigator', {});
  const view = mount(DesktopDownload, {
    global: { plugins: [createAurigaI18n('en')] },
  });
  await flushPromises();
  await view.get('.release-panel button').trigger('click');
  await flushPromises();
  expect(view.get('[role=status]').text()).toContain('Copy failed');
  expect(view.get('code').text()).toBe(manifest.sha256);
  view.unmount();
});

it('Esc 恢复焦点、点外部关闭，显示本地化时间与品牌无障碍名称', async () => {
  api.get.mockResolvedValue(manifest);
  const i18n = createAurigaI18n('en');
  const view = mount(DesktopDownload, {
    attachTo: document.body,
    global: { plugins: [i18n] },
  });
  await flushPromises();
  const details = view.get('details').element as globalThis.HTMLDetailsElement;
  details.open = true;
  document.dispatchEvent(
    new globalThis.KeyboardEvent('keydown', { key: 'Escape' }),
  );
  expect(details.open).toBe(false);
  expect(document.activeElement).toBe(view.get('summary').element);
  details.open = true;
  await view.get('.release-panel').trigger('click');
  expect(details.open).toBe(true);
  document.body.click();
  expect(details.open).toBe(false);
  expect(view.get('section').attributes('aria-label')).toBe('Auriga DSH');
  expect(view.get('time').attributes('datetime')).toBe(manifest.publishedAt);
  const formatted = new Intl.DateTimeFormat('en', {
    dateStyle: 'medium',
    timeStyle: 'short',
  }).format(new Date(manifest.publishedAt));
  expect(view.get('time').text()).toBe(formatted);
  i18n.global.locale.value = 'zh-CN';
  await flushPromises();
  expect(view.get('time').text()).not.toBe(formatted);
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
