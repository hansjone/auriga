import { mount, flushPromises, type VueWrapper } from '@vue/test-utils';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createMemoryHistory, createRouter } from 'vue-router';
import { createAurigaI18n, type Member } from '@auriga/shared';
import ElementPlus from 'element-plus';
import RolesView from '../src/views/RolesView.vue';
import { apiClient, bindApiRouter } from '../src/api';
import {
  rolesApi,
  type Role,
  type Permission,
  type RoleBinding,
} from '../src/admin/rolesApi';

const roleId = '01900000-0000-7000-8000-000000000001';
const builtinId = '01900000-0000-7000-8000-000000000002';
const memberId = '01900000-0000-7000-8000-000000000003';
const groupId = '01900000-0000-7000-8000-000000000004';
const bindingId = '01900000-0000-7000-8000-000000000005';
const createdId = '01900000-0000-7000-8000-000000000006';
const timestamp = '2026-10-24T00:00:00Z';
const base = '/api/org/v1/admin';
const all = [
  'org.roles.read',
  'org.roles.write',
  'org.roles.grant',
  'org.members.read',
  'org.groups.read',
  'workspace.nodes.read',
];
const member: Member = {
  id: memberId,
  displayName: 'Alice',
  email: 'alice@example.com',
  memberType: 'member',
  status: 'active',
  groupIds: [],
  roleKeys: ['member'],
  createdAt: timestamp,
};
let permissions: string[];
let roles: Role[];
let catalog: Permission[];
let bindings: RoleBinding[];
let exists: boolean;
let calls: { path: string; method: string; init: RequestInit }[];
let intercept:
  | ((path: string, init: RequestInit) => Promise<Response> | undefined)
  | undefined;
let fail:
  { path: string; method: string; status: number; code: string } | undefined;
let wrapper: VueWrapper | undefined;
const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
const problem = (status: number, code: string) =>
  json({ status, code, title: 'Request rejected' }, status);
const writes = () => calls.filter((call) => call.method !== 'GET');
const binding = (
  type: RoleBinding['subject']['type'] = 'member',
): RoleBinding => ({
  id: bindingId,
  roleId,
  roleKey: 'ops_reader',
  subject: { type, id: type === 'group' ? groupId : memberId },
  createdAt: timestamp,
  grantedBy: memberId,
});

