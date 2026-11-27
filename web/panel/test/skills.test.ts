import { afterEach, expect, it, vi } from 'vitest';
import { AurigaTeamPanel } from '../src/TeamPanel';
import {
  ResourceDiagnosticError,
  resourceErrorMessage,
  resourceResponseError,
} from '../src/resource-diagnostics';
it('rejects inherited diagnostic reasons and untrusted trace content', async () => {
  const error = new ResourceDiagnosticError('toString', 'private content');
  expect(resourceErrorMessage(error, false)).toContain('操作未完成');
  expect(resourceErrorMessage(error, false)).not.toContain('private content');
  expect(
    (
      await resourceResponseError(
        Response.json(
          { code: 'toString', reason: '__proto__' },
          { status: 500 },
        ),
      )
    ).reason,
  ).toBe('internal_error');
});
const packageId = '019a0000-0000-7000-8000-000000000002',
  versionId = '019a0000-0000-7000-8000-000000000004';
const identity = {
  tenantId: '019a0000-0000-7000-8000-000000000001',
  packageId,
  versionId,
  kind: 'skill',
  version: '1.0.0',
  manifestDigest: 'sha256:' + 'a'.repeat(64),
};
const row = {
  clientId: '019a0000-0000-7000-8000-000000000011',
  installationRevision: 1,
  selectionRevision: 7,
  generation: 9,
  state: 'applying',
  resource: {
    identity,
    enabled: true,
    envelope: { statement: { name: 'example' } },
  },
};
if (!customElements.get('auriga-test-skills'))
  customElements.define('auriga-test-skills', AurigaTeamPanel);
afterEach(() => {
  document.body.replaceChildren();
  vi.restoreAllMocks();
});

it.each([
  [
    'zh-CN',
    '技能',
    '当前账号或设备没有资源访问权限',
    '刷新登录凭据',
    '联系管理员',
  ],
  [
    'en',
    'Skills',
    'Your account or device is not authorized to access resources',
    'Refresh your sign-in credentials',
    'contact an administrator',
  ],
])(
  'explains resource authorization failures and recovery in %s',
  async (locale, tab, reason, refresh, administrator) => {
    const panel = document.createElement(
      'auriga-test-skills',
    ) as AurigaTeamPanel;
    panel.context = {
      mode: 'connect',
      protocol: 1,
      locale,
      fetch: vi.fn(async (path: string) =>
        path.startsWith('catalog/')
          ? Response.json({}, { status: 403 })
          : Response.json({ items: [] }),
      ),
    };
    document.body.append(panel);
    await settle();
    click(panel, tab);
    await vi.waitFor(() => {
      expect(panel.shadowRoot!.textContent).toContain(reason);
      expect(panel.shadowRoot!.textContent).toContain(refresh);
      expect(panel.shadowRoot!.textContent).toContain(administrator);
    });
  },
);

