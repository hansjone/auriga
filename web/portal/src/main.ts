import { createApp } from 'vue';
import { createPinia } from 'pinia';
import 'element-plus/dist/index.css';
import '@auriga/shared/theme.css';
import { createAurigaI18n, enableAurigaMock } from '@auriga/shared';
import App from './App.vue';
import { router } from './router';
import { bindApiRouter } from './api';
import { readPortalLocale, syncPortalLocale } from './portal-locale';

async function boot() {
  if (import.meta.env.VITE_AURIGA_MOCK === '1') {
    await enableAurigaMock();
  }
  bindApiRouter(router);
  const i18n = createAurigaI18n(readPortalLocale());
  syncPortalLocale(i18n.global.locale);
  createApp(App).use(createPinia()).use(router).use(i18n).mount('#app');
}

void boot();