beforeEach(() => {
  permissions = [...all];
  calls = [];
  exists = true;
  intercept = undefined;
  fail = undefined;
  bindings = [];
  roles = [
    {
      id: roleId,
      key: 'ops_reader',
      name: { en: 'Operations reader', zh: '运维只读' },
      description: { en: 'Example description', zh: '示例说明' },
      builtin: false,
      permissions: ['workspace.nodes.read'],
      bindingCount: 0,
    },
    {
      id: builtinId,
      key: 'owner',
      name: { en: 'Owner', zh: '所有者' },
      builtin: true,
      permissions: [...all],
      bindingCount: 1,
    },
  ];
  catalog = [
    {
      key: 'workspace.nodes.read',
      service: 'workspace',
      description: { en: 'View nodes', zh: '查看节点' },
      sensitive: true,
    },
    {
      key: 'org.members.read',
      service: 'org',
      description: { en: 'View members', zh: '查看成员' },
      sensitive: false,
    },
    {
      key: 'org.groups.write',
      service: 'org',
      description: { en: 'Edit groups', zh: '修改分组' },
      sensitive: true,
    },
  ];
  document.head.innerHTML = '<meta name="auriga-access-mode" content="domain">';
  document.cookie = 'auriga_csrf=example-csrf; path=/';
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
      if (fail?.path === path && fail.method === method)
        return problem(fail.status, fail.code);
      if (path === '/api/org/v1/me')
        return json({
          member,
          permissions,
          tenant: {
            id: groupId,
            displayName: 'Example',
            slug: 'example',
            locale: 'zh-CN',
            hosts: [],
            createdAt: timestamp,
          },
        });
      if (path === `${base}/permissions`) return json({ items: catalog });
      if (path === `${base}/roles`) {
        if (method === 'POST') {
          const result: Role = {
            ...JSON.parse(String(init.body)),
            id: createdId,
            builtin: false,
            bindingCount: 0,
          };
          roles.push(result);
          return json(result, 201);
        }
        return json({ items: roles });
      }
      const role = roles.find((item) => path === `${base}/roles/${item.id}`);
      if (role) {
        if (method === 'PATCH')
          Object.assign(role, JSON.parse(String(init.body)));
        if (method === 'DELETE') {
          roles = roles.filter((item) => item.id !== role.id);
          bindings = bindings.filter((item) => item.roleId !== role.id);
          return new Response(null, { status: 204 });
        }
        return json(role);
      }
      if (path.startsWith(`${base}/role-bindings?`))
        return json({
          items: bindings.filter(
            (item) => item.roleId === url.searchParams.get('roleId'),
          ),
        });
      if (path === `${base}/role-bindings` && method === 'POST') {
        const result: RoleBinding = {
          ...JSON.parse(String(init.body)),
          id: bindingId,
          roleKey: roles[0].key,
          createdAt: timestamp,
          grantedBy: memberId,
        };
        bindings.push(result);
        roles[0].bindingCount++;
        return json(result, 201);
      }
      if (
        path === `${base}/role-bindings/${bindingId}` &&
        method === 'DELETE'
      ) {
        bindings = [];
        return new Response(null, { status: 204 });
      }
      if (path.startsWith(`${base}/members?`)) return json({ items: [member] });
      if (path === `${base}/members/${memberId}`)
        return exists ? json(member) : problem(404, 'common.not_found');
      if (path === `${base}/groups`)
        return json({
          items: [
            {
              id: groupId,
              name: 'Example group',
              memberCount: 0,
              source: 'manual',
              createdAt: timestamp,
            },
          ],
        });
      if (path === `${base}/groups/${groupId}`)
        return exists
          ? json({
              id: groupId,
              name: 'Example group',
              memberCount: 0,
              source: 'manual',
              createdAt: timestamp,
            })
          : problem(404, 'common.not_found');
      return problem(404, 'common.not_found');
    }),
  );
});
afterEach(() => {
  wrapper?.unmount();
  wrapper = undefined;
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  document.body.innerHTML = '';
  document.head.innerHTML = '';
  document.cookie = 'auriga_csrf=; Max-Age=0; path=/';
});
async function render(locale: 'zh-CN' | 'en' = 'zh-CN') {
  wrapper = mount(RolesView, {
    attachTo: document.body,
    global: { plugins: [createAurigaI18n(locale), ElementPlus] },
  });
  await flushPromises();
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
async function submit() {
  await wrapper!.findAll('form').at(-1)!.trigger('submit');
  await flushPromises();
}
async function choose(label: string, value: string) {
  const select = wrapper!
    .findAllComponents({ name: 'ElSelect' })
    .find((item) => item.props('ariaLabel') === label);
  expect(select, label).toBeDefined();
  select!.vm.$emit('update:modelValue', value);
  await flushPromises();
}
async function editValues() {
  await wrapper!.get('input[aria-label="英文名称"]').setValue('Example role');
  await wrapper!.get('input[aria-label="中文名称"]').setValue('示例角色');
}
async function prepareGrant(type: 'member' | 'group' = 'member') {
  await click('详情');
  if (type === 'group') await choose('主体类型', 'group');
  await click('读取授权候选');
  await choose('选择授权主体', type === 'member' ? memberId : groupId);
  await click('授予角色');
}

describe('role definition workflows', () => {
  it('reads the official role and permission catalogs', async () => {
    await render();
    expect(wrapper!.text()).toContain('运维只读');
    expect(wrapper!.text()).toContain('查看节点');
    expect(calls.some((call) => call.path === `${base}/permissions`)).toBe(
      true,
    );
    await click('详情');
    expect(wrapper!.text()).toContain(roleId);
  });
  it('creates a localized custom role after confirmation and reads back server results', async () => {
    await render();
    await click('创建角色');
    await wrapper!.get('input[aria-label="角色标识"]').setValue('example_role');
    await editValues();
    wrapper!
      .getComponent({ name: 'ElCheckboxGroup' })
      .vm.$emit('update:modelValue', ['workspace.nodes.read']);
    await flushPromises();
    await submit();
    expect(writes()).toHaveLength(0);
    expect(wrapper!.text()).toContain('敏感权限：workspace.nodes.read');
    await confirm();
    expect(JSON.parse(String(writes()[0].init.body))).toEqual({
      key: 'example_role',
      name: { en: 'Example role', zh: '示例角色' },
      description: { en: '', zh: '' },
      permissions: ['workspace.nodes.read'],
    });
    expect(new Headers(writes()[0].init.headers).get('X-Auriga-CSRF')).toBe(
      'example-csrf',
    );
    expect(wrapper!.text()).toContain('角色已保存');
    expect(
      calls.some((call) => call.path === `${base}/roles/${createdId}`),
    ).toBe(true);
  });
  it('edits only supported fields, keeps the key immutable and clears localized descriptions as empty strings', async () => {
    await render();
    await click('详情');
    await click('编辑角色');
    expect(
      wrapper!.get('input[aria-label="角色标识"]').attributes('disabled'),
    ).toBeDefined();
    await editValues();
    await wrapper!.get('input[aria-label="英文描述"]').setValue('');
    await wrapper!.get('input[aria-label="中文描述"]').setValue('');
    await submit();
    await confirm();
    const body = JSON.parse(String(writes()[0].init.body));
    expect(body.key).toBe('ops_reader');
    expect(body.description).toEqual({ en: '', zh: '' });
    expect(body).not.toHaveProperty('builtin');
    expect(body).not.toHaveProperty('adminOnly');
    expect(new Headers(writes()[0].init.headers).get('Content-Type')).toBe(
      'application/merge-patch+json',
    );
  });
  it('cancels without creating a role', async () => {
    await render();
    await click('创建角色');
    await wrapper!.get('input[aria-label="角色标识"]').setValue('example_role');
    await editValues();
    await submit();
    await wrapper!
      .getComponent({ name: 'ActionConfirm' })
      .findAll('button')[0]
      .trigger('click');
    await flushPromises();
    expect(writes()).toHaveLength(0);
  });
  it('uses builtin metadata rather than a role key to disable definition changes', async () => {
    roles[0].builtin = true;
    await render();
    await click('详情');
    expect(wrapper!.text()).toContain('内置角色定义只读');
    expect(wrapper!.text()).not.toContain('编辑角色');
    expect(wrapper!.text()).not.toContain('删除角色');
  });
  it('confirms cascading binding deletion before deleting a custom role', async () => {
    bindings = [binding()];
    await render();
    await click('详情');
    await click('删除角色');
    expect(wrapper!.text()).toContain('绑定将一并删除');
    expect(writes()).toHaveLength(0);
    await confirm();
    expect(roles.some((item) => item.id === roleId)).toBe(false);
    expect(bindings).toEqual([]);
    expect(wrapper!.text()).toContain('角色已删除');
  });
  it('does not enable role creation if the permissions catalog failed, and retries it', async () => {
    fail = {
      path: `${base}/permissions`,
      method: 'GET',
      status: 503,
      code: 'common.unavailable',
    };
    await render();
    expect(wrapper!.text()).toContain('common.unavailable');
    expect(
      wrapper!
        .findAll('button')
        .find((button) => button.text() === '创建角色')!
        .attributes('disabled'),
    ).toBeDefined();
    fail = undefined;
    await click('重新加载选项');
    expect(wrapper!.text()).not.toContain('common.unavailable');
    expect(
      wrapper!
        .findAll('button')
        .find((button) => button.text() === '创建角色')!
        .attributes('disabled'),
    ).toBeUndefined();
  });
  it('disables selection of catalog permissions outside the current effective permissions', async () => {
    await render();
    await click('创建角色');
    const option = wrapper!
      .findAllComponents({ name: 'ElCheckbox' })
      .find((item) => item.props('value') === 'org.groups.write')!;
    expect(option.props('disabled')).toBe(true);
    expect(wrapper!.text()).toContain('服务端仍最终校验授权上限');
  });
  it('shows server grant-limit rejection without claiming role creation', async () => {
    fail = {
      path: `${base}/roles`,
      method: 'POST',
      status: 403,
      code: 'org.grant_exceeds_own',
    };
    await render();
    await click('创建角色');
    await wrapper!.get('input[aria-label="角色标识"]').setValue('example_role');
    await editValues();
    await submit();
    await confirm();
    expect(wrapper!.text()).toContain('org.grant_exceeds_own');
    expect(wrapper!.text()).not.toContain('角色已保存');
    expect(wrapper!.text()).not.toContain('运维只读');
  });
  it('does not submit an invalid role key', async () => {
    await render();
    await click('创建角色');
    await wrapper!.get('input[aria-label="角色标识"]').setValue('INVALID');
    await editValues();
    await submit();
    expect(writes()).toHaveLength(0);
    expect(wrapper!.text()).toContain('请填写合法角色标识');
  });
  it('does not repeat a create when authoritative readback fails', async () => {
    let created = false;
    intercept = (path, init) => {
      if (init.method === 'POST' && path === `${base}/roles`) {
        created = true;
        return Promise.resolve(json({ ...roles[0], id: createdId }, 201));
      }
      if (created && path === `${base}/roles`)
        return Promise.resolve(problem(503, 'common.unavailable'));
    };
    await render();
    await click('创建角色');
    await wrapper!.get('input[aria-label="角色标识"]').setValue('example_role');
    await editValues();
    await submit();
    await confirm();
    expect(writes()).toHaveLength(1);
    expect(wrapper!.text()).toContain('不要重复提交');
  });
  it('prevents repeated confirmation while role creation is pending', async () => {
    let resolve!: (response: Response) => void;
    intercept = (path, init) =>
      path === `${base}/roles` && init.method === 'POST'
        ? new Promise((done) => {
            resolve = done;
          })
        : undefined;
    await render();
    await click('创建角色');
    await wrapper!.get('input[aria-label="角色标识"]').setValue('example_role');
    await editValues();
    await submit();
    await confirm();
    await confirm();
    expect(writes()).toHaveLength(1);
    roles.push({ ...roles[0], id: createdId });
    resolve(json(roles.at(-1), 201));
    await flushPromises();
  });
});

describe('binding workflows and permission separation', () => {
  it.each(['disabled', 'offboarded'] as const)(
    'does not offer a %s member for grants',
    async (status) => {
      intercept = (path) =>
        path === `${base}/members?limit=50`
          ? Promise.resolve(json({ items: [{ ...member, status }] }))
          : undefined;
      await render();
      await click('详情');
      await click('读取授权候选');
      const select = wrapper!
        .findAllComponents({ name: 'ElSelect' })
        .find((item) => item.props('ariaLabel') === '选择授权主体')!;
      expect(select.props('options')).toEqual([]);
      expect(writes()).toHaveLength(0);
    },
  );
  it.each(['disabled', 'offboarded'] as const)(
    'rejects a member changed to %s before confirmation',
    async (status) => {
      await render();
      await prepareGrant();
      intercept = (path) =>
        path === `${base}/members/${memberId}`
          ? Promise.resolve(json({ ...member, status }))
          : undefined;
      await confirm();
      expect(writes()).toHaveLength(0);
      expect(wrapper!.text()).toContain('org.conflict');
    },
  );
  it.each(['POST', 'DELETE'])(
    'rereads roles and bindings after a failed %s binding write',
    async (method) => {
      bindings = [binding()];
      await render();
      if (method === 'POST') await prepareGrant();
      else {
        await click('详情');
        await click('撤销授权');
      }
      const before = calls.length;
      fail = {
        path: `${base}/role-bindings${method === 'DELETE' ? `/${bindingId}` : ''}`,
        method,
        status: 403,
        code: 'org.grant_exceeds_own',
      };
      await confirm();
      const reads = calls.slice(before).filter((call) => call.method === 'GET');
      expect(reads.some((call) => call.path === `${base}/roles`)).toBe(true);
      expect(
        reads.some((call) => call.path.startsWith(`${base}/role-bindings?`)),
      ).toBe(true);
      expect(wrapper!.text()).toContain('org.grant_exceeds_own');
      expect(writes()).toHaveLength(1);
    },
  );
  it('grants a role with grant-only permission and no role-write permission', async () => {
    permissions = all.filter((key) => key !== 'org.roles.write');
    await render();
    expect(wrapper!.text()).not.toContain('创建角色');
    await prepareGrant();
    expect(writes()).toHaveLength(0);
    await confirm();
    expect(JSON.parse(String(writes()[0].init.body))).toEqual({
      roleId,
      subject: { type: 'member', id: memberId },
    });
    expect(wrapper!.text()).toContain('授权绑定已保存');
    expect(
      calls.filter((call) => call.path === `${base}/members/${memberId}`),
    ).toHaveLength(1);
  });
  it('does not grant or revoke with role-write permission alone', async () => {
    permissions = all.filter((key) => key !== 'org.roles.grant');
    bindings = [binding()];
    await render();
    await click('详情');
    expect(wrapper!.text()).toContain('编辑角色');
    expect(wrapper!.text()).not.toContain('授予角色');
    expect(wrapper!.text()).not.toContain('撤销授权');
    expect(wrapper!.text()).toContain('独立的角色授予权限');
  });
  it('supports a real group candidate and verifies it before granting', async () => {
    await render();
    await prepareGrant('group');
    await confirm();
    expect(writes()[0].path).toBe(`${base}/role-bindings`);
    expect(JSON.parse(String(writes()[0].init.body)).subject).toEqual({
      type: 'group',
      id: groupId,
    });
    expect(
      calls.some((call) => call.path === `${base}/groups/${groupId}`),
    ).toBe(true);
  });
  it('does not read or accept candidates without their independent read permission', async () => {
    permissions = ['org.roles.read', 'org.roles.grant'];
    await render();
    await click('详情');
    expect(wrapper!.text()).toContain('相应的成员或分组查看权限');
    await choose('主体类型', 'group');
    expect(
      calls.some(
        (call) =>
          call.path === `${base}/groups` || call.path.includes('/members?'),
      ),
    ).toBe(false);
    expect(
      wrapper!
        .findAllComponents({ name: 'ElSelect' })
        .some((item) => item.props('ariaLabel') === '选择授权主体'),
    ).toBe(false);
  });
  it('rechecks the candidate read permission before a grant', async () => {
    await render();
    await prepareGrant();
    permissions = all.filter((key) => key !== 'org.members.read');
    await confirm();
    expect(writes()).toHaveLength(0);
    expect(wrapper!.text()).toContain('common.forbidden');
  });
  it('does not grant to a candidate that disappeared', async () => {
    await render();
    await prepareGrant();
    exists = false;
    await confirm();
    expect(writes()).toHaveLength(0);
    expect(wrapper!.text()).toContain('common.not_found');
  });
  it('rechecks the specific grant permission before confirmation', async () => {
    await render();
    await prepareGrant();
    permissions = all.filter((key) => key !== 'org.roles.grant');
    await confirm();
    expect(writes()).toHaveLength(0);
    expect(wrapper!.text()).toContain('common.forbidden');
  });
  it('revokes an existing member binding after confirmation', async () => {
    bindings = [binding()];
    await render();
    await click('详情');
    await click('撤销授权');
    expect(writes()).toHaveLength(0);
    await confirm();
    expect(writes()[0].path).toBe(`${base}/role-bindings/${bindingId}`);
    expect(wrapper!.text()).toContain('授权绑定已撤销');
    expect(bindings).toEqual([]);
  });
  it('preserves last-owner rejection and does not claim a successful revoke', async () => {
    bindings = [binding()];
    fail = {
      path: `${base}/role-bindings/${bindingId}`,
      method: 'DELETE',
      status: 409,
      code: 'org.last_owner',
    };
    await render();
    await click('详情');
    await click('撤销授权');
    await confirm();
    expect(wrapper!.text()).toContain('org.last_owner');
    expect(wrapper!.text()).not.toContain('授权绑定已撤销');
    expect(bindings).toHaveLength(1);
  });
  it('keeps service-account bindings visible and read-only without offering fabricated candidates', async () => {
    bindings = [binding('service_account')];
    await render();
    await click('详情');
    expect(wrapper!.text()).toContain('本页仅查看已有绑定');
    expect(wrapper!.text()).toContain('服务账号');
    expect(
      wrapper!.findAll('button').some((item) => item.text() === '撤销授权'),
    ).toBe(false);
    const types = wrapper!
      .findAllComponents({ name: 'ElSelect' })
      .find((item) => item.props('ariaLabel') === '主体类型')!;
    expect(
      types.props('options').map((item: { value: string }) => item.value),
    ).toEqual(['member', 'group']);
  });
  it('paginates role-filtered bindings with opaque cursors', async () => {
    const first = `${base}/role-bindings?roleId=${roleId}&limit=50`;
    intercept = (path) =>
      path === first
        ? Promise.resolve(
            json({ items: [binding()], nextCursor: 'opaque+/cursor' }),
          )
        : path.includes('cursor=opaque%2B%2Fcursor')
          ? Promise.resolve(
              json({
                items: [
                  { ...binding(), subject: { type: 'group', id: groupId } },
                ],
              }),
            )
          : undefined;
    await render();
    await click('详情');
    await click('下一页');
    expect(calls.at(-1)!.path).toBe(`${first}&cursor=opaque%2B%2Fcursor`);
    await click('上一页');
    expect(calls.filter((call) => call.path === first)).toHaveLength(2);
  });
  it('paginates and searches member candidates without inventing UUIDs', async () => {
    intercept = (path) =>
      path === `${base}/members?limit=50`
        ? Promise.resolve(
            json({ items: [member], nextCursor: 'candidate+/cursor' }),
          )
        : path.includes('cursor=candidate%2B%2Fcursor')
          ? Promise.resolve(
              json({ items: [{ ...member, displayName: 'Bob' }] }),
            )
          : undefined;
    await render();
    await click('详情');
    await click('读取授权候选');
    const next = wrapper!
      .findAll('button')
      .filter((item) => item.text() === '下一页')
      .at(-1)!;
    await next.trigger('click');
    await flushPromises();
    expect(
      calls.some((call) => call.path.includes('cursor=candidate%2B%2Fcursor')),
    ).toBe(true);
    await wrapper!.get('input[aria-label="搜索候选成员"]').setValue('alice');
    await click('读取授权候选');
    expect(calls.at(-1)!.path).toBe(`${base}/members?q=alice&limit=50`);
  });
});

describe('role page state boundaries', () => {
  it('does not reuse an old binding-read error after deleting the role successfully', async () => {
    fail = {
      path: `${base}/role-bindings?roleId=${roleId}&limit=50`,
      method: 'GET',
      status: 503,
      code: 'common.unavailable',
    };
    await render();
    await click('详情');
    expect(wrapper!.text()).toContain('common.unavailable');
    fail = undefined;
    await click('删除角色');
    await confirm();
    expect(wrapper!.text()).toContain('角色已删除');
    expect(wrapper!.text()).not.toContain('正式读回失败');
  });
  it('clears a binding-read error after a successful retry', async () => {
    fail = {
      path: `${base}/role-bindings?roleId=${roleId}&limit=50`,
      method: 'GET',
      status: 503,
      code: 'common.unavailable',
    };
    await render();
    await click('详情');
    fail = undefined;
    const retry = wrapper!
      .findAll('button')
      .filter((item) => item.text() === '重试')
      .at(-1)!;
    await retry.trigger('click');
    await flushPromises();
    expect(wrapper!.text()).not.toContain('common.unavailable');
  });
  it('does not write after unmount while final authorization is still pending', async () => {
    await render();
    await click('创建角色');
    await wrapper!.get('input[aria-label="角色标识"]').setValue('example_role');
    await editValues();
    await submit();
    let resolve!: (response: Response) => void;
    intercept = (path) =>
      path === '/api/org/v1/me'
        ? new Promise((done) => {
            resolve = done;
          })
        : undefined;
    await confirm();
    wrapper!.unmount();
    wrapper = undefined;
    resolve(json({ member, permissions }));
    await flushPromises();
    expect(writes()).toHaveLength(0);
  });
  it('rejects stale member candidates after switching subject type', async () => {
    let resolve!: (response: Response) => void;
    intercept = (path) =>
      path === `${base}/members?limit=50`
        ? new Promise((done) => {
            resolve = done;
          })
        : undefined;
    await render();
    await click('详情');
    await click('读取授权候选');
    await choose('主体类型', 'group');
    await click('读取授权候选');
    resolve(json({ items: [member] }));
    await flushPromises();
    const candidates = wrapper!
      .findAllComponents({ name: 'ElSelect' })
      .find((item) => item.props('ariaLabel') === '选择授权主体')!;
    expect(
      candidates.props('options').map((item: { value: string }) => item.value),
    ).toEqual([groupId]);
  });
  it('rejects a grant if the role permissions changed after confirmation was prepared', async () => {
    await render();
    await prepareGrant();
    roles[0].permissions = ['org.members.read'];
    await confirm();
    expect(writes()).toHaveLength(0);
    expect(wrapper!.text()).toContain('common.conflict');
  });
  it('keeps existing role permissions absent from the catalog without guessing their sensitivity', async () => {
    roles[0].permissions = ['org.roles.read'];
    await render();
    await click('详情');
    await click('编辑角色');
    await submit();
    expect(wrapper!.text()).toContain('权限目录未提供此项的敏感级别');
    await confirm();
    expect(JSON.parse(String(writes()[0].init.body)).permissions).toEqual([
      'org.roles.read',
    ]);
  });
  it('does not fetch catalogs without roles-read permission', async () => {
    permissions = ['org.roles.write', 'org.roles.grant'];
    await render();
    expect(calls.map((call) => call.path)).toEqual(['/api/org/v1/me']);
    expect(wrapper!.text()).toContain('common.forbidden');
  });
  it('clears roles, catalog, form and bindings when read permission is revoked', async () => {
    await render();
    await click('详情');
    await click('删除角色');
    permissions = [];
    await confirm();
    expect(writes()).toHaveLength(0);
    expect(wrapper!.text()).not.toContain('运维只读');
    expect(wrapper!.text()).not.toContain('查看节点');
    expect(wrapper!.text()).toContain('common.forbidden');
  });
  it('clears stale role data on server-side 403', async () => {
    fail = {
      path: `${base}/roles/${roleId}`,
      method: 'GET',
      status: 403,
      code: 'common.forbidden',
    };
    await render();
    await click('详情');
    expect(wrapper!.text()).toContain('common.forbidden');
    expect(wrapper!.text()).not.toContain('运维只读');
  });
  it('ignores a late role response after unmount', async () => {
    let resolve!: (response: Response) => void;
    intercept = (path) =>
      path === `${base}/roles/${roleId}`
        ? new Promise((done) => {
            resolve = done;
          })
        : undefined;
    await render();
    await click('详情');
    wrapper!.unmount();
    wrapper = undefined;
    resolve(json(roles[0]));
    await flushPromises();
    expect(document.body.textContent).not.toContain('运维只读');
  });
  it('clears old binding and candidate selection on switching roles', async () => {
    await render();
    await click('详情');
    await click('读取授权候选');
    await choose('选择授权主体', memberId);
    const detail = wrapper!
      .findAll('button')
      .filter((item) => item.text() === '详情')[1];
    await detail.trigger('click');
    await flushPromises();
    expect(wrapper!.findAll('section.details').at(-1)!.get('h2').text()).toBe(
      '所有者',
    );
    const grantButton = wrapper!
      .findAll('button')
      .find((item) => item.text() === '授予角色')!;
    expect(grantButton.attributes('disabled')).toBeDefined();
    expect(
      calls.some((call) => call.path.includes(`roleId=${builtinId}`)),
    ).toBe(true);
  });
  it('uses the shared 401 redirect for expired sessions', async () => {
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: '/roles', component: RolesView },
        { path: '/login', component: { template: '<div />' } },
      ],
    });
    await router.push('/roles');
    bindApiRouter(router);
    fail = {
      path: '/api/org/v1/me',
      method: 'GET',
      status: 401,
      code: 'common.unauthenticated',
    };
    await render();
    expect(router.currentRoute.value.path).toBe('/login');
    expect(router.currentRoute.value.query.returnTo).toBe('/roles');
  });
  it('keeps an authorized empty catalog/list separate from loading or errors', async () => {
    roles = [];
    catalog = [];
    await render();
    expect(wrapper!.text()).toContain('暂无数据');
    expect(wrapper!.find('[role="alert"]').exists()).toBe(false);
  });
  it('renders English role text', async () => {
    await render('en');
    expect(wrapper!.text()).toContain('Roles and grants');
    expect(wrapper!.text()).toContain('Operations reader');
  });
  it('encodes role and binding identifiers in request paths', async () => {
    intercept = () => Promise.resolve(new Response(null, { status: 204 }));
    await rolesApi.get('a/b?c');
    await rolesApi.revoke('b/a?c');
    expect(calls.map((call) => call.path)).toEqual([
      `${base}/roles/a%2Fb%3Fc`,
      `${base}/role-bindings/b%2Fa%3Fc`,
    ]);
  });
});
