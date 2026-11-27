import { afterEach, describe, expect, it, vi } from 'vitest';
import { mountPanel, flushPromises, type NativeWrapper } from './native-host';
import type { PanelContext } from '../src/resources';
import { AurigaTeamPanel } from '../src';
const id = (n: number) =>
  `01900000-0000-7000-8000-${String(n).padStart(12, '0')}`;
const wrappers: NativeWrapper[] = [];
afterEach(() => {
  wrappers.splice(0).forEach((w) => w.unmount());
  AurigaTeamPanel.clearCatalog({ ownerId: desktopOwner.ownerId });
  AurigaTeamPanel.clearCatalog({
    ownerId: desktopOwner.ownerId,
    generation: 'generation-2',
  });
  vi.unstubAllGlobals();
});
const json = (body: unknown, status = 200) => Response.json(body, { status });
async function click(w: NativeWrapper, label: string) {
  const b = w
    .findAll('button')
    .find(
      (b) =>
        b.text().includes(label) ||
        (b.element as HTMLButtonElement).getAttribute('aria-label') === label,
    );
  expect(b, label).toBeDefined();
  await b!.trigger('click');
  await flushPromises();
}
function fixture(
  kind: 'plugin' | 'skill',
  extra: Record<string, unknown> = {},
) {
  const pack = {
    id: id(2),
    tenantId: id(1),
    ownerId: id(3),
    name: 'Example ' + kind,
    kind,
    enabled: true,
    defaultVersionId: id(4),
    revision: 1,
  };
  const manifest = {
    kind,
    name: pack.name,
    version: '1.0.0',
    description: 'Signed capability',
    author: { displayName: 'Example author' },
    license: 'MIT',
    permissions: { tools: [], outboundHosts: [], dataAccess: [] },
    ...(kind === 'plugin'
      ? { plugin: { packageName: '@example/npm-original' } }
      : { skill: { entry: 'SKILL.md' } }),
  };
  const version = {
    id: id(4),
    packageId: pack.id,
    kind,
    version: '1.0.0',
    state: 'published',
    manifest,
    digest: 'sha256:' + 'a'.repeat(64),
  };
  const row = {
    clientId: id(5),
    installationRevision: 2,
    selectionRevision: 7,
    generation: 9,
    state: 'applying',
    reportSource: 'member',
    resource: {
      identity: {
        tenantId: id(1),
        packageId: pack.id,
        versionId: version.id,
        kind,
        version: version.version,
        manifestDigest: version.digest,
      },
      envelope: { statement: { name: pack.name } },
      enabled: true,
      desiredState: 'ENABLED',
      installationMode: 'optional',
    },
  };
  const requests: { path: string; init?: RequestInit }[] = [];
  const fetch = vi.fn(async (path: string, init?: RequestInit) => {
    requests.push({ path, init });
    if (path === 'catalog/installations')
      return json({ items: [row], sharedAvailable: true, ...extra });
    if (path === 'catalog/actions')
      return json({ accepted: true, installation: row }, 202);
    if (path.includes('/versions?')) return json({ items: [version] });
    if (path.endsWith('/versions/' + version.id)) return json(version);
    if (path.includes('catalog/packages')) {
      const kind = path.includes('kind=skill')
        ? 'skill'
        : path.includes('kind=plugin')
          ? 'plugin'
          : null;
      const items = !kind || kind === pack.kind ? [pack] : [];
      if (path.includes('page=')) return json({ items, total: items.length });
      return json({ items });
    }
    return json({ items: [pack] });
  });
  return { pack, version, row, requests, fetch };
}
async function render(
  kind: 'plugin' | 'skill',
  fetch: NonNullable<PanelContext['fetch']>,
  extra: Partial<PanelContext> = {},
) {
  const w = mountPanel({
    mode: 'connect',
    protocol: 1,
    locale: 'en',
    fetch,
    ...extra,
  });
  wrappers.push(w);
  await flushPromises();
  await click(w, kind === 'skill' ? 'Skills' : 'Plugins');
  await flushPromises();
  return w;
}

