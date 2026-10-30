export const navigation = [
  { path: '/overview', key: 'navOverview', icon: 'overview', children: [] },
  {
    path: '/resources',
    key: 'governance',
    icon: 'resources',
    children: [
      ['/resources', 'resourceCatalog'],
      ['/resources/review', 'resourceReview'],
      ['/resources/published', 'resourcePublished'],
      ['/resources/distribution', 'resourceDistribution'],
      ['/resources/tools', 'resourceTools'],
    ],
  },
  {
    path: '/members',
    key: 'navMembers',
    icon: 'members',
    children: [
      ['/members', 'members'],
      ['/invitations', 'invitationTitle'],
      ['/groups', 'groupTitle'],
      ['/roles', 'roleTitle'],
      ['/quotas', 'quotaTitle'],
    ],
  },
  {
    path: '/instances',
    key: 'navRuntime',
    icon: 'runtime',
    children: [
      ['/instances', 'instances'],
      ['/runtime/devices', 'navDevices'],
      ['/nodes', 'nodes'],
      ['/runtime/headquarters', 'navHeadquarters'],
    ],
  },
  {
    path: '/models/relay',
    key: 'navModels',
    icon: 'models',
    children: [],
  },
  {
    path: '/providers',
    key: 'navSecurity',
    icon: 'security',
    children: [
      ['/providers', 'identityProviders'],
      ['/security/credentials', 'navCredentials'],
      ['/security/entries', 'navEntries'],
      ['/security/egress', 'navEgress'],
    ],
  },
  {
    path: '/ops/alerts',
    key: 'navOps',
    icon: 'ops',
    children: [
      ['/ops/alerts', 'navAlerts'],
      ['/ops/upgrades', 'navUpgrades'],
      ['/ops/backups', 'navBackups'],
      ['/ops/lifecycle', 'navLifecycle'],
      ['/ops/notifications', 'navNotifications'],
      ['/settings', 'systemSettings'],
    ],
  },
  { path: '/audit', key: 'navAudit', icon: 'audit', children: [] },
] as const;
export const iconPaths: Record<string, string> = {
  overview: 'M3 3h7v7H3zM14 3h7v7h-7zM3 14h7v7H3zM14 14h7v7h-7z',
  resources: 'm12 3 9 5-9 5-9-5zM3 12l9 5 9-5M3 16l9 5 9-5',
  members:
    'M9 11a3 3 0 1 0 0-6 3 3 0 0 0 0 6M3 21v-3a6 6 0 0 1 12 0v3M16 5a3 3 0 0 1 0 6M18 15a5 5 0 0 1 3 5',
  runtime: 'M3 3h18v7H3zM3 14h18v7H3zM7 6h1M7 17h1',
  models: 'm12 2 9 5v10l-9 5-9-5V7zM3 7l9 5 9-5M12 12v10',
  security: 'm12 3 8 3v6c0 4-4 7-8 9-4-2-8-5-8-9V6zM8 12l3 3 5-6',
  ops: 'M3 12h4l3-8 4 16 3-8h4',
  audit: 'M5 3h14v18H5zM9 8h6M9 12h6M9 16h4',
};
