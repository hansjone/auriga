import { describe, expect, it } from 'vitest';
import {
  allPluginPackages,
  filterPlugins,
  marketOptions,
  validateStar,
} from '../src/plugin-market';

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