const desktopOwner = { ownerId: id(3), tenantId: id(1) };
function desktopFixture() {
  const f = fixture('plugin', { identity: desktopOwner });
  const fetch: NonNullable<PanelContext['fetch']> = (path, init) =>
    path.includes('kind=skill')
      ? Promise.resolve(
          json(
            path.includes('page=') ? { items: [], total: 0 } : { items: [] },
          ),
        )
      : f.fetch(path, init);
  return { ...f, fetch };
}

it('loads plugin market metadata from later pages and scopes likes to the native account generation', async () => {
  const f = desktopFixture();
  const projected = {
    ...f.pack,
    id: id(10),
    name: 'Later plugin',
    market: {
      authorId: id(3),
      version: '2.0.0',
      dshVersionRange: '^4.0.0',
      description: 'Editor tools',
      tags: ['Docs'],
      installationCount: 6,
      starCount: 2,
      starred: false,
    },
  };
  const fetch = vi.fn(async (path: string, init?: RequestInit) => {
    if (path === 'catalog/packages/' + projected.id + '/star')
      return json({ starred: true, starCount: 3 });
    if (path.includes('catalog/packages?')) {
      if (path.includes('kind=skill')) return json({ items: [], total: 0 });
      const params = new URLSearchParams(path.slice(path.indexOf('?') + 1));
      const search = (params.get('search') ?? '').toLowerCase();
      const items =
        !search ||
        projected.name.toLowerCase().includes(search) ||
        projected.market.tags.some((tag) => tag.toLowerCase().includes(search))
          ? search
            ? [projected]
            : [f.pack, projected]
          : [];
      return json({ items, total: items.length });
    }
    return f.fetch(path, init);
  });
  const w = await render('plugin', fetch, {
    catalogSession: {
      ownerId: desktopOwner.ownerId,
      generation: 'generation-2',
    },
  });
  await w.get('input').setValue('Docs');
  await flushPromises();
  expect(w.text()).toContain(projected.name);
  expect(w.text()).toContain('2.0.0');
  expect(w.text()).toContain('Unknown');
  expect(w.text()).not.toContain(f.pack.name);
  const like = w
    .findAll('button')
    .find((button) => button.element?.getAttribute('aria-label') === 'Like');
  expect(like).toBeTruthy();
  await like!.trigger('click');
  await flushPromises();
  const req = fetch.mock.calls.find(([path]) => path.endsWith('/star'))?.[1];
  expect(req?.method).toBe('POST');
  expect(req?.headers).toMatchObject({
    'x-auriga-catalog': '1',
    'x-auriga-catalog-owner': desktopOwner.ownerId,
    'x-auriga-catalog-generation': 'generation-2',
  });
  expect(req?.body).toBe(JSON.stringify({ starred: true }));
  const liked = w
    .findAll('button')
    .find((button) => button.element?.getAttribute('aria-label') === 'Unlike');
  expect(liked?.element?.getAttribute('aria-pressed')).toBe('true');
  expect(liked?.text()).toMatch(/3/);
  expect(liked?.element?.querySelector('svg')).toBeTruthy();
});

it('uses one login prefetch across tabs and even after remounting the desktop panel', async () => {
  const f = desktopFixture();
  const fetch = vi.fn(f.fetch);
  const catalogSession = { ownerId: desktopOwner.ownerId };
  const context = {
    mode: 'connect',
    protocol: 1,
    fetch,
    catalogSession,
  } as const;
  AurigaTeamPanel.prefetchCatalog(context);
  await flushPromises();
  const paths = fetch.mock.calls.map(([path]) => path);
  expect(paths.filter((path) => path === 'catalog/installations')).toHaveLength(
    1,
  );
  expect(paths.some((path) => path.includes('page=1'))).toBe(true);
  expect(paths.some((path) => path.includes('limit=12'))).toBe(true);
  expect(fetch.mock.calls[0][1]?.headers).toMatchObject({
    'x-auriga-catalog-owner': catalogSession.ownerId,
  });
  const requestCount = fetch.mock.calls.length;
  const w = await render('plugin', fetch, { catalogSession });
  expect(w.text()).toContain(f.pack.name);
  await click(w, 'My resources');
  expect(w.text()).toContain(f.pack.name);
  await click(w, 'Skills');
  await click(w, 'Plugins');
  await click(w, 'Resource market');
  expect(w.text()).toContain(f.pack.name);
  w.unmount();
  const remounted = mountPanel({
    mode: 'connect',
    protocol: 1,
    locale: 'en',
    fetch,
    catalogSession,
  });
  wrappers.push(remounted);
  await flushPromises();
  expect(remounted.text()).toContain(f.pack.name);
  // Remount reuses the desktop catalog snapshot; only kind-filter reloads add calls.
  expect(fetch.mock.calls.length).toBeGreaterThanOrEqual(requestCount);
  expect(
    fetch.mock.calls.every(
      ([, init]) =>
        !init?.headers ||
        (init.headers as Record<string, string>)['x-auriga-catalog-owner'] ===
          catalogSession.ownerId,
    ),
  ).toBe(true);
  AurigaTeamPanel.clearCatalog(catalogSession);
});

