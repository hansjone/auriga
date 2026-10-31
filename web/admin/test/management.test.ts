import { mount, flushPromises, type VueWrapper } from '@vue/test-utils';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import ElementPlus from 'element-plus';
import { createAurigaI18n, type Member, type OrgPaths } from '@auriga/shared';
import type { Component } from 'vue';
import { createMemoryHistory, createRouter } from 'vue-router';
import NodesView from '../src/views/NodesView.vue';
import InstancesView from '../src/views/InstancesView.vue';
import MembersView from '../src/views/MembersView.vue';
import MemberRoles from '../src/admin/MemberRoles.vue';
import { apiClient, bindApiRouter } from '../src/api';
import { adminApi, type Instance, type Node } from '../src/admin/api';

const nodeId = '01900000-0000-7000-8000-000000000001';
const instanceId = '01900000-0000-7000-8000-000000000002';
const memberId = '01900000-0000-7000-8000-000000000003';
const tenantId = '01900000-0000-7000-8000-000000000004';
const timestamp = '2026-10-23T00:00:00Z';
const allPermissions = [
  'workspace.nodes.read',
  'workspace.nodes.admin',
  'workspace.instances.read',
  'workspace.instances.admin',
  'org.members.read',
  'org.members.write',
];
type Membership =
  OrgPaths['/me']['get']['responses']['200']['content']['application/json'];
let node: Node;
let instance: Instance;
let member: Member;
let permissions: string[];
let calls: { path: string; method: string; init: RequestInit }[];
let wrapper: VueWrapper | undefined;
let listEmpty: boolean;
let failure:
  | {
      path: string;
      method: string;
      status: number;
      code: string;
      retryAfter?: string;
    }
  | undefined;
let intercept:
  | ((path: string, init: RequestInit) => Promise<Response> | undefined)
  | undefined;
const json = (
  body: unknown,
  status = 200,
  headers: Record<string, string> = {},
) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json', ...headers },
  });
const memberBase = (): Member => ({
  id: memberId,
  displayName: 'Alice',
  email: 'alice@example.com',
  memberType: 'member',
  status: 'active',
  roleKeys: ['member'],
  groupIds: [],
  createdAt: timestamp,
});

