import { createApp } from 'vue';
import { createPinia } from 'pinia';
import { createRouter, createWebHistory } from 'vue-router';
import 'element-plus/dist/index.css';
import '@auriga/shared/theme.css';
import { createAurigaI18n } from '@auriga/shared';
import App from './App.vue';
import Home from './Home.vue';
createApp(App)
  .use(createPinia())
  .use(
    createRouter({
      history: createWebHistory(),
      routes: [{ path: '/', component: Home }],
    }),
  )
  .use(createAurigaI18n())
  .mount('#app');
