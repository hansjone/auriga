import { mount, flushPromises, type VueWrapper } from '@vue/test-utils';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createMemoryHistory, createRouter } from 'vue-router';
import { defineComponent, type Component } from 'vue';
import ElementPlus from 'element-plus';
import { createAurigaI18n, type Member, type OrgPaths } from '@auriga/shared';
import { apiClient, bindApiRouter } from '../src/api';
import { orgAdminApi, type Group, type Invitation } from '../src/admin/orgApi';
import { rolesApi, type Role } from '../src/admin/rolesApi';
import InvitationsView from '../src/views/InvitationsView.vue';
import GroupsView from '../src/views/GroupsView.vue';

const groupId = '01900000-0000-7000-8000-000000000001';
const memberId = '01900000-0000-7000-8000-000000000002';
const invitationId = '01900000-0000-7000-8000-000000000003';
const parentId = '01900000-0000-7000-8000-000000000004';
const tenantId = '01900000-0000-7000-8000-000000000005';
const timestamp = '2026-10-24T00:00:00Z';
const credential = 'invitation-example-credential';
const permissionsAll = [
  'org.roles.read',
  'org.invitations.read',
  'org.invitations.write',
  'org.groups.read',
  'org.groups.write',
  'org.members.read',
];
type Membership =
  OrgPaths['/me']['get']['responses']['200']['content']['application/json'];
const invitationRoles: Role[] = [
  {
    id: 'member-role',
    key: 'member',
    name: { en: 'Member', zh: '普通成员' },
    builtin: true,
    permissions: [],
    bindingCount: 0,
  },
  {
    id: 'custom-role',
    key: 'reader',
    name: { en: 'Reader', zh: '只读角色' },
    builtin: false,
    permissions: ['org.members.read'],
    bindingCount: 0,
  },
  {
    id: 'owner-role',
    key: 'owner',
    name: { en: 'Owner', zh: '所有者' },
    builtin: true,
    permissions: ['system.write'],
    bindingCount: 1,
  },
];
let permissions: string[];
let invitations: Invitation[];
let groups: Group[];
let members: Member[];
let calls: { path: string; method: string; init: RequestInit }[];
let wrapper: VueWrapper | undefined;
let clipboardWrite: ReturnType<typeof vi.fn>;
let returnedUrl: string;
let fail:
  { path: string; method: string; status: number; code: string } | undefined;
let intercept:
  | ((path: string, init: RequestInit) => Promise<Response> | undefined)
  | undefined;
const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
const noContent = () => new Response(null, { status: 204 });
const writes = () => calls.filter((call) => call.method !== 'GET');