beforeEach(() => {
  permissions = [...allPermissions];
  calls = [];
  listEmpty = false;
  failure = undefined;
  intercept = undefined;
  member = memberBase();
  node = {
    id: nodeId,
    name: 'Node A',
    hostname: 'node-a.example.com',
    os: 'linux',
    arch: 'amd64',
    agentVersion: '1.0.0',
    drivers: ['docker'],
    status: 'online',
    schedulable: true,
    labels: { template: 'example' },
    capacity: { cpuMillis: 2000, memoryMb: 4096, diskGb: 50, maxInstances: 4 },
    joinedAt: timestamp,
  };
  instance = {
    id: instanceId,
    tenantId,
    ownerId: memberId,
    name: 'Workspace A',
    runtime: 'docker',
    desired: 'stopped',
    phase: 'stopped',
    ready: false,
    generation: 3,
    observedGeneration: 3,
    pendingRestart: false,
    alwaysOn: false,
    dshVersion: '1.0.0',
    resources: { cpuMillis: 500, memoryMb: 1024, diskGb: 10 },
    node: { id: nodeId, name: node.name },
    error: null,
    entryUrl: `/open/${instanceId}`,
    createdAt: timestamp,
    updatedAt: timestamp,
  };
  document.head.innerHTML = '<meta name="auriga-access-mode" content="domain">';
  document.cookie = 'auriga_csrf=test-csrf; path=/';
  apiClient.resetSession();
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init: RequestInit = {}) => {
      const url = new URL(String(input), location.href);
      const path = url.pathname + url.search;
      const method = init.method || 'GET';
      calls.push({ path, method, init });
      const custom = intercept?.(path, init);
      if (custom) return custom;
      if (failure && path === failure.path && method === failure.method)
        return json(
          {
            status: failure.status,
            code: failure.code,
            title: 'Request rejected',
          },
          failure.status,
          failure.retryAfter ? { 'Retry-After': failure.retryAfter } : {},
        );
      if (path === '/api/org/v1/me') {
        const membership: Membership = {
          member,
          tenant: {
            id: tenantId,
            displayName: 'Example',
            slug: 'example',
            locale: 'zh-CN',
            hosts: [],
            createdAt: timestamp,
          },
          permissions,
        };
        return json(membership);
      }
      if (path === '/api/workspace/v1/admin/nodes')
        return json({ items: listEmpty ? [] : [node] });
      if (path === `/api/workspace/v1/admin/nodes/${nodeId}`) {
        if (method === 'PATCH')
          node = { ...node, ...JSON.parse(String(init.body)) };
        return json(node);
      }
      if (path === `/api/workspace/v1/admin/nodes/${nodeId}/drain`) {
        node = { ...node, status: 'draining', schedulable: false };
        return json(node, 202);
      }
      if (path.startsWith('/api/workspace/v1/admin/instances?'))
        return json({ items: listEmpty ? [] : [instance] });
      if (path === `/api/workspace/v1/instances/${instanceId}`)
        return json(instance);
      if (path === `/api/workspace/v1/admin/instances/${instanceId}`) {
        instance = {
          ...instance,
          ...JSON.parse(String(init.body)),
          generation: instance.generation + 1,
        };
        return json(instance);
      }
      if (path.startsWith(`/api/workspace/v1/instances/${instanceId}/`)) {
        const stop = path.endsWith('/stop');
        instance = {
          ...instance,
          desired: stop ? 'stopped' : 'running',
          phase: stop ? 'stopping' : 'starting',
          ready: false,
        };
        return json(instance, 202);
      }
      if (path.startsWith('/api/org/v1/admin/members?'))
        return json({ items: listEmpty ? [] : [member] });
      if (path === `/api/org/v1/admin/members/${memberId}`) return json(member);
      if (path === `/api/org/v1/admin/members/${memberId}/disable`) {
        member = { ...member, status: 'disabled' };
        return json(member);
      }
      if (path === `/api/org/v1/admin/members/${memberId}/enable`) {
        member = { ...member, status: 'active' };
        return json(member);
      }
      throw new Error(`Unexpected request: ${method} ${path}`);
    }),
  );
});
afterEach(() => {
  wrapper?.unmount();
  wrapper = undefined;
  vi.useRealTimers();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  document.head.innerHTML = '';
  document.cookie = 'auriga_csrf=; Max-Age=0; path=/';
});

async function render(component: Component, locale: 'zh-CN' | 'en' = 'zh-CN') {
  wrapper = mount(component, {
    global: {
      plugins: [createAurigaI18n(locale), ElementPlus],
      stubs: { teleport: true },
    },
  });
  await flushPromises();
  return wrapper;
}
async function click(text: string) {
  const button = wrapper!.findAll('button').find((b) => b.text() === text);
  expect(button, `button ${text}`).toBeDefined();
  await button!.trigger('click');
  await flushPromises();
}
async function confirm() {
  await wrapper!.get('[data-testid="confirm-action"]').trigger('click');
  await flushPromises();
}
const writes = () => calls.filter((call) => call.method !== 'GET');

