import { afterEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils';
import TeamPanel from '../src/TeamPanel.ce.vue';
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
let wrappers: VueWrapper[] = [];
function render(context?: PanelContext) {
  const wrapper = mount(TeamPanel, { props: { context } });
  wrappers.push(wrapper);
  return wrapper;
}
async function click(wrapper: VueWrapper, label: string) {
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
  it.each(['managed', 'standalone', undefined] as const)(
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
    { items: [plugin], nextCursor: '' },
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
    expect(wrapper.text()).toContain('Local installation · Unavailable');
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
    const host = vi.fn().mockResolvedValue(json({ items: [plugin] }));
    const wrapper = render(connect(host));
    await flushPromises();
    await wrapper.setProps({ context: { ...connect(host), mode: 'managed' } });
    expect(wrapper.text()).not.toContain(plugin.name);
    expect(wrapper.text()).toContain('managed environment');
    expect(host).toHaveBeenCalledTimes(1);
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