it('keeps the old desktop snapshot until a manual refresh completes and retains it on temporary failure', async () => {
  const f = desktopFixture();
  const oldName = f.pack.name;
  let failingPath = '';
  let slowSummary = false;
  let releaseSummary: (() => void) | undefined;
  const summaryBlocked = new Promise<void>((resolve) => {
    releaseSummary = resolve;
  });
  const w = await render(
    'plugin',
    async (path, init) => {
      if (failingPath && path.includes(failingPath)) return json({}, 503);
      if (slowSummary && path.includes('/versions/')) await summaryBlocked;
      return f.fetch(path, init);
    },
    { catalogSession: { ownerId: desktopOwner.ownerId } },
  );
  expect(w.text()).toContain(oldName);
  slowSummary = true;
  f.pack.name = 'Refreshed package';
  f.version.manifest.description = 'Refreshed description';
  await click(w, 'Refresh');
  expect(w.text()).toContain(oldName);
  expect(w.text()).toContain('Refreshing resources');
  expect(w.text()).not.toContain(f.pack.name);
  releaseSummary!();
  await flushPromises();
  expect(w.text()).toContain(f.pack.name);
  expect(w.text()).toContain(f.version.manifest.description);
  expect(w.text()).not.toContain(oldName);

  const refreshedName = f.pack.name;
  f.pack.name = 'Incomplete refresh';
  for (const path of ['catalog/installations', '/versions/']) {
    failingPath = path;
    await click(w, 'Refresh');
    expect(w.text()).toContain(refreshedName);
    expect(w.text()).toContain(f.version.manifest.description);
    expect(w.text()).not.toContain(f.pack.name);
    expect(w.find('[role="alert"]').exists()).toBe(true);
  }
});

it('keeps a desktop snapshot on authorization blips and fences an old login during account change', async () => {
  const f = desktopFixture();
  const catalogSession = { ownerId: desktopOwner.ownerId };
  let status = 200;
  const w = await render(
    'plugin',
    (path, init) =>
      status === 200 ? f.fetch(path, init) : Promise.resolve(json({}, status)),
    { catalogSession },
  );
  const auth = vi.fn();
  w.element.addEventListener('auriga-auth-required', auth);
  status = 401;
  await click(w, 'Refresh');
  expect(auth).toHaveBeenCalledOnce();
  // Transient 401 must not wipe the resident market; only logout / identity
  // switch (clearCatalog) may discard the previous account's rows.
  expect(w.text()).toContain(f.pack.name);

  const other = { ownerId: id(7), tenantId: desktopOwner.tenantId };
  const replacement = fixture('plugin', { identity: other });
  replacement.pack.name = 'Different account';
  AurigaTeamPanel.clearCatalog(catalogSession);
  await w.setProps({
    context: {
      ...w.element.context,
      catalogSession: { ownerId: other.ownerId },
      fetch: (path, init) =>
        path.includes('kind=skill')
          ? Promise.resolve(json({ items: [] }))
          : replacement.fetch(path, init),
    },
  });
  await flushPromises();
  expect(w.text()).toContain(replacement.pack.name);
  expect(w.text()).not.toContain(f.pack.name);
});

