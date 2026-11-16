import { mount, flushPromises } from '@vue/test-utils';
import { afterEach, expect, it, vi } from 'vitest';
import { createAurigaI18n } from '@auriga/shared';
import AboutView from './AboutView.vue';

afterEach(() => vi.unstubAllGlobals());

it('显示现场版本、源码提交及改动来源', async () => {
  vi.stubGlobal(
    'fetch',
    vi.fn().mockResolvedValue({
      ok: true,
      json: async () => ({
        version: '0.2.0-site.3',
        upstreamVersion: '0.2.0',
        commit: 'a'.repeat(40),
        branch: 'site/fix',
        dirty: true,
        siteChanges: true,
        snapshotSha256: 'b'.repeat(64),
        builtAt: '2026-11-16T00:00:00Z',
      }),
    }),
  );
  const view = mount(AboutView, {
    global: { plugins: [createAurigaI18n('zh-CN')] },
  });
  await flushPromises();
  expect(view.text()).toContain('0.2.0-site.3');
  expect(view.text()).toContain('site/fix');
  expect(view.text()).toContain('含现场改动');
  expect(view.text()).toContain('是');
  view.unmount();
});

it('缺少版本来源时显示明确状态', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: false }));
  const view = mount(AboutView, {
    global: { plugins: [createAurigaI18n('zh-CN')] },
  });
  await flushPromises();
  expect(view.text()).toContain('当前发行尚未记录版本来源');
  view.unmount();
});
