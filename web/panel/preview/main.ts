import { registerPanel } from '../src/index';
import {
  filterPlugins,
  type MarketSort,
} from '../../shared/src/plugin-market';

registerPanel();

const ownerId = '01900000-0000-7000-8000-000000000003';
const tenantId = '01900000-0000-7000-8000-000000000001';
const id = (n: number) =>
  `01900000-0000-7000-8000-${String(n).padStart(12, '0')}`;

type Pack = {
  id: string;
  tenantId: string;
  ownerId: string;
  name: string;
  kind: 'plugin' | 'skill';
  enabled: boolean;
  defaultVersionId: string;
  revision: number;
  market: {
    authorId: string;
    authorName: string;
    authorDepartment: string;
    version: string;
    dshVersionRange: string;
    description: string;
    tags: string[];
    installationCount: number;
    starCount: number;
    starred: boolean;
    publishedAt: string;
  };
};

const seed: Pack[] = [
  {
    id: id(10),
    tenantId,
    ownerId,
    name: 'my-decoration',
    kind: 'plugin',
    enabled: true,
    defaultVersionId: id(11),
    revision: 1,
    market: {
      authorId: id(20),
      authorName: '周同学',
      authorDepartment: '研发中心',
      version: '0.1.0',
      dshVersionRange: '^0.2.0',
      description: '给会话加一点装饰效果，方便演示短简介卡片。',
      tags: ['装饰', '演示'],
      installationCount: 3,
      starCount: 1,
      starred: false,
      publishedAt: '2026-11-10T08:00:00Z',
    },
  },
  {
    id: id(12),
    tenantId,
    ownerId,
    name: 'cnms-file-assistant',
    kind: 'plugin',
    enabled: true,
    defaultVersionId: id(13),
    revision: 2,
    market: {
      authorId: id(21),
      authorName: '张工',
      authorDepartment:
        '中国移动通信集团浙江有限公司杭州分公司网络部/运维支撑中心',
      version: '1.2.0',
      dshVersionRange: '^0.2.0',
      description:
        '面向 CNMS 场景的文件助手：整理附件、摘要说明，并支持选择版本安装与更新。',
      tags: ['文件', 'CNMS', '运维', '助手', '文档'],
      installationCount: 28,
      starCount: 7,
      starred: true,
      publishedAt: '2026-11-20T10:30:00Z',
    },
  },
  {
    id: id(14),
    tenantId,
    ownerId,
    name: 'topology-sketch',
    kind: 'plugin',
    enabled: true,
    defaultVersionId: id(15),
    revision: 1,
    market: {
      authorId: id(20),
      authorName: '周同学',
      authorDepartment: '研发中心',
      version: '2.0.1',
      dshVersionRange: '^0.2.1',
      description: '快速草绘网络拓扑，适合评审前的白板讨论。',
      tags: ['拓扑', '网络'],
      installationCount: 12,
      starCount: 4,
      starred: false,
      publishedAt: '2026-11-22T02:15:00Z',
    },
  },
  {
    id: id(30),
    tenantId,
    ownerId,
    name: 'meeting-notes',
    kind: 'skill',
    enabled: true,
    defaultVersionId: id(31),
    revision: 1,
    market: {
      authorId: id(20),
      authorName: '周同学',
      authorDepartment: '研发中心',
      version: '1.0.0',
      dshVersionRange: '^0.2.0',
      description: '把会议录音与纪要整理成可执行待办。',
      tags: ['会议', '纪要'],
      installationCount: 9,
      starCount: 2,
      starred: false,
      publishedAt: '2026-11-18T04:00:00Z',
    },
  },
];

const extras: Pack[] = Array.from({ length: 20 }, (_, index) => {
  const n = 100 + index;
  const kind = index % 4 === 0 ? ('skill' as const) : ('plugin' as const);
  return {
    id: id(n),
    tenantId,
    ownerId,
    name: `demo-${kind}-${index + 1}`,
    kind,
    enabled: true,
    defaultVersionId: id(n + 500),
    revision: 1,
    market: {
      authorId: id(20 + (index % 3)),
      authorName: index % 2 ? '李工' : '周同学',
      authorDepartment: index % 3 ? '运维中心' : '研发中心',
      version: `1.${index}.0`,
      dshVersionRange: '^0.2.0',
      description: `演示分页用的假数据资源 ${index + 1}，用于验证页码与每页数量切换。`,
      tags: [kind, '演示', `批次${(index % 5) + 1}`],
      installationCount: (index * 3) % 40,
      starCount: index % 9,
      starred: false,
      publishedAt: new Date(Date.UTC(2026, 10, 1 + index)).toISOString(),
    },
  };
});