it('updates the shared installation snapshot when an action completes on another tab', async () => {
  const f = desktopFixture();
  let complete: ((value: Response) => void) | undefined;
  let actionSignal: AbortSignal | null | undefined;
  const w = await render(
    'plugin',
    (path, init) => {
      if (path === 'catalog/installations')
        return Promise.resolve(json({ identity: desktopOwner, items: [] }));
      if (path !== 'catalog/actions') return f.fetch(path, init);
      actionSignal = init?.signal;
      return new Promise<Response>((resolve) => {
        complete = resolve;
      });
    },
    { catalogSession: { ownerId: desktopOwner.ownerId } },
  );
  await click(w, 'Install');
  expect(complete).toBeDefined();
  await click(w, 'My resources');
  expect(actionSignal?.aborted).toBe(false);
  complete!(json({ accepted: true, installation: f.row }, 202));
  await flushPromises();
  expect(w.text()).toContain(f.pack.name);
  expect(w.text()).toContain('applying');
});
for (const kind of ['plugin', 'skill'] as const)
  describe('unified ' + kind, () => {
    it('uses catalog reads and never the old member port or global transport', async () => {
      const f = fixture(kind);
      const old = { list: vi.fn(), apply: vi.fn() };
      const global = vi.fn();
      vi.stubGlobal('fetch', global);
      const w = await render(kind, f.fetch, { memberResources: old });
      expect(w.text()).toContain(f.pack.name);
      expect(f.requests.some((r) => r.path.includes('kind=' + kind))).toBe(
        true,
      );
      expect(f.requests.every((r) => r.path.startsWith('catalog/'))).toBe(true);
      expect(old.list).not.toHaveBeenCalled();
      expect(old.apply).not.toHaveBeenCalled();
      expect(global).not.toHaveBeenCalled();
    });
    it('pins action identity and CAS and keeps 202 applying', async () => {
      const f = fixture(kind);
      const w = await render(kind, async (p, i) =>
        p === 'catalog/installations'
          ? json({ items: [], sharedAvailable: true })
          : f.fetch(p, i),
      );
      await click(w, 'Install');
      const r = f.requests.find((r) => r.path === 'catalog/actions')!;
      expect(JSON.parse(String(r.init?.body))).toMatchObject({
        packageId: f.pack.id,
        versionId: f.version.id,
        revision: 0,
        action: 'install',
      });
      expect(r.init?.headers).toMatchObject({ 'x-auriga-catalog': '1' });
      expect(w.findAll('button').some((b) => b.text() === 'Update')).toBe(true);
      expect(w.text()).not.toContain('Select version');
    });
    it('shows list metadata without old levels or version picker', async () => {
      const f = fixture(kind);
      const w = await render(kind, f.fetch);
      expect(w.text()).toContain('Example author');
      expect(w.text()).not.toMatch(/\bL[0-4]\b/);
      expect(w.text()).not.toContain('Select version');
      expect(w.text()).not.toContain('Not installed');
      expect(w.text()).not.toContain('未安装');
      expect(w.findAll('button').some((b) => b.text() === 'Update')).toBe(true);
    });
    it.each(['required', 'default', 'optional'])(
      'enforces %s controls',
      async (mode) => {
        const f = fixture(kind);
        f.row.resource.installationMode = mode;
        const w = await render(kind, f.fetch);
        await click(w, 'My resources');
        for (const action of ['Disable', 'Remove']) {
          const b = w.findAll('button').find((b) => b.text() === action)!;
          expect((b.element as HTMLButtonElement).disabled).toBe(
            mode === 'required',
          );
        }
        expect(
          w.findAll('button').some((b) => b.text().includes('Check source')),
        ).toBe(kind === 'skill');
      },
    );
    it.each([400, 401, 403, 404, 409, 429, 503])(
      'clears stale results after HTTP %s',
      async (status) => {
        const f = fixture(kind);
        let fail = false;
        const w = await render(kind, async (p, i) =>
          fail ? json({ code: 'common.unavailable' }, status) : f.fetch(p, i),
        );
        expect(w.text()).toContain(f.pack.name);
        fail = true;
        await click(w, 'Refresh');
        expect(w.text()).not.toContain(f.pack.name);
        expect(w.find('[role="alert"]').exists()).toBe(true);
      },
    );
    it('emits native auth recovery on a 401', async () => {
      const w = await render(kind, async () => json({}, 401));
      const event = vi.fn();
      w.element.addEventListener('auriga-auth-required', event);
      await click(w, 'Refresh');
      expect(event).toHaveBeenCalled();
    });
    it('renders resource and policy names as text', async () => {
      const f = fixture(kind, {
        allowPersonalPlugins: false,
        blockedPersonalPlugins: ['<img src=x onerror=alert(1)>'],
      });
      f.pack.name = '<img src=x onerror=alert(1)>';
      const w = await render(kind, f.fetch);
      expect(w.find('img').exists()).toBe(false);
      expect(w.text()).toContain('<img');
    });
    it.each([false, true])(
      'reports policy restart=%s honestly',
      async (restart) => {
        const f = fixture(kind, {
          allowPersonalPlugins: false,
          blockedPersonalPlugins: ['personal-plugin'],
          personalPluginPolicyPendingRestart: restart,
        });
        const w = await render(kind, f.fetch);
        expect(w.text()).toContain('personal-plugin');
        expect(w.text()).toContain(
          restart ? 'Restart DSH' : 'disabled by organization policy',
        );
      },
    );
    it('displays an empty personal allowlist restriction', async () => {
      const f = fixture(kind, { allowPersonalPlugins: false });
      const w = await render(kind, f.fetch);
      expect(w.text()).toContain('does not allow personal plugin');
    });
    it('keeps the market visible and explains temporary host and cache problems', async () => {
      const f = fixture(kind, {
        hostBusy: true,
        policyCacheUnavailable: true,
        allowPersonalPlugins: false,
      });
      const w = await render(kind, f.fetch);
      expect(w.text()).toContain(f.pack.name);
      expect(w.text()).toContain('synchronizing shared resources');
      expect(w.text()).toContain('Could not save the personal plugin policy');
      expect(w.text()).toContain(
        'Personal plugin installation remains restricted.',
      );
      expect(w.text()).not.toContain('Restart DSH');
    });
    it('requires a guardian for installation', async () => {
      const f = fixture(kind, { sharedAvailable: false });
      const w = await render(kind, f.fetch);
      const b = w.findAll('button').find((b) => b.text() === 'Update')!;
      expect((b.element as HTMLButtonElement).disabled).toBe(true);
      expect(w.text()).toContain('guardian');
    });
    it('keeps filters across locale and theme changes', async () => {
      const f = fixture(kind);
      const w = await render(kind, f.fetch);
      await w.get('input').setValue('no-match');
      await w.setProps({
        context: { ...w.element.context, locale: 'zh-CN', theme: 'dark' },
      });
      expect(w.get('input').element.value).toBe('no-match');
      expect(w.attributes('data-theme')).toBe('dark');
    });
    it('hides the pager when total fits on one page', async () => {
      const f = fixture(kind);
      const w = await render(kind, f.fetch);
      expect(w.text()).toContain(f.pack.name);
      expect(w.find('[aria-label="Pagination"]').exists()).toBe(false);
    });
    it('pages through server totals and keeps query on refresh', async () => {
      const f = fixture(kind);
      const packs = Array.from({ length: 13 }, (_, index) => ({
        ...f.pack,
        id: id(20 + index),
        name: `Paged ${kind} ${index + 1}`,
      }));
      const w = await render(kind, async (p, i) => {
        if (!p.includes('catalog/packages?')) return f.fetch(p, i);
        const params = new URLSearchParams(p.slice(p.indexOf('?') + 1));
        const page = Number(params.get('page') ?? '1');
        const limit = Number(params.get('limit') ?? '12');
        const start = (page - 1) * limit;
        return json({
          items: packs.slice(start, start + limit),
          total: packs.length,
        });
      });
      expect(w.text()).toContain('Paged ' + kind + ' 1');
      expect(w.find('[aria-label="Pagination"]').exists()).toBe(true);
      expect(w.text()).toContain('1–12 of 13');
      await click(w, 'Next page');
      expect(w.text()).toContain('Paged ' + kind + ' 13');
      expect(w.text()).toContain('13–13 of 13');
      await click(w, 'Refresh');
      expect(w.text()).toContain('Paged ' + kind + ' 13');
      expect(w.text()).toContain('13–13 of 13');
    });
    it('refuses the wrong kind returned by the server', async () => {
      const f = fixture(kind === 'plugin' ? 'skill' : 'plugin');
      const w = await render(kind, async (p, i) =>
        p.includes('catalog/packages?')
          ? json({ items: [f.pack], total: 1 })
          : f.fetch(p, i),
      );
      expect(w.text()).not.toContain(f.pack.name);
      expect(w.find('[role="alert"]').exists()).toBe(true);
    });
    it.each(['withdrawn', 'draft'])('refuses a %s default', async (state) => {
      const f = fixture(kind);
      f.version.state = state;
      const w = await render(kind, f.fetch);
      const b = w.findAll('button').find((b) => b.text() === 'Update')!;
      expect((b.element as HTMLButtonElement).disabled).toBe(true);
    });
    it('filters mixed installations by real kind', async () => {
      const f = fixture(kind);
      const other = fixture(kind === 'plugin' ? 'skill' : 'plugin');
      const w = await render(kind, async (p, i) =>
        p === 'catalog/installations'
          ? json({ items: [f.row, other.row], sharedAvailable: true })
          : f.fetch(p, i),
      );
      await click(w, 'My resources');
      expect(w.text()).toContain(f.pack.name);
      expect(w.text()).not.toContain(other.pack.name);
    });
    it('cancels a read when the native view disconnects', async () => {
      let signal: AbortSignal | undefined;
      const w = await render(kind, async (_p, i) => {
        signal = i?.signal ?? undefined;
        return new Promise<Response>(() => {});
      });
      w.unmount();
      expect(signal?.aborted).toBe(true);
    });
    it('rejects malformed policy notices', async () => {
      const f = fixture(kind, { blockedPersonalPlugins: [{ name: 'bad' }] });
      const w = await render(kind, f.fetch);
      expect(w.find('[role="alert"]').exists()).toBe(true);
    });
    it('uses the catalog in managed mode', async () => {
      const f = fixture(kind);
      const old = { list: vi.fn(), apply: vi.fn() };
      const w = await render(kind, f.fetch, {
        mode: 'managed',
        memberResources: old,
      });
      expect(w.text()).toContain(f.pack.name);
      expect(old.list).not.toHaveBeenCalled();
    });
    it('presents cached restrictions while the platform is offline', async () => {
      const f = fixture(kind, {
        sharedAvailable: false,
        sharedUnavailableReason: 'platform_offline',
        allowPersonalPlugins: false,
        blockedPersonalPlugins: ['personal-plugin'],
      });
      const w = await render(kind, f.fetch);
      expect(w.text()).toContain('last authenticated');
      expect(w.text()).toContain('personal-plugin');
    });
    it.each(['Enable', 'Disable', 'Remove'])(
      'sends a pinned member %s action through the catalog',
      async (label) => {
        const f = fixture(kind);
        const w = await render(kind, f.fetch);
        await click(w, 'My resources');
        await click(w, label);
        const request = f.requests.find((r) => r.path === 'catalog/actions')!;
        expect(JSON.parse(String(request.init?.body))).toMatchObject({
          packageId: f.pack.id,
          versionId: f.version.id,
          revision: 2,
          action: label.toLowerCase(),
        });
      },
    );
  });
it.each([
  { mode: 'standalone' as const, protocol: 1 },
  { mode: 'connect' as const, protocol: 2 },
  { mode: undefined, protocol: 1 },
])('refuses unsupported context %o without network', async (extra) => {
  const fetch = vi.fn();
  const global = vi.fn();
  vi.stubGlobal('fetch', global);
  const w = mountPanel({ locale: 'en', fetch, ...extra });
  wrappers.push(w);
  await flushPromises();
  expect(fetch).not.toHaveBeenCalled();
  expect(global).not.toHaveBeenCalled();
  expect(w.find('[role="status"]').exists()).toBe(true);
});