beforeEach(() => {
  vi.spyOn(rolesApi, 'list').mockResolvedValue({ items: invitationRoles });
  permissions = [...permissionsAll];
  calls = [];
  fail = undefined;
  intercept = undefined;
  returnedUrl = `https://portal.example.com/invite/${credential}`;
  invitations = [
    {
      id: invitationId,
      status: 'active',
      memberType: 'member',
      roleIds: [],
      groupIds: [],
      providerIds: [],
      uses: 0,
      maxUses: 1,
      expiresAt: '2026-10-31T00:00:00Z',
      createdBy: memberId,
      createdAt: timestamp,
    },
  ];
  groups = [
    {
      id: groupId,
      name: 'Engineering',
      description: 'Example group',
      source: 'manual',
      memberCount: 1,
      createdAt: timestamp,
    },
    {
      id: parentId,
      name: 'Example parent',
      source: 'manual',
      memberCount: 0,
      createdAt: timestamp,
    },
  ];
  members = [
    {
      id: memberId,
      displayName: 'Alice',
      email: 'alice@example.com',
      memberType: 'member',
      status: 'active',
      roleKeys: ['member'],
      groupIds: [],
      createdAt: timestamp,
    },
  ];
  document.head.innerHTML = '<meta name="auriga-access-mode" content="domain">';
  document.cookie = 'auriga_csrf=example-csrf; path=/';
  apiClient.resetSession();
  clipboardWrite = vi.fn().mockResolvedValue(undefined);
  const clipboard = { writeText: clipboardWrite };
  vi.stubGlobal(
    'navigator',
    new Proxy(navigator, {
      get(target, key) {
        return key === 'clipboard'
          ? clipboard
          : Reflect.get(target, key, target);
      },
    }),
  );
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init: RequestInit = {}) => {
      const url = new URL(String(input), location.href);
      const path = url.pathname + url.search;
      const method = init.method || 'GET';
      calls.push({ path, method, init });
      const custom = intercept?.(path, init);
      if (custom) return custom;
      if (fail?.path === path && fail.method === method)
        return json(
          {
            status: fail.status,
            code: fail.code,
            title: 'Rejected',
            detail: 'Sensitive detail must not be displayed',
          },
          fail.status,
        );
      if (path === '/api/org/v1/me') {
        const body: Membership = {
          member: members[0],
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
        return json(body);
      }
      if (path === '/api/org/v1/admin/invitations' && method === 'POST')
        return json(
          {
            ...invitations[0],
            ...JSON.parse(String(init.body)),
            code: credential,
            url: returnedUrl,
          },
          201,
        );
      if (path.startsWith('/api/org/v1/admin/invitations?'))
        return json({
          items: invitations.filter(
            (item) =>
              !url.searchParams.get('status') ||
              item.status === url.searchParams.get('status'),
          ),
        });
      if (
        path === `/api/org/v1/admin/invitations/${invitationId}` &&
        method === 'DELETE'
      ) {
        invitations = invitations.map((item) => ({
          ...item,
          status: 'revoked',
        }));
        return noContent();
      }
      if (path === '/api/org/v1/admin/groups') {
        if (method === 'POST') {
          const body = JSON.parse(String(init.body));
          const group = {
            id: '01900000-0000-7000-8000-000000000006',
            ...body,
            source: 'manual',
            memberCount: 0,
            createdAt: timestamp,
          };
          groups.push(group);
          return json(group, 201);
        }
        return json({ items: groups });
      }
      const group = groups.find(
        (item) => path === `/api/org/v1/admin/groups/${item.id}`,
      );
      if (group) {
        if (method === 'PATCH') {
          Object.assign(group, JSON.parse(String(init.body)));
          return json(group);
        }
        if (method === 'DELETE') {
          groups = groups.filter((item) => item.id !== group.id);
          return noContent();
        }
        return json(group);
      }
      if (path.match(/\/groups\/[^/]+\/members\?/))
        return json({
          items: members.filter((item) =>
            item.groupIds.includes(url.pathname.split('/')[6]),
          ),
        });
      if (path.startsWith('/api/org/v1/admin/members?'))
        return json({ items: members });
      if (path === `/api/org/v1/admin/groups/${groupId}/members/${memberId}`) {
        members[0].groupIds = method === 'PUT' ? [groupId] : [];
        groups[0].memberCount = method === 'PUT' ? 1 : 0;
        return noContent();
      }
      throw new Error(`Unexpected request ${method} ${path}`);
    }),
  );
});
afterEach(() => {
  wrapper?.unmount();
  wrapper = undefined;
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  document.head.innerHTML = '';
  document.body.innerHTML = '';
  document.cookie = 'auriga_csrf=; Max-Age=0; path=/';
});
async function render(component: Component, locale: 'zh-CN' | 'en' = 'zh-CN') {
  wrapper = mount(component, {
    attachTo: document.body,
    global: {
      plugins: [createAurigaI18n(locale), ElementPlus],
    },
  });
  await flushPromises();
  return wrapper;
}
async function click(label: string) {
  const button = wrapper!
    .findAll('button')
    .find((item) => item.text() === label);
  expect(button, label).toBeDefined();
  await button!.trigger('click');
  await flushPromises();
}
async function confirm() {
  await wrapper!.get('[data-testid="confirm-action"]').trigger('click');
  await flushPromises();
}
async function choose(label: string, value: string | string[]) {
  const select = wrapper!
    .findAllComponents({ name: 'ElSelect' })
    .find((item) => item.props('ariaLabel') === label);
  expect(select, label).toBeDefined();
  select!.vm.$emit('update:modelValue', value);
  await flushPromises();
}
async function createInvitation() {
  await click('创建成员邀请');
  await wrapper!.findAll('form').at(-1)!.trigger('submit');
  await flushPromises();
  await confirm();
}

