import { createApp } from 'vue';
import { createPinia } from 'pinia';
import 'element-plus/dist/index.css';
import '@auriga/shared/theme.css';
import {
  createAurigaI18n,
  enableAurigaMock,
  readAccessMode,
} from '@auriga/shared';
import App from './App.vue';
import { router } from './router';
import { bindApiRouter } from './api';

async function boot() {
  if (import.meta.env.VITE_AURIGA_MOCK === '1') {
    await enableAurigaMock();
    const mode = document.createElement('meta');
    mode.name = 'auriga-access-mode';
    mode.content = 'domain';
    document.head.append(mode);
  }
  try {
    readAccessMode();
  } catch (error) {
    const notice = document.createElement('p');
    notice.setAttribute('role', 'alert');
    notice.textContent = (error as Error).message;
    document.body.prepend(notice);
  }
  bindApiRouter(router);
  createApp(App)
    .use(createPinia())
    .use(router)
    .use(createAurigaI18n())
    .mount('#app');
}

void boot();
