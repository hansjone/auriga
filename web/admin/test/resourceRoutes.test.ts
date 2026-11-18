import { flushPromises, mount, type VueWrapper } from '@vue/test-utils';
import { afterEach, expect, it, vi } from 'vitest';
import { createMemoryHistory, createRouter, RouterView } from 'vue-router';
import { createAurigaI18n } from '@auriga/shared';
import { adminApi } from '../src/admin/api';
import { navigation } from '../src/admin/navigation';
import { resourceCatalogApi } from '../src/admin/resourceCatalogApi';
import { apiClient } from '../src/api';
import { router } from '../src/router';
import ResourceCenterView from '../src/views/resources/ResourceCenterView.vue';

let view: VueWrapper | undefined;
afterEach(() => {
  view?.unmount();
  view = undefined;
  vi.restoreAllMocks();
});

function memoryRouter() {
  return createRouter({
    history: createMemoryHistory(),
    routes: router.options.routes,
  });
}

async function open(path: string, permissions: string[]) {
  vi.spyOn(adminApi, 'membership').mockResolvedValue({
    permissions,
  } as Awaited<ReturnType<typeof adminApi.membership>>);
  const routes = memoryRouter();
  await routes.push(path);
  view = mount(RouterView, {
    global: {
      plugins: [routes, createAurigaI18n('en')],
      stubs: { AdminLayout: { template: '<RouterView />' } },
    },
  });
  await flushPromises();
  return { routes, view };
}

it('opens the plugin and skill directories and review queues via the catalog', async () => {
  const get = vi.spyOn(apiClient, 'get').mockResolvedValue({ items: [] });
  const { routes, view } = await open('/resources', [
    'resource.packages.read',
    'resource.versions.review',
  ]);
  expect(routes.currentRoute.value.path).toBe('/resources/plugins');
  expect(view.get('[aria-current="page"]').attributes('href')).toBe(
    '/resources/plugins',
  );
  expect(get).toHaveBeenCalledTimes(1);
  await routes.push('/resources/skills');
  await flushPromises();
  expect(view.get('[aria-current="page"]').attributes('href')).toBe(
    '/resources/skills',
  );
  expect(get).toHaveBeenCalledTimes(2);
  await routes.push('/resources/plugins/review');
  await flushPromises();
  expect(get).toHaveBeenCalledTimes(3);
  await routes.push('/resources/review?kind=skill');
  await flushPromises();
  expect(routes.currentRoute.value.path).toBe('/resources/skills/review');
  expect(view.get('[aria-current="page"]').attributes('href')).toBe(
    '/resources/skills/review',
  );
  expect(get.mock.calls.map(([path]) => path)).toEqual([
    '/api/resource/v1/catalog/packages?kind=plugin&limit=50',
    '/api/resource/v1/catalog/packages?kind=skill&limit=50',
    '/api/resource/v1/admin/catalog/review?kind=plugin&limit=50',
    '/api/resource/v1/admin/catalog/review?kind=skill&limit=50',
  ]);
});

it('requires review permission before loading the review queue', async () => {
  const get = vi.spyOn(apiClient, 'get').mockResolvedValue({ items: [] });
  const { view } = await open('/resources/plugins/review', [
    'resource.packages.read',
  ]);
  expect(view.get('[role="alert"]').text()).toContain('Permission denied');
  expect(get).not.toHaveBeenCalled();
});

it('keeps every resource phase and its phase-first redirect', async () => {
  const routes = memoryRouter();
  for (const kind of ['plugins', 'skills']) {
    for (const phase of ['', '/review', '/published', '/distribution']) {
      const to = routes.resolve(`/resources/${kind}${phase}`);
      expect(to.matched.at(-1)?.components?.default).toBe(ResourceCenterView);
    }
    for (const phase of ['review', 'published', 'distribution']) {
      await routes.push(
        `/resources/${phase}${kind === 'skills' ? '?kind=skill' : ''}`,
      );
      expect(routes.currentRoute.value.path).toBe(
        `/resources/${kind}/${phase}`,
      );
    }
  }
});

it('keeps concurrent catalog detail response ETags isolated', async () => {
  const meta = document.createElement('meta');
  meta.name = 'auriga-access-mode';
  meta.content = 'domain';
  document.head.append(meta);
  const response = (etag: string) =>
    Response.json({}, { headers: { ETag: etag } });
  let respondFirst!: (response: Response) => void;
  const first = new Promise<Response>((resolve) => {
    respondFirst = resolve;
  });
  vi.spyOn(globalThis, 'fetch')
    .mockReturnValueOnce(first)
    .mockResolvedValueOnce(response('"second"'));
  try {
    const pendingFirst = resourceCatalogApi.get('first');
    const second = await resourceCatalogApi.get('second');
    respondFirst(response('"first"'));
    expect(second.etag).toBe('"second"');
    expect((await pendingFirst).etag).toBe('"first"');
  } finally {
    meta.remove();
  }
});

it('keeps the eight primary groups and existing management links', () => {
  expect(navigation).toHaveLength(8);
  for (const path of [
    '/members',
    '/nodes',
    '/instances',
    '/groups',
    '/invitations',
    '/quotas',
    '/roles',
    '/providers',
    '/settings',
  ]) {
    expect(
      router.resolve(path).matched.at(-1)?.components?.default,
    ).toBeTruthy();
  }
});

it('preserves the headquarters notice and device and relay navigation', async () => {
  const routes = memoryRouter();
  await routes.push('/runtime/headquarters');
  view = mount(RouterView, {
    global: {
      plugins: [routes, createAurigaI18n('zh-CN')],
      stubs: { AdminLayout: { template: '<RouterView />' } },
    },
  });
  expect(view.text()).toContain('独立系统 DSH');
  expect(view.text()).toContain('尚未开放');
  expect(
    navigation.flatMap((item) => item.children.map(([path]) => path)),
  ).toContain('/runtime/devices');
  expect(navigation.some((item) => item.path === '/models/relay')).toBe(true);
});