describe('invitation management', () => {
  it('submits the chosen external type and multiple initial roles', async () => {
    await render(InvitationsView);
    await click('创建成员邀请');
    await choose('成员类型', 'external');
    await choose('初始角色', ['member-role', 'custom-role']);
    await wrapper!.findAll('form').at(-1)!.trigger('submit');
    await flushPromises();
    await confirm();
    expect(JSON.parse(String(writes()[0].init.body))).toMatchObject({
      memberType: 'external',
      roleIds: ['member-role', 'custom-role'],
    });
  });
  it('blocks unavailable defaults instead of silently inviting with no role', async () => {
    vi.mocked(rolesApi.list).mockResolvedValue({ items: [] });
    await render(InvitationsView);
    await click('创建成员邀请');
    await wrapper!.findAll('form').at(-1)!.trigger('submit');
    await flushPromises();
    expect(writes()).toHaveLength(0);
    expect(wrapper!.text()).toContain('普通成员角色不可用');
  });
  it('blocks role option failures and supports an explicit retry', async () => {
    vi.mocked(rolesApi.list).mockRejectedValueOnce(new Error('unavailable'));
    await render(InvitationsView);
    await click('创建成员邀请');
    await wrapper!.findAll('form').at(-1)!.trigger('submit');
    await flushPromises();
    expect(writes()).toHaveLength(0);
    await click('重新加载选项');
    await wrapper!.findAll('form').at(-1)!.trigger('submit');
    await flushPromises();
    await confirm();
    expect(writes()).toHaveLength(1);
  });
  it('requires role-read and disables roles exceeding the operator permissions', async () => {
    await render(InvitationsView);
    await click('创建成员邀请');
    const select = wrapper!
      .findAllComponents({ name: 'ElSelect' })
      .find((s) => s.props('ariaLabel') === '初始角色')!;
    expect(select.props('options')).toContainEqual(
      expect.objectContaining({ value: 'owner-role', disabled: true }),
    );
    select.vm.$emit('update:modelValue', ['owner-role']);
    await flushPromises();
    await wrapper!.findAll('form').at(-1)!.trigger('submit');
    await flushPromises();
    expect(writes()).toHaveLength(0);
  });
  it('does not fetch roles or submit defaults without role-read', async () => {
    permissions = permissions.filter((p) => p !== 'org.roles.read');
    await render(InvitationsView);
    await click('创建成员邀请');
    expect(rolesApi.list).not.toHaveBeenCalled();
    await wrapper!.findAll('form').at(-1)!.trigger('submit');
    await flushPromises();
    expect(writes()).toHaveLength(0);
  });
  it.each([
    'https://portal.example.com:9443/team/invite/example%2Bcode?source=invitation',
    'http://192.0.2.10:8080/invite/example-code',
    'http://192.0.2.10:8080/portal/path/invite/example-code',
  ])(
    'copies the exact official invitation URL %s without deriving it from the admin origin',
    async (value) => {
      returnedUrl = value;
      await render(InvitationsView);
      await createInvitation();
      expect(wrapper!.get('[data-testid="invitation-url"]').text()).toBe(value);
      await click('复制邀请链接');
      expect(clipboardWrite).toHaveBeenLastCalledWith(value);
      expect(wrapper!.find('a').exists()).toBe(false);
      expect(location.href).not.toContain(credential);
      await click('复制邀请凭据');
      expect(clipboardWrite).toHaveBeenLastCalledWith(credential);
      await click('关闭并清除凭据');
      expect(wrapper!.text()).not.toContain(value);
      expect(wrapper!.text()).not.toContain(credential);
    },
  );
  it('clears the invitation URL on explicit refresh and on starting another invitation', async () => {
    await render(InvitationsView);
    await createInvitation();
    await click('刷新');
    expect(wrapper!.text()).not.toContain(returnedUrl);
    await createInvitation();
    await click('创建成员邀请');
    expect(wrapper!.text()).not.toContain(returnedUrl);
    expect(wrapper!.text()).not.toContain(credential);
  });
  it('clears the result when a filter changes or an invitation is revoked', async () => {
    await render(InvitationsView);
    await createInvitation();
    await choose('状态', 'active');
    expect(wrapper!.text()).not.toContain(returnedUrl);
    await createInvitation();
    await click('撤销邀请');
    expect(wrapper!.text()).not.toContain(returnedUrl);
    expect(wrapper!.text()).not.toContain(credential);
  });
  it('preserves a one-time result after list failure and retries only GET without discarding it', async () => {
    let created = false;
    intercept = (path, init) => {
      if (path === '/api/org/v1/admin/invitations' && init.method === 'POST') {
        created = true;
        return Promise.resolve(
          json({ ...invitations[0], code: credential, url: returnedUrl }, 201),
        );
      }
      if (created && path.includes('/admin/invitations?'))
        return Promise.resolve(
          json(
            { status: 503, code: 'common.unavailable', title: 'Unavailable' },
            503,
          ),
        );
    };
    await render(InvitationsView);
    await createInvitation();
    expect(wrapper!.text()).toContain('勿重复提交');
    expect(wrapper!.text()).toContain(returnedUrl);
    intercept = undefined;
    const retry = wrapper!
      .findAllComponents({ name: 'ElDialog' })
      .at(-1)!
      .findAll('button')
      .find((button) => button.text() === '重试');
    expect(retry).toBeDefined();
    await retry!.trigger('click');
    await flushPromises();
    expect(writes()).toHaveLength(1);
    expect(wrapper!.text()).toContain(returnedUrl);
    await click('复制邀请链接');
    expect(clipboardWrite).toHaveBeenLastCalledWith(returnedUrl);
  });
  it.each([401, 403])(
    'clears code and URL when a result readback returns %s',
    async (status) => {
      let created = false;
      intercept = (path, init) => {
        if (
          path === '/api/org/v1/admin/invitations' &&
          init.method === 'POST'
        ) {
          created = true;
          return Promise.resolve(
            json(
              { ...invitations[0], code: credential, url: returnedUrl },
              201,
            ),
          );
        }
        if (created && path.includes('/admin/invitations?'))
          return Promise.resolve(
            json(
              {
                status,
                code:
                  status === 401
                    ? 'common.unauthenticated'
                    : 'common.forbidden',
                title: 'Denied',
              },
              status,
            ),
          );
      };
      await render(InvitationsView);
      await createInvitation();
      expect(wrapper!.text()).not.toContain(returnedUrl);
      expect(wrapper!.text()).not.toContain(credential);
      expect(
        wrapper!
          .findAll('.invitation-code')
          .every((node) => node.text() === ''),
      ).toBe(true);
    },
  );
  it('reports unavailable trusted portal configuration without a fake result or automatic POST replay', async () => {
    fail = {
      path: '/api/org/v1/admin/invitations',
      method: 'POST',
      status: 503,
      code: 'common.unavailable',
    };
    await render(InvitationsView);
    await createInvitation();
    expect(wrapper!.text()).toContain('受信门户配置');
    expect(wrapper!.text()).not.toContain('邀请已创建');
    expect(wrapper!.text()).not.toContain(returnedUrl);
    expect(wrapper!.text()).not.toContain(credential);
    expect(writes()).toHaveLength(1);
  });
  it.each([
    'javascript:alert(1)',
    'data:text/html,<b>example</b>',
    '/invite/relative',
  ])(
    'does not present an unsupported returned URL as a usable invitation: %s',
    async (value) => {
      returnedUrl = value;
      await render(InvitationsView);
      await createInvitation();
      expect(wrapper!.text()).toContain('未提供有效的邀请链接');
      expect(wrapper!.text()).not.toContain(value);
      expect(wrapper!.text()).not.toContain('邀请已创建');
      expect(wrapper!.find('a').exists()).toBe(false);
    },
  );
  it('keeps the link selectable when clipboard access fails and hides exception text', async () => {
    clipboardWrite.mockRejectedValue(
      new Error('example-private-clipboard-error'),
    );
    await render(InvitationsView);
    await createInvitation();
    await click('复制邀请链接');
    expect(wrapper!.text()).toContain('手动选择并复制');
    expect(wrapper!.get('[data-testid="invitation-url"]').text()).toBe(
      returnedUrl,
    );
    expect(wrapper!.text()).not.toContain('example-private-clipboard-error');
  });
  it('does not restore a cleared result or copy notice from a late clipboard response', async () => {
    let resolve!: () => void;
    clipboardWrite.mockImplementation(
      () =>
        new Promise<void>((done) => {
          resolve = done;
        }),
    );
    await render(InvitationsView);
    await createInvitation();
    await click('复制邀请链接');
    await click('关闭并清除凭据');
    resolve();
    await flushPromises();
    expect(wrapper!.text()).not.toContain(returnedUrl);
    expect(wrapper!.text()).not.toContain(credential);
    expect(wrapper!.text()).not.toContain('已复制');
  });
  it('ignores an old copy failure even if a new creation returns the same credential', async () => {
    let reject!: (e: Error) => void;
    clipboardWrite.mockImplementationOnce(
      () =>
        new Promise((_done, fail) => {
          reject = fail;
        }),
    );
    await render(InvitationsView);
    await createInvitation();
    await click('复制邀请链接');
    await click('关闭并清除凭据');
    await createInvitation();
    reject(new Error('old-copy'));
    await flushPromises();
    expect(wrapper!.text()).not.toContain('无法访问剪贴板');
    expect(wrapper!.text()).toContain(returnedUrl);
  });
  it('does not save a creation response that arrives after results were cleared', async () => {
    let resolve!: (response: Response) => void;
    intercept = (path, init) =>
      path === '/api/org/v1/admin/invitations' && init.method === 'POST'
        ? new Promise((done) => {
            resolve = done;
          })
        : undefined;
    await render(InvitationsView);
    await click('创建成员邀请');
    await wrapper!.findAll('form').at(-1)!.trigger('submit');
    await flushPromises();
    await confirm();
    wrapper!.findAllComponents({ name: 'ElDialog' }).at(-1)!.vm.$emit('close');
    await flushPromises();
    resolve(
      json({ ...invitations[0], code: credential, url: returnedUrl }, 201),
    );
    await flushPromises();
    expect(wrapper!.text()).not.toContain(returnedUrl);
    expect(wrapper!.text()).not.toContain(credential);
  });
  it('uses English link and copy labels', async () => {
    await render(InvitationsView, 'en');
    await click('Create invitation');
    await wrapper!.findAll('form').at(-1)!.trigger('submit');
    await flushPromises();
    await confirm();
    expect(wrapper!.text()).toContain('One-time invitation link');
    await click('Copy invitation link');
    expect(clipboardWrite).toHaveBeenLastCalledWith(returnedUrl);
  });
  it('defaults to a discovered member role invitation and shows the returned code and URL once', async () => {
    await render(InvitationsView);
    await click('创建成员邀请');
    await wrapper!
      .get('input[aria-label="邮箱"]')
      .setValue('alice@example.com');
    await wrapper!.findAll('form').at(-1)!.trigger('submit');
    await flushPromises();
    expect(writes()).toHaveLength(0);
    await confirm();
    expect(JSON.parse(String(writes()[0].init.body))).toEqual({
      memberType: 'member',
      maxUses: 1,
      roleIds: ['member-role'],
      groupIds: [],
      providerIds: [],
      email: 'alice@example.com',
    });
    expect(new Headers(writes()[0].init.headers).get('X-Auriga-CSRF')).toBe(
      'example-csrf',
    );
    expect(wrapper!.get('[data-testid="invitation-code"]').text()).toBe(
      credential,
    );
    expect(wrapper!.get('[data-testid="invitation-url"]').text()).toBe(
      `https://portal.example.com/invite/${credential}`,
    );
    expect(wrapper!.find('a').exists()).toBe(false);
    expect(wrapper!.text()).toContain('没有发送邮件');
    expect(location.href).not.toContain(credential);
    expect(document.cookie).not.toContain(credential);
    expect(JSON.stringify(localStorage)).not.toContain(credential);
    expect(JSON.stringify(sessionStorage)).not.toContain(credential);
    await click('关闭并清除凭据');
    expect(wrapper!.text()).not.toContain(credential);
    await click('刷新');
    expect(wrapper!.text()).not.toContain(credential);
  });
  it('copies explicitly and clears the credential on unmount', async () => {
    await render(InvitationsView);
    await createInvitation();
    await click('复制邀请凭据');
    expect(clipboardWrite).toHaveBeenCalledWith(credential);
    expect(wrapper!.text()).toContain('已复制');
    wrapper!.unmount();
    wrapper = undefined;
    expect(document.body.textContent).not.toContain(credential);
  });
  it('handles clipboard failure without echoing the failed value', async () => {
    clipboardWrite.mockRejectedValue(new Error(credential));
    await render(InvitationsView);
    await createInvitation();
    await click('复制邀请凭据');
    expect(wrapper!.text()).toContain('无法访问剪贴板');
    await click('关闭并清除凭据');
    expect(wrapper!.text()).not.toContain(credential);
  });
  it('cancels creation without making a write', async () => {
    await render(InvitationsView);
    await click('创建成员邀请');
    await wrapper!.findAll('form').at(-1)!.trigger('submit');
    await flushPromises();
    await wrapper!
      .getComponent({ name: 'ActionConfirm' })
      .findAll('button')[0]
      .trigger('click');
    await flushPromises();
    expect(writes()).toHaveLength(0);
  });
  it('filters status and uses opaque next/previous cursors', async () => {
    intercept = (path) => {
      if (path === '/api/org/v1/admin/invitations?limit=50')
        return Promise.resolve(
          json({ items: invitations, nextCursor: 'opaque+/cursor' }),
        );
      if (path.includes('cursor=opaque%2B%2Fcursor'))
        return Promise.resolve(
          json({ items: [{ ...invitations[0], status: 'used' }] }),
        );
    };
    await render(InvitationsView);
    await click('下一页');
    expect(wrapper!.text()).toContain('已用完');
    await click('上一页');
    expect(wrapper!.text()).toContain('可使用');
    await choose('状态', 'revoked');
    expect(calls.at(-1)?.path).toBe(
      '/api/org/v1/admin/invitations?limit=50&status=revoked',
    );
    expect(wrapper!.text()).toContain('暂无数据');
  });
  it('confirms revocation and rereads actual status', async () => {
    await render(InvitationsView);
    await click('撤销邀请');
    expect(writes()).toHaveLength(0);
    await confirm();
    expect(writes()[0].method).toBe('DELETE');
    expect(wrapper!.text()).toContain('已撤销');
    expect(
      wrapper!
        .findAll('button')
        .find((item) => item.text() === '撤销邀请')!
        .attributes('disabled'),
    ).toBeDefined();
  });
  it('blocks a write after permission revocation', async () => {
    await render(InvitationsView);
    await click('撤销邀请');
    permissions = ['org.invitations.read'];
    await confirm();
    expect(writes()).toHaveLength(0);
    expect(wrapper!.text()).toContain('common.forbidden');
  });
  it('does not create twice while a response is pending', async () => {
    let resolve!: (response: Response) => void;
    intercept = (path, init) =>
      path === '/api/org/v1/admin/invitations' && init.method === 'POST'
        ? new Promise((done) => {
            resolve = done;
          })
        : undefined;
    await render(InvitationsView);
    await click('创建成员邀请');
    await wrapper!.findAll('form').at(-1)!.trigger('submit');
    await flushPromises();
    await confirm();
    await confirm();
    expect(writes()).toHaveLength(1);
    resolve(
      json(
        {
          ...invitations[0],
          code: credential,
          url: `https://portal.example.com/invite/${credential}`,
        },
        201,
      ),
    );
    await flushPromises();
    expect(wrapper!.text()).toContain(credential);
  });
  it('rejects an old response after a status filter changes', async () => {
    let resolve!: (response: Response) => void;
    intercept = (path) =>
      path === '/api/org/v1/admin/invitations?limit=50'
        ? new Promise((done) => {
            resolve = done;
          })
        : undefined;
    await render(InvitationsView);
    await choose('状态', 'expired');
    resolve(json({ items: invitations }));
    await flushPromises();
    expect(wrapper!.text()).toContain('暂无数据');
    expect(wrapper!.text()).not.toContain(invitationId);
  });
  it('discards a credential response received after navigation/unmount', async () => {
    let resolve!: (response: Response) => void;
    intercept = (path, init) =>
      path.endsWith('/invitations') && init.method === 'POST'
        ? new Promise((done) => {
            resolve = done;
          })
        : undefined;
    await render(InvitationsView);
    await click('创建成员邀请');
    await wrapper!.findAll('form').at(-1)!.trigger('submit');
    await flushPromises();
    await confirm();
    wrapper!.unmount();
    wrapper = undefined;
    resolve(
      json(
        {
          ...invitations[0],
          code: credential,
          url: `https://portal.example.com/invite/${credential}`,
        },
        201,
      ),
    );
    await flushPromises();
    expect(document.body.textContent).not.toContain(credential);
  });
  it('does not replay a create when its following refresh fails', async () => {
    let creates = 0;
    intercept = (path, init) => {
      if (path.endsWith('/invitations') && init.method === 'POST') {
        ++creates;
        return Promise.resolve(
          json(
            {
              ...invitations[0],
              code: credential,
              url: 'https://portal.example.com/example',
            },
            201,
          ),
        );
      }
      if (creates && path.includes('/invitations?'))
        return Promise.resolve(
          json(
            { status: 503, code: 'common.unavailable', title: 'Unavailable' },
            503,
          ),
        );
    };
    await render(InvitationsView);
    await createInvitation();
    expect(creates).toBe(1);
    expect(wrapper!.text()).toContain('勿重复提交');
    expect(wrapper!.text()).toContain(credential);
  });
});

