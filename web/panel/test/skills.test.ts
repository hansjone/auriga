import { afterEach, expect, it, vi } from 'vitest';
import { AurigaTeamPanel } from '../src/TeamPanel';
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
async function settle() {
  for (let n = 0; n < 8; n++) await Promise.resolve();
}
function click(panel: AurigaTeamPanel, label: string) {
  const button = [...panel.shadowRoot!.querySelectorAll('button')].find(
    (b) => b.textContent === label,
  );
  expect(button, label).toBeTruthy();
  button!.click();
}
it('separates Skill reads from legacy plugin APIs, installs the selected exact version and keeps 202 distinct from applied', async () => {
  let installed = false;
  const fetch = vi.fn(async (path: string, init?: RequestInit) => {
    if (path.startsWith('/api/resource/v1/packages'))
      return Response.json({ items: [] });
    if (path === 'catalog/packages?limit=50')
      return Response.json({
        items: [{ id: packageId, kind: 'skill', name: 'example' }],
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
    expect(panel.shadowRoot!.textContent).toContain('选择版本／更新'),
  );
  const legacyCount = fetch.mock.calls.filter(([p]) =>
    p.startsWith('/api/'),
  ).length;
  click(panel, '选择版本／更新');
  await vi.waitFor(() =>
    expect(panel.shadowRoot!.textContent).toContain('安装此精确版本'),
  );
  expect(panel.shadowRoot!.querySelector('img')).toBeNull();
  expect(panel.shadowRoot!.textContent).toContain('<img src=x');
  click(panel, '安装此精确版本');
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
      items: path.startsWith('catalog/packages')
        ? [{ id: packageId, kind: 'skill', name: 'new-identity' }]
        : [],
    }),
  );
  panel.context = { mode: 'connect', protocol: 1, fetch: nextFetch };
  await settle();
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
    return Response.json({
      items:
        path === 'catalog/packages?limit=50'
          ? [{ id: packageId, kind: 'skill', name: 'example' }]
          : [],
    });
  });
  const panel = document.createElement('auriga-test-skills') as AurigaTeamPanel;
  panel.context = { mode: 'connect', protocol: 1, locale: 'zh-CN', fetch };
  document.body.append(panel);
  await settle();
  click(panel, '技能');
  await vi.waitFor(() =>
    expect(panel.shadowRoot!.textContent).toContain('选择版本／更新'),
  );
  click(panel, '选择版本／更新');
  await vi.waitFor(() =>
    expect(panel.shadowRoot!.textContent).toContain('安装此精确版本'),
  );
  click(panel, '安装此精确版本');
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
