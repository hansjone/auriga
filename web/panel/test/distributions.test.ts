import { describe, it, expect, vi } from 'vitest';
import {
  createDistributionClient,
  distributionPhase,
  allowedActions,
} from '../src/distributions';
import type {
  PanelContext,
  ResourcePackage,
  ResourceVersion,
} from '../src/resources';
const id = (n: number) =>
  `01900000-0000-7000-8000-${String(n).padStart(12, '0')}`;
const identity = { tenantId: id(1), ownerId: id(2), instanceId: id(3) };
const pack = {
  id: id(4),
  tenantId: id(1),
  ownerId: id(2),
  level: 'l3',
  visibility: 'tenant',
  enabled: true,
  name: 'example',
  kind: 'plugin',
  groupIds: [],
  revision: 1,
  createdAt: '2026-11-11T00:00:00Z',
  defaultVersionId: id(5),
} as ResourcePackage;
const version = {
  id: id(5),
  packageId: id(4),
  state: 'published',
  version: '1.0.0',
  sizeBytes: 100,
  mediaType: 'application/gzip',
  revision: 1,
  createdAt: '2026-11-11T00:00:00Z',
  digest: `sha256:${'a'.repeat(64)}`,
} as ResourceVersion;
const row = {
  id: id(6),
  instanceId: id(3),
  ownerId: id(2),
  selectionRevision: 1,
  state: 'requested',
  revision: 1,
};
const signal = new AbortController().signal;
function host(extra: Partial<typeof row> = {}) {
  const fetch = vi.fn(async (path: string, init?: RequestInit) => {
    if (init?.method === 'POST')
      expect(path).toBe('/api/resource/v1/distributions');
    return Response.json(
      path.endsWith(`/versions/${id(5)}`)
        ? version
        : path.endsWith(id(4))
          ? pack
          : { ...row, ...extra },
      { status: path.endsWith('/distributions') ? 202 : 200 },
    );
  });
  return {
    fetch,
    context: {
      mode: 'managed',
      protocol: 1,
      fetch,
      resourceDistributionAvailable: true,
      resourceIdentity: identity,
    } as PanelContext,
  };
}
describe('member distributions', () => {
  it.each(['l0', 'l1', 'l2', 'l3', 'l4'] as const)(
    'enforces %s member controls',
    (level) => {
      const actions = allowedActions(
        { ...pack, level, visibility: level === 'l4' ? 'private' : 'tenant' },
        identity,
      );
      expect(actions).toEqual(
        level === 'l0' || level === 'l1'
          ? []
          : level === 'l2'
            ? ['enable', 'disable']
            : ['install', 'enable', 'disable', 'remove'],
      );
    },
  );
  it('rejects cross tenant and personal ownership mismatch', () => {
    expect(allowedActions({ ...pack, tenantId: id(9) }, identity)).toEqual([]);
    expect(
      allowedActions(
        { ...pack, level: 'l4', visibility: 'private', ownerId: id(9) },
        identity,
      ),
    ).toEqual([]);
  });
  it('rereads exact metadata before a single idempotent write and never installs in the browser', async () => {
    const h = host();
    const client = createDistributionClient(h.context);
    expect(
      await client.request(
        pack.id,
        version.id,
        'install',
        'intent-key',
        signal,
      ),
    ).toEqual(row);
    expect(h.fetch.mock.calls.map(([path]) => path)).toEqual([
      `/api/resource/v1/packages/${pack.id}`,
      `/api/resource/v1/packages/${pack.id}/versions/${version.id}`,
      '/api/resource/v1/distributions',
    ]);
    const init = h.fetch.mock.calls[2][1] as RequestInit;
    expect(JSON.parse(init.body as string)).toEqual({
      instanceId: identity.instanceId,
      selections: [
        { packageId: pack.id, versionId: version.id, action: 'install' },
      ],
    });
    expect(new Headers(init.headers).get('Idempotency-Key')).toBe('intent-key');
  });
  it('never treats accepted or generation-mismatched state as applied', () => {
    expect(
      distributionPhase({
        ...row,
        state: 'applied',
        generation: 3,
        observedGeneration: 2,
        ready: true,
      } as never),
    ).toBe('applying');
    expect(
      distributionPhase({
        ...row,
        state: 'applied',
        generation: 3,
        observedGeneration: 3,
        ready: false,
      } as never),
    ).toBe('applying');
    expect(
      distributionPhase({
        ...row,
        state: 'applied',
        generation: 3,
        observedGeneration: 3,
        ready: true,
      } as never),
    ).toBe('applied');
    expect(distributionPhase(row as never)).toBe('requested');
  });
  it('refuses desktop writes, absent identity and wrong returned owner/instance', async () => {
    const h = host({ ownerId: id(9) });
    expect(() =>
      createDistributionClient({ ...h.context, mode: 'connect' }),
    ).toThrow();
    expect(() =>
      createDistributionClient({ ...h.context, resourceIdentity: undefined }),
    ).toThrow();
    await expect(
      createDistributionClient(h.context).request(
        pack.id,
        version.id,
        'install',
        'key',
        signal,
      ),
    ).rejects.toThrow('invalid');
  });
  it('does not replay writes after a transport error', async () => {
    const h = host();
    h.fetch.mockImplementation(async (path) => {
      if (path.endsWith('/distributions')) throw new Error('private-error');
      return Response.json(path.includes('/versions/') ? version : pack);
    });
    await expect(
      createDistributionClient(h.context).request(
        pack.id,
        version.id,
        'install',
        'key',
        signal,
      ),
    ).rejects.toThrow('unavailable');
    expect(
      h.fetch.mock.calls.filter(([p]) => p.endsWith('/distributions')),
    ).toHaveLength(1);
  });
});
it.each(['l1', 'cross-tenant', 'withdrawn'])(
  'rejects a fresh %s decision before sending a distribution',
  async (condition) => {
    const h = host();
    h.fetch.mockImplementation(async (path) =>
      Response.json(
        path.includes('/versions/')
          ? {
              ...version,
              state: condition === 'withdrawn' ? 'withdrawn' : 'published',
            }
          : {
              ...pack,
              level: condition === 'l1' ? 'l1' : 'l3',
              tenantId:
                condition === 'cross-tenant' ? id(9) : identity.tenantId,
            },
      ),
    );
    await expect(
      createDistributionClient(h.context).request(
        pack.id,
        version.id,
        'install',
        'key',
        signal,
      ),
    ).rejects.toThrow('forbidden');
    expect(
      h.fetch.mock.calls.filter(([path]) => path.endsWith('/distributions')),
    ).toHaveLength(0);
  },
);