it('offers a next step for empty skill markets and installations', async () => {
  const panel = document.createElement('auriga-test-skills') as AurigaTeamPanel;
  panel.context = {
    mode: 'connect',
    protocol: 1,
    locale: 'en',
    fetch: vi.fn(async (path: string) =>
      Response.json(
        path.startsWith('catalog/packages?')
          ? { items: [], total: 0 }
          : { items: [] },
      ),
    ),
  };
  document.body.append(panel);
  await settle();
  click(panel, 'Skills');
  await vi.waitFor(() =>
    expect(panel.shadowRoot!.textContent).toContain(
      'Ask an administrator to publish',
    ),
  );
  click(panel, 'My resources');
  await vi.waitFor(() =>
    expect(panel.shadowRoot!.textContent).toContain(
      'Choose a resource from the market',
    ),
  );
});
async function settle() {
  for (let n = 0; n < 8; n++) await Promise.resolve();
}
it.each([false, true])(
  'keeps failed installation capability unknown (mine=%s)',
  async (mine) => {
    const root = document.createElement('div');
    document.body.append(root);
    let available: boolean | undefined = false;
    let failed = true;
    const { SkillPanel } = await import('../src/SkillPanel');
    const panel = new SkillPanel(
      root,
      {
        mode: 'connect',
        protocol: 1,
        locale: 'en',
        fetch: async (path: string) => {
          if (path === 'catalog/installations' && failed)
            return Response.json(
              { code: 'common.validation_failed', reason: 'validation_failed' },
              { status: 400 },
            );
          return Response.json({
            items: [],
            ...(path.startsWith('catalog/packages?') ? { total: 0 } : {}),
            ...(available === undefined ? {} : { sharedAvailable: available }),
          });
        },
      },
      mine,
    );
    try {
      await vi.waitFor(() => expect(root.textContent).toContain('invalid'));
      expect(root.textContent).not.toContain('trusted guardian');
      failed = false;
      clickRefresh(root);
      await vi.waitFor(() =>
        expect(root.textContent).toContain('trusted guardian'),
      );
      failed = true;
      clickRefresh(root);
      await vi.waitFor(() => expect(root.textContent).toContain('invalid'));
      expect(root.textContent).not.toContain('trusted guardian');
      failed = false;
      available = undefined;
      clickRefresh(root);
      await vi.waitFor(() => expect(root.textContent).not.toContain('invalid'));
      expect(root.textContent).not.toContain('trusted guardian');
    } finally {
      panel.dispose();
    }
  },
);
function click(panel: AurigaTeamPanel, label: string) {
  const button = [...panel.shadowRoot!.querySelectorAll('button')].find(
    (b) =>
      b.textContent === label || b.getAttribute('aria-label') === label,
  );
  expect(button, label).toBeTruthy();
  button!.click();
}
function clickRefresh(root: HTMLElement) {
  const button =
    root.querySelector<HTMLButtonElement>('button[aria-label="Refresh"]') ||
    [...root.querySelectorAll('button')].find((b) => b.textContent === 'Refresh');
  expect(button, 'Refresh').toBeTruthy();
  button!.click();
}
it('separates Skill reads from legacy plugin APIs, installs the selected exact version and keeps 202 distinct from applied', async () => {
  let installed = false;
  const fetch = vi.fn(async (path: string, init?: RequestInit) => {
    if (path.startsWith('/api/resource/v1/packages'))
      return Response.json({ items: [] });
    if (
      path.startsWith('catalog/packages?') &&
      path.includes('kind=skill')
    )
      return Response.json({
        items: [
          {
            id: packageId,
            kind: 'skill',
            name: 'example',
            defaultVersionId: versionId,
          },
        ],
        total: 1,
      });
    if (path.startsWith('catalog/packages?'))
      return Response.json({ items: [], total: 0 });
    if (path === `catalog/packages/${packageId}/versions/${versionId}`)
      return Response.json({
        id: versionId,
        packageId,
        kind: 'skill',
        version: '1.0.0',
        state: 'published',
        manifest: { description: '<img src=x onerror=alert(1)>' },
      });
    if (path === `catalog/packages/${packageId}/versions?limit=200`)
      return Response.json({
        items: [
          {
            id: versionId,
            packageId,
            kind: 'skill',
            version: '1.0.0',
            state: 'published',
            manifest: { description: '<img src=x onerror=alert(1)>' },
          },
        ],
      });
    if (path === 'catalog/installations')
      return Response.json({ items: installed ? [row] : [] });
    if (path === 'catalog/actions') {
      expect(new Headers(init?.headers).get('x-auriga-catalog')).toBe('1');
      const input = JSON.parse(String(init?.body));
      expect(input).toMatchObject({
        packageId,
        versionId,
        revision: 0,
        action: 'install',
      });
      expect(input.clientId).toBeUndefined();
      installed = true;
      return Response.json(
        { accepted: true, installation: row },
        { status: 202 },
      );
    }
    throw new Error('unexpected ' + path);
  });
  const panel = document.createElement('auriga-test-skills') as AurigaTeamPanel;
  panel.context = { mode: 'connect', protocol: 1, locale: 'zh-CN', fetch };
  document.body.append(panel);
  await settle();
  click(panel, '技能');
  await vi.waitFor(() =>
    expect(panel.shadowRoot!.textContent).toContain('<img src=x'),
  );
  const legacyCount = fetch.mock.calls.filter(([p]) =>
    p.startsWith('/api/'),
  ).length;
  expect(panel.shadowRoot!.querySelector('img')).toBeNull();
  click(panel, '安装');
  await vi.waitFor(() =>
    expect(panel.shadowRoot!.textContent).toContain('已接受请求'),
  );
  expect(panel.shadowRoot!.textContent).not.toContain('applied');
  click(panel, '我的资源');
  await vi.waitFor(() =>
    expect(panel.shadowRoot!.textContent).toContain('正在应用'),
  );
  expect(fetch.mock.calls.filter(([p]) => p.startsWith('/api/'))).toHaveLength(
    legacyCount,
  );
});
it('discards an old identity response after the panel context changes', async () => {
  let resolveOld: (v: Response) => void = () => {};
  const oldFetch = vi.fn(async (path: string) =>
    path.startsWith('/api/')
      ? Response.json({ items: [] })
      : new Promise<Response>((r) => {
          resolveOld = r;
        }),
  );
  const panel = document.createElement('auriga-test-skills') as AurigaTeamPanel;
  panel.context = { mode: 'connect', protocol: 1, fetch: oldFetch };
  document.body.append(panel);
  await settle();
  click(panel, '技能');
  await settle();
  const nextFetch = vi.fn(async (path: string) =>
    Response.json({
      items:
        path.includes('kind=skill')
          ? [{ id: packageId, kind: 'skill', name: 'new-identity' }]
          : [],
      total: path.includes('kind=skill') ? 1 : 0,
    }),
  );
  panel.context = { mode: 'connect', protocol: 1, fetch: nextFetch };
  await settle();
  click(panel, '技能');
  resolveOld(
    Response.json({
      items: [{ id: packageId, kind: 'skill', name: 'old-identity' }],
    }),
  );
  await settle();
  await vi.waitFor(() =>
    expect(panel.shadowRoot!.textContent).toContain('new-identity'),
  );
  expect(panel.shadowRoot!.textContent).not.toContain('old-identity');
});
it('shows readable Chinese guardian and file-occupation hints, without replacement question marks', async () => {
  const fetch = vi.fn(async (path: string) =>
    Response.json({
      items: [],
      ...(path.startsWith('catalog/packages?') ? { total: 0 } : {}),
      ...(path === 'catalog/installations'
        ? { sharedAvailable: false, pendingRestart: true }
        : {}),
    }),
  );
  const panel = document.createElement('auriga-test-skills') as AurigaTeamPanel;
  panel.context = { mode: 'connect', protocol: 1, locale: 'zh-CN', fetch };
  document.body.append(panel);
  await settle();
  click(panel, '技能');
  await vi.waitFor(() =>
    expect(panel.shadowRoot!.textContent).toContain('共享安装暂不可用'),
  );
  expect(panel.shadowRoot!.textContent).toContain('共享目录正在被占用');
  expect(panel.shadowRoot!.textContent).not.toMatch(/\?{3,}/);
});
it('waits for explicit collision confirmation before retrying an exact install', async () => {
  const actions: Record<string, unknown>[] = [];
  const fetch = vi.fn(async (path: string, init?: RequestInit) => {
    if (path === 'catalog/actions') {
      const input = JSON.parse(String(init?.body));
      actions.push(input);
      return Response.json(
        input.confirmation
          ? { accepted: true, installation: row }
          : {
              confirmationRequired: true,
              confirmation: 'owner-bound-proof',
              conflicts: [{ provider: 'private' }],
            },
      );
    }
    if (path === `catalog/packages/${packageId}/versions/${versionId}`)
      return Response.json({
        id: versionId,
        packageId,
        kind: 'skill',
        state: 'published',
        version: '1.0.0',
        manifest: { description: 'Example' },
      });
    if (path.includes('/versions?'))
      return Response.json({
        items: [
          {
            id: versionId,
            packageId,
            kind: 'skill',
            state: 'published',
            version: '1.0.0',
            manifest: { description: 'Example' },
          },
        ],
      });
    if (path.startsWith('catalog/packages?') && path.includes('kind=skill'))
      return Response.json({
        items: [
          {
            id: packageId,
            kind: 'skill',
            name: 'example',
            defaultVersionId: versionId,
          },
        ],
        total: 1,
      });
    return Response.json({ items: [], total: 0 });
  });
  const panel = document.createElement('auriga-test-skills') as AurigaTeamPanel;
  panel.context = { mode: 'connect', protocol: 1, locale: 'zh-CN', fetch };
  document.body.append(panel);
  await settle();
  click(panel, '技能');
  await vi.waitFor(() =>
    expect(
      [...panel.shadowRoot!.querySelectorAll('button')].some(
        (b) => b.textContent === '安装',
      ),
    ).toBe(true),
  );
  click(panel, '安装');
  await vi.waitFor(() =>
    expect(panel.shadowRoot!.textContent).toContain('发现同名私有或宿主技能'),
  );
  expect(actions).toHaveLength(1);
  expect(panel.shadowRoot!.textContent).not.toContain('已接受请求');
  click(panel, '仍然安装此精确版本');
  await vi.waitFor(() => expect(actions).toHaveLength(2));
  expect(actions[1].confirmation).toBe('owner-bound-proof');
  expect(actions[1].key).toBe(actions[0].key);
});

