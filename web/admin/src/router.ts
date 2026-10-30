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
import IdentityProvidersView from './views/IdentityProvidersView.vue';
import SettingsView from './views/SettingsView.vue';
import WorkflowUnavailableView from './views/WorkflowUnavailableView.vue';
import OverviewView from './views/OverviewView.vue';
import { navigation } from './admin/navigation';

const existing = new Set([
  '/overview',
  '/models/providers',
  '/settings',
  '/resources',
  '/members',
  '/nodes',
  '/instances',
  '/groups',
  '/invitations',
  '/quotas',
  '/roles',
  '/providers',
  '/models/relay',
  '/models/usage',
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
        { path: '', redirect: '/overview' },
        { path: 'overview', component: OverviewView },
        { path: 'resources', component: ResourcesView },
        { path: 'members', component: MembersView },
        { path: 'nodes', component: NodesView },
        { path: 'instances', component: InstancesView },
        { path: 'groups', component: GroupsView },
        { path: 'invitations', component: InvitationsView },
        { path: 'quotas', component: QuotasView },
        { path: 'roles', component: RolesView },
        { path: 'providers', component: IdentityProvidersView },
        { path: 'settings', component: SettingsView },
        {
          path: 'models/:pathMatch(.*)*',
          beforeEnter: () => {
            window.location.assign('/ai/ui/');
            return false;
          },
          component: WorkflowUnavailableView,
        },
        ...unavailable.map((path) => ({
          path: path.slice(1),
          component: WorkflowUnavailableView,
        })),
      ],
    },
  ],
});
