import { describe, expect, it, vi } from 'vitest';
import { DesktopCatalog } from '../src/desktop-catalog';
import {
  allPluginPackages,
  filterPlugins,
  marketOptions,
  validateStar,
} from '../../shared/src/plugin-market';

const market = {
  authorId: 'a',
  version: '1.0.0',
  dshVersionRange: '^1.0.0',
  description: '编辑文档',
  tags: ['办公'],
  installationCount: 0,
  starCount: 1,
  starred: false,
};
const older = {
  id: '1',
  name: '旧工具',
  market: { ...market, publishedAt: '2026-08-01', installationCount: 5 },
};
const newer = {
  id: '2',
  name: '新工具',
  market: {
    ...market,
    authorId: 'b',
    authorDepartment: '研发',
    tags: ['终端'],
    starCount: 3,
    publishedAt: '2026-09-01',
  },
};
const legacy = { id: '3', name: '经典工具' };
const filters = {
  search: '',
  author: '',
  department: '',
  tag: '',
  sort: '' as const,
};

describe('plugin market', () => {
  it.each([false, true])(
    'tracks a queued failure after the action response is lost (%s)',
    async (lostResponse) => {
      vi.useFakeTimers();
      const ownerId = 'member';
      const plugin = {
        state: 'applying',
        installationRevision: 1,
        resource: {
          identity: { packageId: 'tool', versionId: 'v1', kind: 'plugin' },
        },
      };
      const operation = {
        packageId: 'tool',
        versionId: 'v1',
        phase: 'installing_dependencies',
        attempt: { index: 2, total: 3 },
      };
      const failure = {
        ...plugin,
        state: 'failed',
        pluginFailure: {
          reason: 'plugin_install_network',
          trace_id: 'a'.repeat(32),
        },
      };
      let installing = false;
      let finish!: (response: Response) => void;
      const action = new Promise<Response>((resolve) => {
        finish = resolve;
      });
      const host = vi.fn(async (path: string) => {
        if (path.startsWith('catalog/packages'))
          return Response.json({ items: [] });
        if (path === 'catalog/actions') {
          installing = true;
          return action;
        }
        if (path === 'catalog/plugin-progress')
          return Response.json({ operation: installing ? operation : null });
        if (path === 'catalog/installations')
          return Response.json({
            identity: { ownerId },
            items: host.mock.calls.some(([path]) => path === 'catalog/actions')
              ? [failure]
              : [],
          });
        throw new Error(path);
      });
      const catalog = new DesktopCatalog(host, { ownerId });
      try {
        await catalog.refresh();
        const installingPlugin = catalog.act(
          {
            action: 'install',
            packageId: 'tool',
            versionId: 'v1',
            revision: 0,
          },
          'plugin',
        );
        const timedOut = lostResponse
          ? expect(installingPlugin).resolves.toEqual({ awaitingResult: true })
          : undefined;
        await vi.advanceTimersByTimeAsync(1000);
        expect(catalog.pluginOperation).toEqual(operation);
        expect(catalog.mutating).toBe(true);
        if (timedOut) {
          await vi.advanceTimersByTimeAsync(90000);
          await timedOut;
          expect(catalog.isTrackingPlugin('tool')).toBe(true);
          await expect(
            catalog.act(
              {
                action: 'install',
                packageId: 'tool',
                versionId: 'v1',
                revision: 0,
              },
              'plugin',
            ),
          ).rejects.toThrow('unavailable');
        }
        installing = false;
        if (!lostResponse) {
          finish(Response.json({ installation: plugin }, { status: 202 }));
          await installingPlugin;
        }
        await vi.advanceTimersByTimeAsync(1000);
        expect(catalog.snapshot?.installations.items).toEqual([failure]);
        expect(catalog.pluginOperation).toBeUndefined();
        expect(catalog.isTrackingPlugin('tool')).toBe(false);
        expect(catalog.error).toBeUndefined();
        const progressReads = host.mock.calls.filter(
          ([path]) => path === 'catalog/plugin-progress',
        ).length;
        await vi.advanceTimersByTimeAsync(5000);
        expect(
          host.mock.calls.filter(([path]) => path === 'catalog/actions'),
        ).toHaveLength(1);
        expect(
          host.mock.calls.filter(
            ([path]) => path === 'catalog/plugin-progress',
          ),
        ).toHaveLength(progressReads);
      } finally {
        catalog.dispose();
        vi.useRealTimers();
      }
    },
  );

  it('builds filters and sorts over packages from every page', async () => {
    const { packages } = await allPluginPackages(async (cursor) =>
      cursor
        ? { packages: [newer, legacy] }
        : { packages: [older], cursor: 'next' },
    );
    expect(marketOptions(packages, 'zh-CN').departments).toEqual(['研发']);
    expect(filterPlugins(packages, { ...filters, search: '编辑' })).toEqual([
      older,
      newer,
    ]);
    expect(filterPlugins(packages, { ...filters, search: '研发' })).toEqual([
      newer,
    ]);
    expect(
      filterPlugins(packages, {
        ...filters,
        search: '终端',
        author: 'b',
        department: '研发',
        tag: '终端',
      }),
    ).toEqual([newer]);
    expect(filterPlugins(packages, { ...filters, search: '经典' })).toEqual([
      legacy,
    ]);
    expect(
      filterPlugins(packages, { ...filters, sort: 'publishedAt' }),
    ).toEqual([newer, older, legacy]);
    expect(
      filterPlugins(packages, { ...filters, sort: 'installationCount' }),
    ).toEqual([older, newer, legacy]);
    expect(filterPlugins(packages, { ...filters, sort: 'starCount' })).toEqual([
      newer,
      older,
      legacy,
    ]);
    expect(filterPlugins(packages, { ...filters, sort: 'name' }).map((p) => p.name)).toEqual(
      [newer, legacy, older]
        .map((p) => p.name)
        .sort((a, b) => a.localeCompare(b)),
    );
  });
  it('rejects repeated cursors and duplicate packages', async () => {
    await expect(
      allPluginPackages(async () => ({ packages: [], cursor: 'next' })),
    ).rejects.toThrow('Invalid plugin catalog pagination');
    await expect(
      allPluginPackages(async (cursor) => ({
        packages: [older],
        cursor: cursor ? undefined : 'next',
      })),
    ).rejects.toThrow('Invalid plugin catalog pagination');
  });
  it('accepts only a boolean state and nonnegative integer count', () => {
    expect(validateStar({ starred: true, starCount: 3 })).toEqual({
      starred: true,
      starCount: 3,
    });
    expect(() => validateStar({ starred: true, starCount: -1 })).toThrow(
      'invalid',
    );
    expect(() => validateStar({ starred: 1, starCount: 1 })).toThrow('invalid');
  });
});
