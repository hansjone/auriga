import { watch, type Ref } from 'vue';
import type { Locale } from '@auriga/shared';

const key = 'auriga.portal.locale';
export function readPortalLocale(): Locale {
  try {
    return globalThis.localStorage.getItem(key) === 'en' ? 'en' : 'zh-CN';
  } catch {
    return 'zh-CN';
  }
}

export function syncPortalLocale(locale: Ref<string>) {
  return watch(
    locale,
    (value) => {
      document.documentElement.lang = value;
      try {
        globalThis.localStorage.setItem(key, value);
      } catch {
        // Language switching still works when browser storage is unavailable.
      }
    },
    { immediate: true, flush: 'sync' },
  );
}