describe('administrative resource workflows', () => {
  it('reports failed member readback after a role write without claiming refreshed permissions', async () => {
    await render(MembersView);
    await click('详情');
    failure = {
      path: `/api/org/v1/admin/members/${memberId}`,
      method: 'GET',
      status: 503,
      code: 'common.unavailable',
    };
    wrapper!.findComponent(MemberRoles).vm.$emit('updated');
    await flushPromises();
    expect(wrapper!.text()).toContain('操作已提交，但刷新失败');
    expect(wrapper!.text()).not.toContain('成员角色已更新，并重新读取当前权限');
  });
  it('edits node labels using merge-patch, CSRF and explicit confirmation', async () => {
    await render(NodesView);
    await click('详情');
    await wrapper!.get('input[aria-label="名称"]').setValue('Node B');
    await wrapper!.get('input[aria-label="标签值"]').setValue('new-template');
    await wrapper!.get('form').trigger('submit');
    await flushPromises();
    expect(writes()).toHaveLength(0);
    await confirm();
    expect(writes()).toHaveLength(1);
    const request = writes()[0];
    expect(request.path).toBe(`/api/workspace/v1/admin/nodes/${nodeId}`);
    expect(request.method).toBe('PATCH');
    expect(new Headers(request.init.headers).get('Content-Type')).toBe(
      'application/merge-patch+json',
    );
    expect(new Headers(request.init.headers).get('X-Auriga-CSRF')).toBe(
      'test-csrf',
    );
    expect(request.init.credentials).toBe('same-origin');
    expect(request.init.redirect).toBe('error');
    expect(JSON.parse(String(request.init.body))).toEqual({
      name: 'Node B',
      schedulable: true,
      labels: { template: 'new-template' },
    });
    expect(wrapper!.text()).toContain('已保存服务端配置');
    expect(wrapper!.get('h2').text()).toBe('Node B');
  });
  it('cancels drain without writing and distinguishes accepted from complete', async () => {
    await render(NodesView);
    await click('详情');
    await click('排空节点');
    await click('取消');
    expect(writes()).toHaveLength(0);
    await click('排空节点');
    await confirm();
    expect(writes()).toHaveLength(1);
    expect(wrapper!.text()).toContain('排空中');
    expect(wrapper!.text()).toContain('接受请求不代表执行完成');
    await click('刷新');
    expect(wrapper!.get('.details').text()).toContain('排空中');
  });
  it('rejects duplicate labels before a request', async () => {
    await render(NodesView);
    await click('详情');
    await click('添加标签');
    await wrapper!
      .findAll('input[aria-label="标签名"]')[1]
      .setValue('template');
    await wrapper!.get('form').trigger('submit');
    await flushPromises();
    expect(wrapper!.text()).toContain('标签名不能为空或重复');
    expect(writes()).toHaveLength(0);
  });
  it('starts an instance then reads its actual ready state on refresh', async () => {
    await render(InstancesView);
    await click('详情');
    await click('启动');
    expect(writes()).toHaveLength(0);
    await confirm();
    expect(writes()[0].path).toBe(
      `/api/workspace/v1/instances/${instanceId}/start`,
    );
    expect(wrapper!.text()).toContain('启动中');
    expect(wrapper!.text()).toContain('操作已接受');
    instance = {
      ...instance,
      phase: 'running',
      ready: true,
      observedGeneration: 3,
    };
    await click('刷新');
    expect(wrapper!.get('.details').text()).toContain('运行中');
    expect(wrapper!.findAll('a')).toHaveLength(0);
  });
  it.each(['停止', '重启'] as const)(
    'confirms instance %s with the existing endpoint',
    async (label) => {
      instance = {
        ...instance,
        desired: 'running',
        phase: 'running',
        ready: true,
      };
      await render(InstancesView);
      await click('详情');
      await click(label);
      await confirm();
      expect(writes()[0].path).toBe(
        `/api/workspace/v1/instances/${instanceId}/${label === '停止' ? 'stop' : 'restart'}`,
      );
    },
  );
  it('sends the selected generation for always-on and never retries a conflict automatically', async () => {
    failure = {
      path: `/api/workspace/v1/admin/instances/${instanceId}`,
      method: 'PATCH',
      status: 412,
      code: 'common.precondition_failed',
    };
    await render(InstancesView);
    await click('详情');
    await click('常驻运行: 是');
    await confirm();
    expect(new Headers(writes()[0].init.headers).get('If-Match')).toBe('"3"');
    expect(wrapper!.text()).toContain('common.precondition_failed');
    expect(wrapper!.text()).toContain('请刷新详情后重试');
    expect(writes()).toHaveLength(1);
    expect(instance.alwaysOn).toBe(false);
  });
  it('uses the list projection for read-only instance details without calling a more privileged endpoint', async () => {
    permissions = ['workspace.instances.read'];
    await render(InstancesView);
    await click('详情');
    expect(wrapper!.text()).toContain('详情来自管理列表');
    expect(
      calls.some(
        (call) => call.path === `/api/workspace/v1/instances/${instanceId}`,
      ),
    ).toBe(false);
    expect(wrapper!.find('.actions').exists()).toBe(false);
    instance = {
      ...instance,
      phase: 'failed',
      error: { code: 'common.unavailable' },
    };
    await click('刷新');
    expect(wrapper!.text()).toContain('common.unavailable');
  });
  it('disables and enables a member with returned status feedback', async () => {
    await render(MembersView);
    await click('详情');
    await click('停用成员');
    await confirm();
    expect(wrapper!.text()).toContain('成员状态已更新');
    expect(member.status).toBe('disabled');
    await click('启用成员');
    await confirm();
    expect(member.status).toBe('active');
    expect(writes()).toHaveLength(2);
  });
  it('shows last-owner rejection without claiming success', async () => {
    failure = {
      path: `/api/org/v1/admin/members/${memberId}/disable`,
      method: 'POST',
      status: 409,
      code: 'org.last_owner',
    };
    await render(MembersView);
    await click('详情');
    await click('停用成员');
    await confirm();
    expect(wrapper!.text()).toContain('org.last_owner');
    expect(wrapper!.text()).not.toContain('成员状态已更新');
    expect(member.status).toBe('active');
  });
  it('rechecks revoked permission before writing', async () => {
    await render(NodesView);
    await click('详情');
    await click('排空节点');
    permissions = ['workspace.nodes.read'];
    await confirm();
    expect(writes()).toHaveLength(0);
    expect(wrapper!.text()).toContain('没有此操作所需权限');
  });
  it('handles a server-side 403 after the permission check and hides further writes', async () => {
    failure = {
      path: `/api/workspace/v1/admin/nodes/${nodeId}/drain`,
      method: 'POST',
      status: 403,
      code: 'common.forbidden',
    };
    await render(NodesView);
    await click('详情');
    await click('排空节点');
    await confirm();
    expect(wrapper!.text()).toContain('common.forbidden');
    expect(wrapper!.find('.actions').exists()).toBe(false);
    expect(writes()).toHaveLength(1);
  });
  it('does not request a list when read permission is missing', async () => {
    permissions = [];
    await render(InstancesView);
    expect(calls.map((call) => call.path)).toEqual(['/api/org/v1/me']);
    expect(wrapper!.text()).toContain('没有此操作所需权限');
  });
  it.each([
    [429, 'common.rate_limited'],
    [503, 'common.unavailable'],
  ] as const)(
    'shows status %s and Retry-After without replaying a mutation',
    async (status, code) => {
      failure = {
        path: `/api/workspace/v1/instances/${instanceId}/start`,
        method: 'POST',
        status,
        code,
        retryAfter: '30',
      };
      await render(InstancesView);
      await click('详情');
      await click('启动');
      await confirm();
      expect(wrapper!.text()).toContain(code);
      expect(wrapper!.text()).toContain('30');
      expect(writes()).toHaveLength(1);
    },
  );
  it.each([NodesView, InstancesView, MembersView])(
    'renders empty data separately from errors',
    async (component) => {
      listEmpty = true;
      await render(component);
      expect(wrapper!.text()).toContain('暂无数据');
      expect(wrapper!.find('[role="alert"]').exists()).toBe(false);
    },
  );
  it('retries a failed list without leaving stale rows or a misleading empty state', async () => {
    failure = {
      path: '/api/workspace/v1/admin/nodes',
      method: 'GET',
      status: 503,
      code: 'common.unavailable',
    };
    await render(NodesView);
    expect(wrapper!.text()).toContain('common.unavailable');
    expect(wrapper!.text()).not.toContain('暂无数据');
    failure = undefined;
    await click('重试');
    expect(wrapper!.text()).toContain('Node A');
    expect(wrapper!.find('[role="alert"]').exists()).toBe(false);
  });
  it('renders loading state until list resolves', async () => {
    let resolve!: (response: Response) => void;
    intercept = (path) =>
      path === '/api/workspace/v1/admin/nodes'
        ? new Promise((done) => {
            resolve = done;
          })
        : undefined;
    await render(NodesView);
    expect(wrapper!.text()).toContain('加载中');
    expect(wrapper!.text()).not.toContain('暂无数据');
    resolve(json({ items: [node] }));
    await flushPromises();
    expect(wrapper!.text()).not.toContain('加载中');
  });
  it('ignores repeated confirmation while the write is in flight', async () => {
    let resolve!: (response: Response) => void;
    intercept = (path) =>
      path.endsWith('/drain')
        ? new Promise((done) => {
            resolve = done;
          })
        : undefined;
    await render(NodesView);
    await click('详情');
    await click('排空节点');
    await confirm();
    await confirm();
    expect(writes()).toHaveLength(1);
    resolve(json({ ...node, status: 'draining' }, 202));
    await flushPromises();
    expect(wrapper!.text()).toContain('排空中');
  });
  it('keeps opaque pagination cursors and returns to the first page', async () => {
    intercept = (path) => {
      if (path === '/api/workspace/v1/admin/instances?limit=50')
        return Promise.resolve(
          json({ items: [instance], nextCursor: 'opaque+/cursor' }),
        );
      if (path.includes('cursor=opaque%2B%2Fcursor'))
        return Promise.resolve(
          json({ items: [{ ...instance, name: 'Workspace B' }] }),
        );
    };
    await render(InstancesView);
    await click('下一页');
    expect(wrapper!.text()).toContain('Workspace B');
    await click('上一页');
    expect(wrapper!.text()).toContain('Workspace A');
    expect(
      calls.filter(
        (call) => call.path === '/api/workspace/v1/admin/instances?limit=50',
      ),
    ).toHaveLength(2);
  });
  it('uses English management copy', async () => {
    await render(InstancesView, 'en');
    await click('Details');
    await click('Start');
    expect(wrapper!.text()).toContain('Confirm operation');
    await confirm();
    expect(wrapper!.text()).toContain('Operation accepted');
  });
  it('discards a stale member response after a new search and cancels search on unmount', async () => {
    vi.useFakeTimers();
    let resolveOld!: (response: Response) => void;
    intercept = (path) => {
      if (path === '/api/org/v1/admin/members?limit=50')
        return new Promise((done) => {
          resolveOld = done;
        });
      if (path.includes('/admin/members?q=bob'))
        return Promise.resolve(
          json({ items: [{ ...member, displayName: 'Bob' }] }),
        );
    };
    await render(MembersView);
    await wrapper!.get('input').setValue('bob');
    await vi.advanceTimersByTimeAsync(251);
    await flushPromises();
    expect(wrapper!.text()).toContain('Bob');
    resolveOld(json({ items: [member] }));
    await flushPromises();
    expect(wrapper!.text()).toContain('Bob');
    expect(wrapper!.text()).not.toContain('Alice');
    await wrapper!.get('input').setValue('alice');
    const before = calls.length;
    wrapper!.unmount();
    wrapper = undefined;
    await vi.advanceTimersByTimeAsync(251);
    expect(calls).toHaveLength(before);
  });
  it('clears stale detail data on a 404 and allows reopening after refresh', async () => {
    failure = {
      path: `/api/workspace/v1/admin/nodes/${nodeId}`,
      method: 'GET',
      status: 404,
      code: 'common.not_found',
    };
    await render(NodesView);
    await click('详情');
    expect(wrapper!.text()).toContain('common.not_found');
    expect(wrapper!.find('.details').exists()).toBe(false);
    failure = undefined;
    await click('刷新');
    await click('详情');
    expect(wrapper!.get('h2').text()).toBe('Node A');
  });
  it.each([
    [NodesView, 'workspace.nodes.read'],
    [MembersView, 'org.members.read'],
  ] as const)(
    'does not expose write actions to read-only users',
    async (component, permission) => {
      permissions = [permission];
      await render(component);
      await click('详情');
      expect(wrapper!.text()).toContain('当前权限仅允许查看');
      expect(wrapper!.find('.actions').exists()).toBe(false);
      expect(writes()).toHaveLength(0);
    },
  );
  it('shows a failed membership request and does not guess permissions', async () => {
    failure = {
      path: '/api/org/v1/me',
      method: 'GET',
      status: 503,
      code: 'common.unavailable',
    };
    await render(MembersView);
    expect(calls).toHaveLength(1);
    expect(wrapper!.text()).toContain('common.unavailable');
  });
  it('uses the shared 401 handling for an expired session', async () => {
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: '/instances', component: InstancesView },
        { path: '/login', component: { template: '<div />' } },
      ],
    });
    await router.push('/instances');
    bindApiRouter(router);
    failure = {
      path: '/api/org/v1/me',
      method: 'GET',
      status: 401,
      code: 'common.unauthenticated',
    };
    await render(InstancesView);
    expect(wrapper!.text()).toContain('common.unauthenticated');
    expect(writes()).toHaveLength(0);
    expect(router.currentRoute.value.path).toBe('/login');
    expect(router.currentRoute.value.query.returnTo).toBe('/instances');
  });
  it('registers in-memory CSRF in IP mode before the existing management mutation', async () => {
    document.head.innerHTML = '<meta name="auriga-access-mode" content="ip">';
    let token = '';
    intercept = (path, init) => {
      if (path !== '/api/iam/v1/admin/session/csrf-tokens') return;
      token = JSON.parse(String(init.body)).token;
      expect(new Headers(init.headers).get('X-Auriga-CSRF-Bootstrap')).toBe(
        'admin-session-v1',
      );
      return Promise.resolve(
        json({
          token,
          expiresAt: new Date(Date.now() + 600_000).toISOString(),
        }),
      );
    };
    await render(NodesView);
    await click('详情');
    await click('排空节点');
    await confirm();
    expect(writes().map((call) => call.path)).toEqual([
      '/api/iam/v1/admin/session/csrf-tokens',
      `/api/workspace/v1/admin/nodes/${nodeId}/drain`,
    ]);
    expect(token).toMatch(/^[A-Za-z0-9_-]{43}$/);
    expect(new Headers(writes()[1].init.headers).get('X-Auriga-CSRF')).toBe(
      token,
    );
    expect(token).not.toBe('test-csrf');
    expect(document.cookie).not.toContain(token);
  });
  it('does not mutate if IP CSRF registration is rejected', async () => {
    document.head.innerHTML = '<meta name="auriga-access-mode" content="ip">';
    failure = {
      path: '/api/iam/v1/admin/session/csrf-tokens',
      method: 'POST',
      status: 403,
      code: 'common.forbidden',
    };
    await render(NodesView);
    await click('详情');
    await click('排空节点');
    await confirm();
    expect(writes().map((call) => call.path)).toEqual([
      '/api/iam/v1/admin/session/csrf-tokens',
    ]);
    expect(wrapper!.text()).toContain('common.forbidden');
    expect(node.status).toBe('online');
  });
  it('does not send an administrative write if access mode is invalid', async () => {
    await render(NodesView);
    await click('详情');
    await click('排空节点');
    document.head.innerHTML = '';
    await confirm();
    expect(writes()).toHaveLength(0);
    expect(wrapper!.find('[role="alert"]').exists()).toBe(true);
  });
  it('encodes identifiers rather than allowing path injection', async () => {
    intercept = (path) =>
      path.includes('/admin/nodes/') ? Promise.resolve(json(node)) : undefined;
    await adminApi.node('a/b?c');
    expect(calls[0].path).toBe('/api/workspace/v1/admin/nodes/a%2Fb%3Fc');
  });
});