it.each([
  ['guardian_missing', '可信 guardian'],
  ['trust_roots_missing', '签名信任根缺失'],
  ['trust_roots_invalid', '签名信任根配置无效'],
  ['signature_invalid', '有效期校验失败'],
  ['platform_unreachable', '平台不可达'],
  ['state_conflict', '安装状态已变化'],
])(
  'shows actionable %s diagnostics with trace instead of a generic 503 conflict',
  async (reason, hint) => {
    const panel = document.createElement(
      'auriga-test-skills',
    ) as AurigaTeamPanel;
    panel.context = {
      mode: 'connect',
      protocol: 1,
      locale: 'zh-CN',
      fetch: async (path) =>
        path.startsWith('catalog/')
          ? Response.json(
              { code: 'common.unavailable', reason, trace_id: 'a'.repeat(32) },
              { status: 503 },
            )
          : Response.json({ items: [] }),
    };
    document.body.append(panel);
    await settle();
    click(panel, '技能');
    await vi.waitFor(() =>
      expect(panel.shadowRoot!.textContent).toContain(hint),
    );
    expect(panel.shadowRoot!.textContent).toContain('a'.repeat(32));
    expect(panel.shadowRoot!.textContent).not.toContain('冲突后请重新选择版本');
  },
);
it('lays out skill cards and version permissions without exposing owner UUIDs in the list', async () => {
  const ownerId = '019a0000-0000-7000-8000-000000000012';
  const manifest = {
    description: '清理会话摘要',
    author: { displayName: '资源维护团队' },
    permissions: { tools: ['task.read'], outboundHosts: [], dataAccess: [] },
  };
  const panel = document.createElement('auriga-test-skills') as AurigaTeamPanel;
  panel.context = {
    mode: 'connect',
    protocol: 1,
    locale: 'zh-CN',
    fetch: async (path) =>
      Response.json(
        path.startsWith('catalog/packages?') && path.includes('kind=skill')
          ? {
              items: [
                {
                  id: packageId,
                  ownerId,
                  kind: 'skill',
                  name: 'clear-task-summary',
                  defaultVersionId: versionId,
                },
              ],
              total: 1,
            }
          : path.startsWith('catalog/packages/')
            ? path.includes('?')
              ? {
                  items: [
                    {
                      id: versionId,
                      packageId,
                      kind: 'skill',
                      state: 'published',
                      version: '1.0.0',
                      manifest,
                    },
                  ],
                }
              : {
                  id: versionId,
                  packageId,
                  kind: 'skill',
                  state: 'published',
                  version: '1.0.0',
                  manifest,
                }
            : path.startsWith('catalog/packages?')
              ? { items: [], total: 0 }
              : { items: [] },
      ),
  };
  document.body.append(panel);
  await settle();
  click(panel, '技能');
  await vi.waitFor(() =>
    expect(panel.shadowRoot!.textContent).toContain('清理会话摘要'),
  );
  expect(panel.shadowRoot!.querySelector('.cards .resource-card')).toBeTruthy();
  expect(panel.shadowRoot!.textContent).not.toContain(ownerId);
  expect(panel.shadowRoot!.textContent).toContain('安装');
  expect(panel.shadowRoot!.textContent).not.toContain('选择版本／更新');
});
