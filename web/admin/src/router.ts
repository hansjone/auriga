import { createRouter, createWebHistory } from 'vue-router';
import LoginView from './views/LoginView.vue';
import AdminLayout from './layouts/AdminLayout.vue';
import MembersView from './views/MembersView.vue';
import NodesView from './views/NodesView.vue';
import InstancesView from './views/InstancesView.vue';
import GroupsView from './views/GroupsView.vue';
import InvitationsView from './views/InvitationsView.vue';
import QuotasView from './views/QuotasView.vue';
import RolesView from './views/RolesView.vue';
import ResourcesView from './views/ResourcesView.vue';
import WorkflowUnavailableView from './views/WorkflowUnavailableView.vue';
import { navigation } from './admin/navigation';

const existing = new Set([
  '/resources',
  '/members',
  '/nodes',
  '/instances',
  '/groups',
  '/invitations',
  '/quotas',
  '/roles',
]);
const unavailable = [
  ...new Set(
    navigation.flatMap((item) => [
      item.path,
      ...item.children.map((child) => child[0]),
    ]),
  ),
].filter((path) => !existing.has(path));

export const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/login', component: LoginView, meta: { public: true } },
    {
      path: '/',
      component: AdminLayout,
      children: [
        { path: '', redirect: '/resources' },
        { path: 'resources', component: ResourcesView },
        { path: 'members', component: MembersView },
        { path: 'nodes', component: NodesView },
        { path: 'instances', component: InstancesView },
        { path: 'groups', component: GroupsView },
        { path: 'invitations', component: InvitationsView },
        { path: 'quotas', component: QuotasView },
        { path: 'roles', component: RolesView },
        ...unavailable.map((path) => ({
          path: path.slice(1),
          component: WorkflowUnavailableView,
        })),
      ],
    },
  ],
});
