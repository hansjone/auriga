import { createRouter, createWebHistory } from 'vue-router';
import { BRAND_NAME } from '@auriga/shared';
import LoginView from './views/LoginView.vue';
import InviteView from './views/InviteView.vue';
import QrConfirmView from './views/QrConfirmView.vue';
import PortalEntry from './views/PortalEntry.vue';
import AccountSecurityLayout from './views/account/AccountSecurityLayout.vue';
import SecurityView from './views/account/SecurityView.vue';
import DeviceView from './views/DeviceView.vue';
import ResourceMarketView from './views/ResourceMarketView.vue';

export const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/resources', component: ResourceMarketView },
    { path: '/login', component: LoginView, meta: { public: true } },
    {
      path: '/device/:userCode?',
      component: DeviceView,
      meta: { public: true },
    },
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
    {
      path: '/',
      component: PortalEntry,
      meta: { public: true },
    },
    {
      path: '/account',
      component: AccountSecurityLayout,
      children: [{ path: 'security', component: SecurityView }],
    },
  ],
});

router.afterEach((to) => {
  const page = to.path.startsWith('/invite/')
    ? '邀请 / Invitation'
    : to.path.startsWith('/login/qr/')
      ? '设备确认 / Device confirmation'
      : to.path === '/login'
        ? '登录 / Sign in'
        : to.path === '/resources'
          ? '我的 DSH 资源 / My DSH resources'
          : to.path === '/account/security'
            ? '账号安全 / Account security'
            : '门户 / Portal';
  document.title = `${BRAND_NAME} · ${page}`;
});
