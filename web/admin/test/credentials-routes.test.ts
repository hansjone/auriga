import { expect, it } from 'vitest';
import { router } from '../src/router';
import CredentialsView from '../src/views/CredentialsView.vue';
import AlertsView from '../src/views/ops/AlertsView.vue';
import NotificationsView from '../src/views/ops/NotificationsView.vue';

it.each([
  ['/security/credentials', CredentialsView],
  ['/ops/alerts', AlertsView],
  ['/ops/notifications', NotificationsView],
] as const)(
  'resolves %s to its implemented page exactly once',
  (path, component) => {
    expect(
      router.getRoutes().filter((route) => route.path === path),
    ).toHaveLength(1);
    expect(router.resolve(path).matched.at(-1)?.components?.default).toBe(
      component,
    );
  },
);
