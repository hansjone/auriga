import { createRouter, createWebHistory } from 'vue-router';
import LoginView from './views/LoginView.vue';
import InviteView from './views/InviteView.vue';
import QrConfirmView from './views/QrConfirmView.vue';
import HomeView from './views/HomeView.vue';

export const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/login', component: LoginView, meta: { public: true } },
    {
      path: '/invite/:code',
      component: InviteView,
      meta: { public: true },
    },
    {
      path: '/login/qr/:challengeId',
      component: QrConfirmView,
      meta: { public: true },
    },
    { path: '/', component: HomeView },
  ],
});
