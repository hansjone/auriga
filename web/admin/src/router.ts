import EntriesView from './views/security/EntriesView.vue';
import EgressView from './views/security/EgressView.vue';
import DevicesView from './views/DevicesView.vue';
import UpgradesView from './views/ops/UpgradesView.vue';
import BackupsView from './views/ops/BackupsView.vue';
import LifecycleView from './views/ops/LifecycleView.vue';
import { createRouter, createWebHistory } from 'vue-router';
import AuditView from './views/AuditView.vue';
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
import ResourceGovernanceView from './views/resources/ResourceGovernanceView.vue';
import IdentityProvidersView from './views/IdentityProvidersView.vue';
import SettingsView from './views/SettingsView.vue';
import WorkflowUnavailableView from './views/WorkflowUnavailableView.vue';
import OverviewView from './views/OverviewView.vue';
import RelayView from './views/models/RelayView.vue';
import ProvidersView from './views/models/ProvidersView.vue';
import KeysView from './views/models/KeysView.vue';
import UsageView from './views/models/UsageView.vue';
import { navigation } from './admin/navigation';

import AlertsView from './views/ops/AlertsView.vue';
import NotificationsView from './views/ops/NotificationsView.vue';

const existing = new Set([
  '/security/entries',
  '/security/egress',
  '/runtime/devices',
  '/ops/upgrades',
  '/ops/backups',
  '/ops/lifecycle',
  '/audit',
  '/ops/alerts',
  '/ops/notifications',
  '/overview',
  '/models/providers',
  '/settings',
  '/resources',
  '/resources/review',
  '/resources/published',
  '/resources/distribution',
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
  '/models/keys',
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
        { path: 'security/entries', component: EntriesView },
        { path: 'security/egress', component: EgressView },
        { path: 'ops/upgrades', component: UpgradesView },
        { path: 'ops/backups', component: BackupsView },
        { path: 'ops/lifecycle', component: LifecycleView },
        { path: 'audit', component: AuditView },
        { path: 'ops/alerts', component: AlertsView },
        { path: 'ops/notifications', component: NotificationsView },
        { path: 'resources', component: ResourcesView },
        ...(['review', 'published', 'distribution'] as const).map((phase) => ({
          path: 'resources/' + phase,
          component: ResourceGovernanceView,
          props: { phase },
        })),
        { path: 'members', component: MembersView },
        { path: 'runtime/devices', component: DevicesView },
        { path: 'nodes', component: NodesView },
        { path: 'instances', component: InstancesView },
        { path: 'groups', component: GroupsView },
        { path: 'invitations', component: InvitationsView },
        { path: 'quotas', component: QuotasView },
        { path: 'roles', component: RolesView },
        { path: 'providers', component: IdentityProvidersView },
        { path: 'settings', component: SettingsView },
        { path: 'models/relay', component: RelayView },
        { path: 'models/providers', component: ProvidersView },
        { path: 'models/keys', component: KeysView },
        { path: 'models/usage', component: UsageView },
        {
          path: 'models/:pathMatch(.*)*',
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
