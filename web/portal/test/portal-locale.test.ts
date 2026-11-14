import { afterEach, expect, it, vi } from 'vitest';
import { ref } from 'vue';
import { readPortalLocale, syncPortalLocale } from '../src/portal-locale';

afterEach(() => {
  vi.unstubAllGlobals();
  globalThis.localStorage.clear();
});
it('切换语言同步 HTML 并在重新进入时恢复选择', () => {
  const locale = ref('zh-CN');
  const stop = syncPortalLocale(locale);
  locale.value = 'en';
  expect(document.documentElement.lang).toBe('en');
  expect(readPortalLocale()).toBe('en');
  locale.value = 'zh-CN';
  expect(document.documentElement.lang).toBe('zh-CN');
  expect(readPortalLocale()).toBe('zh-CN');
  stop();
});
it('浏览器禁止存储时仍能切换 HTML 语言', () => {
  vi.stubGlobal('localStorage', {
    getItem: () => {
      throw new Error('blocked');
    },
    setItem: () => {
      throw new Error('blocked');
    },
  });
  expect(readPortalLocale()).toBe('zh-CN');
  const locale = ref('en');
  const stop = syncPortalLocale(locale);
  expect(document.documentElement.lang).toBe('en');
  stop();
});
