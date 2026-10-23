import { createRouter, createWebHistory } from 'vue-router';
import LoginView from './views/LoginView.vue';
import AdminLayout from './layouts/AdminLayout.vue';
import MembersView from './views/MembersView.vue';
import NodesView from './views/NodesView.vue';
import InstancesView from './views/InstancesView.vue';
import PlaceholderView from './views/PlaceholderView.vue';

export const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/login', component: LoginView, meta: { public: true } },
    {
      path: '/',
      component: AdminLayout,
      children: [
        { path: '', redirect: '/members' },
        { path: 'members', component: MembersView },
        { path: 'nodes', component: NodesView },
        { path: 'instances', component: InstancesView },
        {
          path: 'groups',
          component: PlaceholderView,
          props: { titleKey: 'adminNav.groups' },
        },
        {
          path: 'roles',
          component: PlaceholderView,
          props: { titleKey: 'adminNav.roles' },
        },
        {
          path: 'providers',
          component: PlaceholderView,
          props: { titleKey: 'adminNav.providers' },
        },
        {
          path: 'settings',
          component: PlaceholderView,
          props: { titleKey: 'adminNav.settings' },
        },
      ],
    },
  ],
});
