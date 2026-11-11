import { afterEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mountPanel, type NativeWrapper } from './native-host';
import {
  createResourceReader,
  type PanelContext,
  type ResourcePackage,
  type ResourceVersion,
} from '../src/resources';

// Contract fixtures are confined to component tests; no backend integration is claimed.
const packageId = '01900000-0000-7000-8000-000000000001';
const versionId = '01900000-0000-7000-8000-000000000002';
const plugin: ResourcePackage = {
  id: packageId,
  tenantId: '01900000-0000-7000-8000-000000000003',
  ownerId: '01900000-0000-7000-8000-000000000004',
  name: 'Example plugin',
  kind: 'plugin',
  level: 'l2',
  visibility: 'tenant',
  groupIds: [],
  revision: 1,
  createdAt: '2026-10-23T00:00:00Z',
  enabled: true,
  defaultVersionId: versionId,
};
const version: ResourceVersion = {
  id: versionId,
  packageId,
  version: '1.0.0',
  digest: `sha256:${'a'.repeat(64)}`,
  sizeBytes: 100,
  mediaType: 'application/gzip',
  state: 'published',
  revision: 1,
  createdAt: '2026-10-23T00:00:00Z',
};
function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'content-type': 'application/json' },
  });
}
function connect(fetch: NonNullable<PanelContext['fetch']>): PanelContext {
  return { mode: 'connect', protocol: 1, locale: 'en', theme: 'light', fetch };
}
let wrappers: NativeWrapper[] = [];
function render(context?: PanelContext) {
  const wrapper = mountPanel(context);
  wrappers.push(wrapper);
  return wrapper;
}
async function click(wrapper: NativeWrapper, label: string) {
  const button = wrapper
    .findAll('button')
    .find((item) => item.text().includes(label));
  expect(button, label).toBeDefined();
  await button!.trigger('click');
  await flushPromises();
}
afterEach(() => {
  wrappers.forEach((wrapper) => wrapper.unmount());
  wrappers = [];
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe('resource boundary', () => {
  it.each(['standalone', undefined] as const)(
    'never calls host or global fetch in %s mode',
    async (mode) => {
      const host = vi.fn();
      const global = vi.fn();
      vi.stubGlobal('fetch', global);
      const wrapper = render({ ...connect(host), mode });
      await flushPromises();
      expect(host).not.toHaveBeenCalled();
      expect(global).not.toHaveBeenCalled();
      expect(wrapper.find('[role="status"]').exists()).toBe(true);
      expect(() => createResourceReader({ ...connect(host), mode })).toThrow(
        'unavailable',
      );
    },
  );
  it('rejects unsupported protocols and missing transport without a global fallback', async () => {
    const host = vi.fn();
    const wrapper = render({ ...connect(host), protocol: 2 });
    expect(wrapper.text()).toContain('protocol is unsupported');
    await wrapper.setProps({
      context: {
        mode: 'connect',
        protocol: 1,
        locale: 'en',
        apiBase: 'https://example.com',
      },
    });
    expect(wrapper.text()).toContain('No resource connection');
    expect(host).not.toHaveBeenCalled();
  });
  it.each([
    [401, 'session has expired'],
    [403, 'cannot read'],
    [404, 'does not exist'],
    [429, 'Too many requests'],
    [503, 'unreachable'],
  ])(
    'shows HTTP %s as failure, never as an empty market',
    async (status, message) => {
      const host = vi
        .fn()
        .mockResolvedValue(
          json({ detail: 'Sensitive server detail' }, status as number),
        );
      const wrapper = render(connect(host));
      await flushPromises();
      expect(wrapper.get('[role="alert"]').text()).toContain(message);
      expect(wrapper.text()).not.toContain('No plugins are visible');
      expect(wrapper.text()).not.toContain('Sensitive server detail');
      expect(host).toHaveBeenCalledTimes(1);
    },
  );
  it.each([
    {},
    { items: [{ ...plugin, enabled: 'yes' }] },
    { items: [{ ...plugin, kind: 'skill' }] },
    { items: [plugin], nextCursor: 23 },
    { items: [plugin], nextCursor: null },
  ])('rejects malformed success payloads %#', async (body) => {
    const wrapper = render(connect(vi.fn().mockResolvedValue(json(body))));
    await flushPromises();
    expect(wrapper.get('[role="alert"]').text()).toContain('unrecognized data');
    expect(wrapper.findAll('.card')).toHaveLength(0);
  });
  it('rejects HTML fallback and invalid JSON', async () => {
    const host = vi
      .fn()
      .mockResolvedValueOnce(
        new Response('<html>login</html>', {
          headers: { 'content-type': 'text/html' },
        }),
      )
      .mockResolvedValueOnce(
        new Response('{', { headers: { 'content-type': 'application/json' } }),
      );
    const wrapper = render(connect(host));
    await flushPromises();
    expect(wrapper.get('[role="alert"]').text()).toContain('unrecognized data');
    await click(wrapper, 'Retry');
    expect(wrapper.get('[role="alert"]').text()).toContain('unrecognized data');
  });
  it('rejects path traversal and mismatched package or version identities', async () => {
    const host = vi
      .fn()
      .mockResolvedValueOnce(json({ ...plugin, id: versionId }))
      .mockResolvedValueOnce(
        json({ items: [{ ...version, packageId: versionId }] }),
      );
    const reader = createResourceReader(connect(host));
    const signal = new AbortController().signal;
    await expect(reader.detail('../admin', signal)).rejects.toThrow('invalid');
    expect(host).not.toHaveBeenCalled();
    await expect(reader.detail(packageId, signal)).rejects.toThrow('invalid');
    await expect(reader.versions(packageId, signal)).rejects.toThrow('invalid');
  });
});

describe('market reading', () => {
  it.each([false, true])(
    'accepts a terminal package page with an empty cursor (has items: %s)',
    async (hasItems) => {
      const items = hasItems ? [plugin] : [];
      const reader = createResourceReader(
        connect(vi.fn().mockResolvedValue(json({ items, nextCursor: '' }))),
      );
      await expect(reader.list(new AbortController().signal)).resolves.toEqual({
        items,
        nextCursor: undefined,
      });
    },
  );
  it.each([false, true])(
    'accepts a terminal version page with an empty cursor (has items: %s)',
    async (hasItems) => {
      const items = hasItems ? [version] : [];
      const reader = createResourceReader(
        connect(vi.fn().mockResolvedValue(json({ items, nextCursor: '' }))),
      );
      await expect(
        reader.versions(packageId, new AbortController().signal),
      ).resolves.toEqual({ items, nextCursor: undefined });
    },
  );
  it('preserves loaded packages when the final page is empty and has an empty cursor', async () => {
    const host = vi
      .fn()
      .mockResolvedValueOnce(json({ items: [plugin], nextCursor: 'next' }))
      .mockResolvedValueOnce(json({ items: [], nextCursor: '' }));
    const wrapper = render(connect(host));
    await flushPromises();
    await click(wrapper, 'Load more');
    expect(wrapper.find('[role="alert"]').exists()).toBe(false);
    expect(wrapper.findAll('.card')).toHaveLength(1);
    expect(wrapper.text()).toContain(plugin.name);
    expect(wrapper.text()).not.toContain('Load more');
    expect(host).toHaveBeenCalledTimes(2);
  });
  it('preserves loaded versions when the final page is empty and has an empty cursor', async () => {
    const host = vi
      .fn()
      .mockResolvedValueOnce(json({ items: [plugin] }))
      .mockResolvedValueOnce(json(plugin))
      .mockResolvedValueOnce(json({ items: [version], nextCursor: 'next' }))
      .mockResolvedValueOnce(json({ items: [], nextCursor: '' }));
    const wrapper = render(connect(host));
    await flushPromises();
    await click(wrapper, 'View details');
    await click(wrapper, 'Load more');
    expect(wrapper.find('[role="alert"]').exists()).toBe(false);
    expect(wrapper.findAll('.versions li')).toHaveLength(1);
    expect(wrapper.get('.detail').text()).toContain(version.version);
    expect(wrapper.text()).not.toContain('Load more');
    expect(host).toHaveBeenCalledTimes(4);
  });
  it('reads actual response fields, filters locally, paginates and opens version detail', async () => {
    const host = vi
      .fn()
      .mockResolvedValueOnce(
        json({ items: [plugin], nextCursor: 'page+2&private=no' }),
      )
      .mockResolvedValueOnce(
        json({ items: [{ ...plugin, id: versionId, name: 'Another plugin' }] }),
      )
      .mockResolvedValueOnce(json(plugin))
      .mockResolvedValueOnce(
        json({ items: [version], nextCursor: 'versions:2' }),
      )
      .mockResolvedValueOnce(
        json({ items: [{ ...version, id: packageId, version: '1.1.0' }] }),
      );
    const wrapper = render(connect(host));
    await flushPromises();
    expect(wrapper.findAll('.card')).toHaveLength(1);
    expect(wrapper.text()).toContain('Headquarters availability · Available');
    expect(wrapper.text()).toContain('Local installation · Unknown');
    await click(wrapper, 'Load more');
    expect(host.mock.calls[1][0]).toBe(
      '/api/resource/v1/packages?limit=50&cursor=page%2B2%26private%3Dno',
    );
    expect(wrapper.findAll('.card')).toHaveLength(2);
    await wrapper.get('input').setValue('Example');
    expect(wrapper.findAll('.card')).toHaveLength(1);
    expect(host).toHaveBeenCalledTimes(2);
    await click(wrapper, 'View details');
    expect(wrapper.get('.detail').text()).toContain('1.0.0');
    expect(wrapper.get('.detail').text()).toContain('Published');
    await click(wrapper, 'Load more');
    expect(wrapper.findAll('.versions li')).toHaveLength(2);
    expect(host.mock.calls[4][0]).toBe(
      `/api/resource/v1/packages/${packageId}/versions?limit=50&cursor=versions%3A2`,
    );
    await click(wrapper, 'Back to market');
    expect(wrapper.get('input').element.value).toBe('Example');
    expect(wrapper.findAll('.card')).toHaveLength(1);
    for (const [, init] of host.mock.calls) {
      expect(init.method).toBe('GET');
      expect(init.redirect).toBe('error');
      expect(init.cache).toBe('no-store');
      expect(init.headers).toEqual({ accept: 'application/json' });
      expect(init).not.toHaveProperty('credentials');
    }
  });
  it('keeps installation unknown even when the market is empty', async () => {
    const host = vi.fn().mockResolvedValue(json({ items: [] }));
    const wrapper = render(connect(host));
    await flushPromises();
    expect(wrapper.text()).toContain('No plugins are visible');
    await click(wrapper, 'My resources');
    expect(wrapper.text()).toContain(
      'local installation inventory is unavailable',
    );
    expect(wrapper.find('input').exists()).toBe(false);
    expect(host).toHaveBeenCalledTimes(1);
  });
  it('escapes package names instead of executing resource content', async () => {
    const wrapper = render(
      connect(
        vi.fn().mockResolvedValue(
          json({
            items: [{ ...plugin, name: '<img src=x onerror=alert(1)>' }],
          }),
        ),
      ),
    );
    await flushPromises();
    expect(wrapper.text()).toContain('<img src=x onerror=alert(1)>');
    expect(wrapper.find('img').exists()).toBe(false);
  });
  it('retries explicitly after a network failure', async () => {
    const host = vi
      .fn()
      .mockRejectedValueOnce(new Error('private connection detail'))
      .mockResolvedValueOnce(json({ items: [plugin] }));
    const wrapper = render(connect(host));
    await flushPromises();
    expect(wrapper.get('[role="alert"]').text()).toContain('unreachable');
    await click(wrapper, 'Retry');
    expect(wrapper.find('[role="alert"]').exists()).toBe(false);
    expect(wrapper.findAll('.card')).toHaveLength(1);
  });
  it('clears previously loaded data and emits a composed auth event on a later 401', async () => {
    const host = vi
      .fn()
      .mockResolvedValueOnce(json({ items: [plugin], nextCursor: 'next' }))
      .mockResolvedValueOnce(json({}, 401));
    const wrapper = render(connect(host));
    await flushPromises();
    const events: Event[] = [];
    wrapper.element.addEventListener('auriga-auth-required', (event: Event) =>
      events.push(event),
    );
    await click(wrapper, 'Load more');
    expect(wrapper.text()).not.toContain(plugin.name);
    expect(events).toHaveLength(1);
    expect(events[0].composed).toBe(true);
    expect(events[0].bubbles).toBe(true);
  });
  it('detects a repeated cursor without fetching in a loop', async () => {
    const host = vi
      .fn()
      .mockImplementation(() =>
        Promise.resolve(json({ items: [plugin], nextCursor: 'same' })),
      );
    const wrapper = render(connect(host));
    await flushPromises();
    await click(wrapper, 'Load more');
    expect(wrapper.get('[role="alert"]').text()).toContain('unrecognized data');
    expect(host).toHaveBeenCalledTimes(2);
  });
  it('does not retain market data as an offline fallback after a later failure', async () => {
    const host = vi
      .fn()
      .mockResolvedValueOnce(json({ items: [plugin], nextCursor: 'next' }))
      .mockResolvedValueOnce(json({}, 503))
      .mockResolvedValueOnce(json({ items: [plugin] }));
    const wrapper = render(connect(host));
    await flushPromises();
    await click(wrapper, 'Load more');
    expect(wrapper.text()).not.toContain(plugin.name);
    expect(wrapper.get('[role="alert"]').text()).toContain('unreachable');
    await click(wrapper, 'Retry');
    expect(host.mock.calls[2][0]).toBe('/api/resource/v1/packages?limit=50');
    expect(wrapper.findAll('.card')).toHaveLength(1);
  });
});

describe('request lifecycle', () => {
  it('aborts and ignores a previous connection response when host context changes', async () => {
    let finish!: (response: Response) => void;
    const oldHost = vi.fn().mockImplementation(
      () =>
        new Promise<Response>((resolve) => {
          finish = resolve;
        }),
    );
    const wrapper = render(connect(oldHost));
    expect(wrapper.text()).toContain('Loading resources');
    const newHost = vi
      .fn()
      .mockResolvedValue(
        json({ items: [{ ...plugin, name: 'New connection' }] }),
      );
    await wrapper.setProps({ context: connect(newHost) });
    await flushPromises();
    finish(json({ items: [plugin] }));
    await flushPromises();
    expect(oldHost.mock.calls[0][1].signal.aborted).toBe(true);
    expect(wrapper.text()).toContain('New connection');
    expect(wrapper.text()).not.toContain('Example plugin');
  });
  it('clears data when the host changes to managed mode', async () => {
    const host = vi
      .fn()
      .mockImplementation(async () => json({ items: [plugin] }));
    const wrapper = render(connect(host));
    await flushPromises();
    await wrapper.setProps({ context: { ...connect(host), mode: 'managed' } });
    expect(wrapper.text()).not.toContain(plugin.name);
    expect(wrapper.text()).toContain('Loading resources');
    await flushPromises();
    expect(wrapper.text()).toContain(plugin.name);
    expect(host).toHaveBeenCalledTimes(2);
  });
  it('ignores a failed detail request after returning to the market', async () => {
    let reject!: (error: Error) => void;
    const host = vi
      .fn()
      .mockResolvedValueOnce(json({ items: [plugin] }))
      .mockImplementationOnce(
        () =>
          new Promise<Response>((_, fail) => {
            reject = fail;
          }),
      )
      .mockResolvedValueOnce(json({ items: [version] }));
    const wrapper = render(connect(host));
    await flushPromises();
    await click(wrapper, 'View details');
    await click(wrapper, 'Back to market');
    reject(new Error('network'));
    await flushPromises();
    expect(wrapper.find('[role="alert"]').exists()).toBe(false);
    expect(wrapper.findAll('.card')).toHaveLength(1);
  });
  it('bounds hung reads and cancels transport on unmount', async () => {
    vi.useFakeTimers();
    const host = vi
      .fn()
      .mockImplementation(() => new Promise<Response>(() => {}));
    const wrapper = render(connect(host));
    await vi.advanceTimersByTimeAsync(15_000);
    expect(wrapper.get('[role="alert"]').text()).toContain('unreachable');
    expect(host.mock.calls[0][1].signal.aborted).toBe(true);
    await click(wrapper, 'Retry');
    wrapper.unmount();
    await flushPromises();
    expect(host.mock.calls[1][1].signal.aborted).toBe(true);
    expect(vi.getTimerCount()).toBe(0);
  });
});

it('reads a managed catalogue through the supplied host channel', async () => {
  const host = vi.fn().mockResolvedValue(json({ items: [plugin] }));
  const wrapper = render({ ...connect(host), mode: 'managed' });
  await flushPromises();
  expect(wrapper.text()).toContain('Example plugin');
  expect(host).toHaveBeenCalledTimes(1);
});
it('shows native inventory while the platform is offline and keeps operations disabled', async () => {
  const host = vi.fn().mockRejectedValue(new Error('offline'));
  const listInstalled = vi.fn().mockResolvedValue({
    entries: [
      {
        entryId: 'example',
        moduleName: 'example-plugin',
        enabled: false,
        fiberPhase: null,
      },
    ],
  });
  const wrapper = render({ ...connect(host), listInstalled });
  await flushPromises();
  await click(wrapper, 'My resources');
  expect(wrapper.text()).toContain('example-plugin');
  expect(wrapper.text()).toContain('Disabled');
  expect(listInstalled).toHaveBeenCalledTimes(1);
  for (const label of ['Install', 'Update', 'Uninstall']) {
    const button = wrapper
      .findAll('button')
      .find((item) => item.text() === label);
    expect(button?.element?.hasAttribute('disabled')).toBe(true);
  }
  expect(host).toHaveBeenCalledTimes(1);
});
it('does not present malformed native inventory as an empty installation', async () => {
  const wrapper = render({
    mode: 'standalone',
    protocol: 1,
    locale: 'en',
    listInstalled: async () => ({ entries: [{ moduleName: '<script>' }] }),
  });
  await click(wrapper, 'My resources');
  expect(wrapper.text()).toContain(
    'local installation inventory is unavailable',
  );
  expect(wrapper.text()).not.toContain('No local plugins');
});

it('keeps healthy global and preset plugins when another public preset is broken', async () => {
  const row = {
    entryId: 'healthy',
    moduleName: 'healthy-plugin',
    enabled: true,
    fiberPhase: 'active',
  };
  const wrapper = render({
    mode: 'standalone',
    protocol: 1,
    locale: 'en',
    listInstalled: async () => ({
      entries: [row],
      agentPresets: [
        {
          id: 'healthy-preset',
          isDefault: true,
          rows: [{ ...row, entryId: null, moduleName: 'preset-plugin' }],
        },
        {
          id: 'broken-preset',
          name: 'Broken preset',
          isDefault: false,
          broken: 'private-secret-error',
          rows: [],
        },
      ],
    }),
  });
  await click(wrapper, 'My resources');
  expect(wrapper.findAll('.card')).toHaveLength(2);
  expect(wrapper.text()).toContain('healthy-plugin');
  expect(wrapper.text()).toContain('preset-plugin');
  expect(wrapper.text()).toContain('Broken preset');
  expect(wrapper.text()).toContain(
    'Preset inventory is unavailable; installation state is unknown.',
  );
  expect(wrapper.text()).not.toContain('private-secret-error');
  expect(wrapper.text()).not.toContain('No local plugins');
});
it('does not turn a broken-only preset into an empty installation', async () => {
  const wrapper = render({
    mode: 'standalone',
    protocol: 1,
    locale: 'en',
    listInstalled: async () => ({
      entries: [],
      agentPresets: [
        {
          id: 'broken-only',
          isDefault: false,
          broken: 'private-error',
          rows: [],
        },
      ],
    }),
  });
  await click(wrapper, 'My resources');
  expect(wrapper.text()).toContain('broken-only');
  expect(wrapper.text()).toContain(
    'Preset inventory is unavailable; installation state is unknown.',
  );
  expect(wrapper.text()).not.toContain('No local plugins');
  expect(wrapper.text()).not.toContain('private-error');
});
it('labels personal L4 resources as unreviewed without inferring approval from publication', async () => {
  const personal = {
    ...plugin,
    level: 'l4' as const,
    visibility: 'private' as const,
  };
  const wrapper = render(
    connect(async (path) =>
      json(
        path.includes('/versions')
          ? { items: [version] }
          : path.includes(packageId)
            ? personal
            : { items: [personal] },
      ),
    ),
  );
  await flushPromises();
  expect(wrapper.text()).toContain('Unreviewed');
  await click(wrapper, 'View details');
  expect(wrapper.text()).toContain('Unreviewed');
  expect(wrapper.text()).toContain('Permission declarations are unavailable');
});
it('confirms a managed distribution and only shows applied after the node generation matches', async () => {
  const instanceId = '01900000-0000-7000-8000-000000000009';
  const distId = '01900000-0000-7000-8000-000000000008';
  let observed = 2;
  const fetch = vi.fn(async (path: string, init?: RequestInit) => {
    if (path.includes('/distributions'))
      return json(
        {
          id: distId,
          instanceId,
          ownerId: plugin.ownerId,
          revision: observed,
          selectionRevision: 1,
          state: init?.method === 'POST' ? 'requested' : 'applied',
          generation: 3,
          observedGeneration: observed,
          ready: true,
        },
        init?.method === 'POST' ? 202 : 200,
      );
    if (path.endsWith('/versions/' + versionId)) return json(version);
    if (path.includes('/versions')) return json({ items: [version] });
    return json(
      path.includes(packageId)
        ? { ...plugin, level: 'l3' }
        : { items: [{ ...plugin, level: 'l3' }] },
    );
  });
  const wrapper = render({
    ...connect(fetch),
    mode: 'managed',
    resourceDistributionAvailable: true,
    resourceIdentity: {
      instanceId,
      tenantId: plugin.tenantId,
      ownerId: plugin.ownerId,
    },
  });
  await flushPromises();
  await click(wrapper, 'View details');
  await click(wrapper, 'Install / update');
  expect(fetch.mock.calls.some(([, init]) => init?.method === 'POST')).toBe(
    false,
  );
  await click(wrapper, 'Cancel');
  expect(fetch.mock.calls.some(([, init]) => init?.method === 'POST')).toBe(
    false,
  );
  await click(wrapper, 'Install / update');
  await click(wrapper, 'Confirm');
  expect(wrapper.text()).toContain('Dispatched');
  await click(wrapper, 'Refresh distribution status');
  expect(wrapper.text()).toContain('Applying');
  expect(wrapper.text()).not.toContain('Applied (node');
  observed = 3;
  await click(wrapper, 'Refresh distribution status');
  expect(wrapper.text()).toContain('Applied (node ready');
  expect(
    fetch.mock.calls.filter(([, init]) => init?.method === 'POST'),
  ).toHaveLength(1);
});

it('keeps uncertain managed writes bound to the same intent key when manually retried', async () => {
  const instanceId = '01900000-0000-7000-8000-000000000009';
  const keys: string[] = [];
  const fetch = vi.fn(async (path: string, init?: RequestInit) => {
    if (init?.method === 'POST') {
      keys.push(new Headers(init.headers).get('Idempotency-Key')!);
      throw new Error('private-transport-error');
    }
    if (path.endsWith('/versions/' + versionId)) return json(version);
    if (path.includes('/versions')) return json({ items: [version] });
    return json(
      path.includes(packageId)
        ? { ...plugin, level: 'l3' }
        : { items: [{ ...plugin, level: 'l3' }] },
    );
  });
  const wrapper = render({
    ...connect(fetch),
    mode: 'managed',
    resourceDistributionAvailable: true,
    resourceIdentity: {
      instanceId,
      tenantId: plugin.tenantId,
      ownerId: plugin.ownerId,
    },
  });
  await flushPromises();
  await click(wrapper, 'View details');
  await click(wrapper, 'Install / update');
  await click(wrapper, 'Confirm');
  expect(wrapper.text()).toContain('request outcome is unknown');
  expect(wrapper.text()).not.toContain('private-transport-error');
  await click(wrapper, 'Retry this distribution');
  expect(keys).toHaveLength(2);
  expect(keys[0]).toBe(keys[1]);
});
it('keeps normal device-backed managed operations unavailable without claiming a successful writer grant', async () => {
  const fetch = vi.fn(async (path: string, init?: RequestInit) => {
    expect(init?.method).not.toBe('POST');
    return json(
      path.includes('/versions')
        ? { items: [version] }
        : path.includes(packageId)
          ? { ...plugin, level: 'l3' }
          : { items: [plugin] },
    );
  });
  const wrapper = render({
    ...connect(fetch),
    mode: 'managed',
    resourceIdentity: {
      instanceId: versionId,
      tenantId: plugin.tenantId,
      ownerId: plugin.ownerId,
    },
    resourceDistributionAvailable: false,
  });
  await flushPromises();
  await click(wrapper, 'View details');
  expect(wrapper.text()).toContain(
    'Member distribution credentials are not connected',
  );
  expect(
    wrapper
      .findAll('button')
      .some((button) => button.text() === 'Install / update'),
  ).toBe(false);
  expect(
    fetch.mock.calls.some(
      ([, init]) => (init as RequestInit | undefined)?.method === 'POST',
    ),
  ).toBe(false);
});
it('limits the member market to enabled published defaults and published versions', async () => {
  const unpublished = {
    ...plugin,
    id: '01900000-0000-7000-8000-000000000006',
    name: 'Unpublished plugin',
    defaultVersionId: null,
  };
  const disabled = {
    ...plugin,
    id: '01900000-0000-7000-8000-000000000007',
    name: 'Disabled plugin',
    enabled: false,
  };
  const fetch = vi.fn(async (path: string) =>
    json(
      path.includes('/versions')
        ? {
            items: [
              version,
              {
                ...version,
                id: unpublished.id,
                state: 'draft',
                version: 'draft-version',
              },
            ],
          }
        : path.includes(packageId)
          ? plugin
          : { items: [plugin, unpublished, disabled] },
    ),
  );
  const wrapper = render(connect(fetch));
  await flushPromises();
  expect(wrapper.findAll('.card')).toHaveLength(1);
  expect(wrapper.text()).not.toContain('Unpublished plugin');
  expect(wrapper.text()).not.toContain('Disabled plugin');
  await click(wrapper, 'View details');
  expect(wrapper.text()).toContain(version.version);
  expect(wrapper.text()).not.toContain('draft-version');
});