describe('group management', () => {
  it('creates a group with confirmed name, description and selected parent', async () => {
    await render(GroupsView);
    await click('创建分组');
    await wrapper!.get('input[aria-label="名称"]').setValue('Example child');
    await wrapper!.get('textarea').setValue('Example description');
    await choose('父组', parentId);
    await wrapper!.findAll('form').at(-1)!.trigger('submit');
    await flushPromises();
    expect(writes()).toHaveLength(0);
    await confirm();
    expect(JSON.parse(String(writes()[0].init.body))).toEqual({
      name: 'Example child',
      description: 'Example description',
      parentId,
    });
    expect(wrapper!.text()).toContain('分组已保存');
  });
  it('edits a group through merge patch while excluding its descendants as parents', async () => {
    groups[1].parentId = groupId;
    await render(GroupsView);
    await click('详情');
    await click('修改分组');
    const parentSelect = wrapper!.findComponent({ name: 'ElSelect' });
    expect(
      parentSelect
        .findAllComponents({ name: 'ElOption' })
        .map((item) => item.props('value')),
    ).not.toContain(parentId);
    await wrapper!.get('input[aria-label="名称"]').setValue('Engineering B');
    await wrapper!.findAll('form').at(-1)!.trigger('submit');
    await flushPromises();
    await confirm();
    expect(new Headers(writes()[0].init.headers).get('Content-Type')).toBe(
      'application/merge-patch+json',
    );
    expect(wrapper!.text()).toContain('Engineering B');
  });
  it('does not offer clearing an existing parent unsupported by the backend', async () => {
    groups[0].parentId = parentId;
    await render(GroupsView);
    await click('详情');
    await click('修改分组');
    expect(wrapper!.text()).toContain('不支持清除已有父组');
    const options = wrapper!
      .findComponent({ name: 'ElSelect' })
      .findAllComponents({ name: 'ElOption' });
    expect(options.map((item) => item.props('value'))).not.toContain('');
  });
  it('reports group-not-empty and keeps the group after deletion is rejected', async () => {
    fail = {
      path: `/api/org/v1/admin/groups/${groupId}`,
      method: 'DELETE',
      status: 409,
      code: 'org.group_not_empty',
    };
    await render(GroupsView);
    await click('详情');
    await click('删除分组');
    expect(wrapper!.text()).toContain('角色绑定和配额');
    await confirm();
    expect(wrapper!.text()).toContain('org.group_not_empty');
    expect(wrapper!.text()).not.toContain('分组已删除');
    expect(groups).toHaveLength(2);
  });
  it('deletes a group only after confirmation and refreshes the list', async () => {
    await render(GroupsView);
    await click('详情');
    await click('删除分组');
    expect(writes()).toHaveLength(0);
    await confirm();
    expect(groups).toHaveLength(1);
    expect(wrapper!.text()).not.toContain('Engineering');
    expect(wrapper!.text()).toContain('分组已删除');
  });
  it('selects a real member to add and confirms removal from a group', async () => {
    await render(GroupsView);
    await click('详情');
    await click('搜索成员');
    await choose('选择要加入的成员', memberId);
    await click('加入成员');
    await confirm();
    expect(writes()[0].path).toBe(
      `/api/org/v1/admin/groups/${groupId}/members/${memberId}`,
    );
    expect(writes()[0].method).toBe('PUT');
    expect(wrapper!.text()).toContain('Alice');
    await click('移出成员');
    await confirm();
    expect(writes()[1].method).toBe('DELETE');
    expect(members[0].groupIds).toEqual([]);
  });
  it('keeps candidate selection empty on option failure and supports retry', async () => {
    fail = {
      path: '/api/org/v1/admin/members?limit=50',
      method: 'GET',
      status: 503,
      code: 'common.unavailable',
    };
    await render(GroupsView);
    await click('详情');
    await click('搜索成员');
    expect(wrapper!.text()).toContain('选项加载失败');
    expect(
      wrapper!
        .findAll('button')
        .find((item) => item.text() === '加入成员')!
        .attributes('disabled'),
    ).toBeDefined();
    fail = undefined;
    await click('重新加载选项');
    await choose('选择要加入的成员', memberId);
    await click('加入成员');
    await confirm();
    expect(writes()).toHaveLength(1);
  });
  it('does not offer arbitrary IDs or member choices without member read permission', async () => {
    permissions = ['org.groups.read', 'org.groups.write'];
    await render(GroupsView);
    await click('详情');
    expect(wrapper!.text()).toContain('选择新成员需要成员查看权限');
    expect(calls.some((call) => call.path.includes('/admin/members?'))).toBe(
      false,
    );
    expect(wrapper!.find('input').exists()).toBe(false);
  });
  it('shows server grant rejection without claiming member addition', async () => {
    fail = {
      path: `/api/org/v1/admin/groups/${groupId}/members/${memberId}`,
      method: 'PUT',
      status: 403,
      code: 'org.grant_exceeds_own',
    };
    await render(GroupsView);
    await click('详情');
    await click('搜索成员');
    await choose('选择要加入的成员', memberId);
    await click('加入成员');
    await confirm();
    expect(wrapper!.text()).toContain('org.grant_exceeds_own');
    expect(members[0].groupIds).toEqual([]);
    expect(wrapper!.text()).not.toContain('关系已保存');
  });
  it('pages group members with opaque cursors', async () => {
    intercept = (path) => {
      if (path === `/api/org/v1/admin/groups/${groupId}/members?limit=50`)
        return Promise.resolve(
          json({ items: [members[0]], nextCursor: 'member+/cursor' }),
        );
      if (path.includes('cursor=member%2B%2Fcursor'))
        return Promise.resolve(
          json({ items: [{ ...members[0], displayName: 'Bob' }] }),
        );
    };
    await render(GroupsView);
    await click('详情');
    await click('下一页');
    expect(wrapper!.text()).toContain('Bob');
    await click('上一页');
    expect(wrapper!.text()).toContain('Alice');
  });
  it('keeps mapped groups read-only', async () => {
    groups[0].source = 'mapped';
    await render(GroupsView);
    await click('详情');
    expect(wrapper!.text()).toContain('由身份源映射维护');
    expect(wrapper!.text()).not.toContain('修改分组');
    expect(wrapper!.text()).not.toContain('删除分组');
  });
  it('clears an old member selection after group changes', async () => {
    await render(GroupsView);
    await click('详情');
    await click('搜索成员');
    await choose('选择要加入的成员', memberId);
    const detailsButtons = wrapper!
      .findAll('button')
      .filter((button) => button.text() === '详情');
    await detailsButtons[1].trigger('click');
    await flushPromises();
    expect(wrapper!.get('h2').text()).toBe('Example parent');
    expect(
      wrapper!
        .findAll('button')
        .find((button) => button.text() === '加入成员')!
        .attributes('disabled'),
    ).toBeDefined();
  });
});