const catalog = [...seed, ...extras];

const installed = {
  identity: { ownerId, tenantId },
  sharedAvailable: true,
  items: [
    {
      clientId: id(40),
      installationRevision: 2,
      selectionRevision: 1,
      generation: 1,
      state: 'applied',
      reportSource: 'member',
      resource: {
        identity: {
          tenantId,
          packageId: id(12),
          versionId: id(13),
          kind: 'plugin',
          version: '1.2.0',
          manifestDigest: 'sha256:' + 'b'.repeat(64),
        },
        envelope: { statement: { name: 'cnms-file-assistant' } },
        enabled: true,
        desiredState: 'ENABLED',
        installationMode: 'optional',
      },
    },
  ],
};

function json(body: unknown, status = 200) {
  return Response.json(body, { status });
}

function pagePackages(query: URLSearchParams) {
  const kind = query.get('kind') as 'plugin' | 'skill' | null;
  const search = query.get('search') ?? '';
  const sort = (query.get('sort') ?? '') as MarketSort;
  const page = Math.max(1, Number(query.get('page') ?? '1') || 1);
  const limit = Math.min(
    200,
    Math.max(1, Number(query.get('limit') ?? '12') || 12),
  );
  const filtered = filterPlugins(
    kind ? catalog.filter((pack) => pack.kind === kind) : catalog,
    { search, author: '', department: '', tag: '', sort },
  );
  const start = (page - 1) * limit;
  return {
    items: filtered.slice(start, start + limit),
    total: filtered.length,
  };
}

async function fakeCatalog(path: string, init?: RequestInit): Promise<Response> {
  const url = path.replace(/^catalog\//, '');
  if (url === 'installations') return json(installed);
  if (url.startsWith('packages?')) {
    const query = new URLSearchParams(url.slice(url.indexOf('?') + 1));
    if (query.has('page')) return json(pagePackages(query));
    const kind = query.get('kind');
    return json({
      items: kind
        ? catalog.filter((pack) => pack.kind === kind)
        : catalog,
    });
  }
  const star = url.match(/^packages\/([^/]+)\/star$/);
  if (star && init?.method === 'POST') {
    const pack = catalog.find((item) => item.id === star[1]);
    if (!pack?.market) return json({ title: 'missing' }, 404);
    const body = JSON.parse(String(init.body ?? '{}')) as { starred?: boolean };
    pack.market.starred = Boolean(body.starred);
    pack.market.starCount = Math.max(
      0,
      pack.market.starCount + (pack.market.starred ? 1 : -1),
    );
    return json({
      starred: pack.market.starred,
      starCount: pack.market.starCount,
    });
  }
  if (url === 'actions') {
    return json(
      {
        accepted: true,
        installation: installed.items[0],
      },
      202,
    );
  }
  if (url.includes('/versions')) {
    const packId = url.split('/')[1];
    const pack = catalog.find((item) => item.id === packId);
    if (!pack) return json({ title: 'missing' }, 404);
    const version = {
      id: pack.defaultVersionId,
      packageId: pack.id,
      kind: pack.kind,
      version: pack.market?.version ?? '1.0.0',
      state: 'published',
      manifest: {
        kind: pack.kind,
        name: pack.name,
        version: pack.market?.version ?? '1.0.0',
        description: pack.market?.description ?? '',
        author: { displayName: pack.market?.authorName ?? 'Unknown' },
        license: 'MIT',
        permissions: { tools: [], outboundHosts: [], dataAccess: [] },
        ...(pack.kind === 'plugin'
          ? { plugin: { packageName: '@example/' + pack.name } }
          : { skill: { entry: 'SKILL.md' } }),
      },
      digest: 'sha256:' + 'a'.repeat(64),
    };
    if (url.endsWith('/versions/' + pack.defaultVersionId)) return json(version);
    return json({ items: [version] });
  }
  return json({ items: [] });
}

const panel = document.querySelector('auriga-team-panel')!;
(
  panel as HTMLElement & {
    context: Record<string, unknown>;
  }
).context = {
  mode: 'connect',
  protocol: 1,
  locale: 'zh-CN',
  theme: 'light',
  fetch: fakeCatalog,
};
