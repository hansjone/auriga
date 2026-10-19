import { createApp } from 'vue';
import { createPinia } from 'pinia';
import 'element-plus/dist/index.css';
import '@auriga/shared/theme.css';
import { createAurigaI18n, enableAurigaMock } from '@auriga/shared';
import App from './App.vue';
import { router } from './router';
import { bindApiRouter } from './api';

async function boot() {
  if (import.meta.env.VITE_AURIGA_MOCK === '1') {
    await enableAurigaMock();
  }
  bindApiRouter(router);
  createApp(App)
    .use(createPinia())
    .use(router)
    .use(createAurigaI18n())
    .mount('#app');
}

void boot();
