import { afterEach, describe, expect, it, vi } from 'vitest';
import { mountPanel, flushPromises, type NativeWrapper } from './native-host';
import type { PanelContext } from '../src/resources';
import { AurigaTeamPanel } from '../src';
const id = (n: number) =>
  `01900000-0000-7000-8000-${String(n).padStart(12, '0')}`;
const wrappers: NativeWrapper[] = [];
afterEach(() => {
  wrappers.splice(0).forEach((w) => w.unmount());
  vi.unstubAllGlobals();
});
const json = (body: unknown, status = 200) => Response.json(body, { status });
async function click(w: NativeWrapper, label: string) {
  const b = w.findAll('button').find((b) => b.text().includes(label));
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
  if (kind === 'skill') await click(w, 'Skills');
  await flushPromises();
  return w;
}

const desktopOwner = { ownerId: id(3), tenantId: id(1) };
function desktopFixture() {
  const f = fixture('plugin', { identity: desktopOwner });
  const fetch: NonNullable<PanelContext['fetch']> = (path, init) =>
    path.includes('kind=skill')
      ? Promise.resolve(json({ items: [] }))
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
    if (path.includes('kind=plugin'))
      return json(
        path.includes('cursor=next')
          ? { items: [projected] }
          : { items: [f.pack], nextCursor: 'next' },
      );
    return f.fetch(path, init);
  });
  const w = await render('plugin', fetch, {
    catalogSession: {
      ownerId: desktopOwner.ownerId,
      generation: 'generation-2',
    },
  });
  await w.get('input').setValue('Docs');
  expect(w.text()).toContain(projected.name);
  expect(w.text()).toContain('^4.0.0');
  expect(w.text()).toContain('Unknown');
  expect(w.text()).not.toContain(f.pack.name);
  await click(w, 'Like');
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
    .find((button) => button.text() === 'Unlike');
  expect(liked?.element?.getAttribute('aria-pressed')).toBe('true');
  expect(w.text()).toContain('Likes3');
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
  expect(paths.some((path) => path.includes('kind=plugin'))).toBe(true);
  expect(paths.some((path) => path.includes('kind=skill'))).toBe(true);
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
  const remounted = await render('plugin', fetch, { catalogSession });
  expect(remounted.text()).toContain(f.pack.name);
  expect(fetch.mock.calls).toHaveLength(requestCount);
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

it('clears a desktop snapshot on authorization failure and fences an old login during account change', async () => {
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
  expect(w.text()).not.toContain(f.pack.name);

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
  await click(w, 'Install 1.0.0');
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
      const w = await render(kind, f.fetch);
      await click(w, 'Install 1.0.0');
      const r = f.requests.find((r) => r.path === 'catalog/actions')!;
      expect(JSON.parse(String(r.init?.body))).toMatchObject({
        packageId: f.pack.id,
        versionId: f.version.id,
        revision: 2,
        action: 'install',
      });
      expect(r.init?.headers).toMatchObject({ 'x-auriga-catalog': '1' });
      expect(w.text()).toContain('applying');
      expect(w.text()).not.toContain('applied');
    });
    it('shows manifest metadata and npm identity without old levels', async () => {
      const f = fixture(kind);
      const w = await render(kind, f.fetch);
      await click(w, 'Select version');
      expect(w.text()).toContain('Example author');
      expect(w.text()).toContain('MIT');
      if (kind === 'plugin')
        expect(w.text()).toContain('@example/npm-original');
      expect(w.text()).not.toMatch(/\bL[0-4]\b/);
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
      const b = w.findAll('button').find((b) => b.text() === 'Install 1.0.0')!;
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
    it('keeps prior packages after an empty final page', async () => {
      const f = fixture(kind);
      const w = await render(kind, async (p, i) =>
        p.includes('catalog/packages?')
          ? json(
              p.includes('cursor=next')
                ? { items: [], nextCursor: '' }
                : { items: [f.pack], nextCursor: 'next' },
            )
          : f.fetch(p, i),
      );
      if (kind === 'skill') await click(w, 'Load more');
      expect(w.text()).toContain(f.pack.name);
      expect(w.findAll('button').some((b) => b.text() === 'Load more')).toBe(
        false,
      );
    });
    it('refuses repeated page cursors', async () => {
      const f = fixture(kind);
      const w = await render(kind, async (p, i) =>
        p.includes('catalog/packages?')
          ? json({
              items: p.includes('cursor=next') ? [] : [f.pack],
              nextCursor: 'next',
            })
          : f.fetch(p, i),
      );
      if (kind === 'skill') await click(w, 'Load more');
      expect(w.find('[role="alert"]').exists()).toBe(true);
      if (kind === 'plugin') expect(w.text()).not.toContain(f.pack.name);
    });
    it('refuses the wrong kind returned by the server', async () => {
      const f = fixture(kind === 'plugin' ? 'skill' : 'plugin');
      const w = await render(kind, f.fetch);
      expect(w.text()).not.toContain(f.pack.name);
      expect(w.find('[role="alert"]').exists()).toBe(true);
    });
    it.each(['withdrawn', 'draft'])('refuses a %s default', async (state) => {
      const f = fixture(kind);
      f.version.state = state;
      const w = await render(kind, f.fetch);
      const b = w.findAll('button').find((b) => b.text() === 'Install 1.0.0')!;
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
    it('paginates exact versions while retaining earlier choices', async () => {
      const f = fixture(kind);
      const w = await render(kind, async (p, i) =>
        p.includes('/versions?')
          ? json(
              p.includes('cursor=version-next')
                ? {
                    items: [{ ...f.version, id: id(6), version: '2.0.0' }],
                    nextCursor: '',
                  }
                : { items: [f.version], nextCursor: 'version-next' },
            )
          : f.fetch(p, i),
      );
      await click(w, 'Select version');
      await click(w, 'More versions');
      expect(w.text()).toContain('1.0.0');
      expect(w.text()).toContain('2.0.0');
      expect(
        w.findAll('button').some((b) => b.text() === 'More versions'),
      ).toBe(false);
    });
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