describe('permissions and API boundaries', () => {
  it('clears the invitation main list on read revocation while retaining the denial error', async () => {
    await render(InvitationsView);
    expect(wrapper!.text()).toContain(invitationId);
    await click('撤销邀请');
    permissions = [];
    await confirm();
    expect(writes()).toHaveLength(0);
    expect(wrapper!.text()).not.toContain(invitationId);
    expect(wrapper!.text()).toContain('common.forbidden');
  });
  it('clears group rows and details on read revocation while retaining the denial error', async () => {
    await render(GroupsView);
    await click('详情');
    await click('删除分组');
    permissions = [];
    await confirm();
    expect(writes()).toHaveLength(0);
    expect(wrapper!.text()).not.toContain('Engineering');
    expect(wrapper!.text()).toContain('common.forbidden');
  });
  it('does not add a selected member after member-read permission is revoked', async () => {
    await render(GroupsView);
    await click('详情');
    await click('搜索成员');
    await choose('选择要加入的成员', memberId);
    await click('加入成员');
    permissions = ['org.groups.read', 'org.groups.write'];
    await confirm();
    expect(writes()).toHaveLength(0);
    expect(wrapper!.text()).toContain('common.forbidden');
  });
  it('rejects a descendant parent before writing even if model state is changed directly', async () => {
    groups[1].parentId = groupId;
    await render(GroupsView);
    await click('详情');
    await click('修改分组');
    await choose('父组', parentId);
    await wrapper!.findAll('form').at(-1)!.trigger('submit');
    await flushPromises();
    expect(writes()).toHaveLength(0);
    expect(wrapper!.text()).toContain('不能选择自身或下级分组');
  });
  it('renders group-list failure and retries without offering stale parent choices', async () => {
    fail = {
      path: '/api/org/v1/admin/groups',
      method: 'GET',
      status: 503,
      code: 'common.unavailable',
    };
    await render(GroupsView);
    expect(wrapper!.text()).toContain('common.unavailable');
    expect(
      wrapper!
        .findAll('button')
        .find((button) => button.text() === '创建分组')!
        .attributes('disabled'),
    ).toBeDefined();
    fail = undefined;
    await click('重试');
    expect(wrapper!.text()).toContain('Engineering');
  });
  it.each([InvitationsView, GroupsView])(
    'shows an empty list when the server returns no data',
    async (component) => {
      invitations = [];
      groups = [];
      await render(component);
      expect(wrapper!.text()).toContain('暂无数据');
      expect(wrapper!.find('[role="alert"]').exists()).toBe(false);
    },
  );
  it.each([
    [InvitationsView, 'org.invitations.read'],
    [GroupsView, 'org.groups.read'],
  ] as const)(
    'renders read-only management without write actions',
    async (component, permission) => {
      permissions = [permission];
      await render(component);
      expect(wrapper!.text()).toContain('当前权限仅允许查看');
      expect(wrapper!.text()).not.toContain('创建成员邀请');
      expect(wrapper!.text()).not.toContain('创建分组');
    },
  );
  it.each([InvitationsView, GroupsView])(
    'does not fetch resources without read permission',
    async (component) => {
      permissions = [];
      await render(component);
      expect(calls.map((call) => call.path)).toEqual(['/api/org/v1/me']);
      expect(wrapper!.text()).toContain('common.forbidden');
    },
  );
  it('clears group editing after write permission is revoked', async () => {
    await render(GroupsView);
    await click('创建分组');
    await wrapper!.get('input[aria-label="名称"]').setValue('Example');
    await wrapper!.findAll('form').at(-1)!.trigger('submit');
    await flushPromises();
    permissions = ['org.groups.read'];
    await confirm();
    expect(writes()).toHaveLength(0);
    expect(wrapper!.text()).toContain('common.forbidden');
  });
  it('preserves the shared 401 redirect including returnTo', async () => {
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: '/groups', component: GroupsView },
        { path: '/login', component: defineComponent({ template: '<div />' }) },
      ],
    });
    await router.push('/groups');
    bindApiRouter(router);
    fail = {
      path: '/api/org/v1/me',
      method: 'GET',
      status: 401,
      code: 'common.unauthenticated',
    };
    await render(GroupsView);
    expect(router.currentRoute.value.path).toBe('/login');
    expect(router.currentRoute.value.query.returnTo).toBe('/groups');
  });
  it('does not leak server error detail strings into invitation UI', async () => {
    fail = {
      path: '/api/org/v1/admin/invitations',
      method: 'POST',
      status: 503,
      code: 'common.unavailable',
    };
    await render(InvitationsView);
    await createInvitation();
    expect(wrapper!.text()).toContain('common.unavailable');
    expect(wrapper!.text()).not.toContain('Sensitive detail');
    expect(writes()).toHaveLength(1);
  });
  it('uses English invitation copy', async () => {
    await render(InvitationsView, 'en');
    expect(wrapper!.text()).toContain('Invitations');
    await click('Create invitation');
    expect(wrapper!.text()).toContain('No email is sent automatically');
  });
  it('encodes group and invitation identifiers', async () => {
    intercept = () => Promise.resolve(noContent());
    await orgAdminApi.revokeInvitation('a/b?c');
    await orgAdminApi.addMember('g/a', 'm?b');
    expect(writes().map((call) => call.path)).toEqual([
      '/api/org/v1/admin/invitations/a%2Fb%3Fc',
      '/api/org/v1/admin/groups/g%2Fa/members/m%3Fb',
    ]);
  });
});
