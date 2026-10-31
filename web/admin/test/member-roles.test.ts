import { mount, flushPromises, type VueWrapper } from '@vue/test-utils';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import ElementPlus from 'element-plus';
import { ApiError, createAurigaI18n } from '@auriga/shared';
import MemberRoles from '../src/admin/MemberRoles.vue';
import { adminApi } from '../src/admin/api';
import { rolesApi, type Role, type RoleBinding } from '../src/admin/rolesApi';
import { orgAdminApi } from '../src/admin/orgApi';

const all = ['org.roles.read', 'org.roles.grant', 'org.groups.read'];
const roles: Role[] = [
  {
    id: 'member-role',
    key: 'member',
    name: { en: 'Member', zh: '普通成员' },
    builtin: true,
    permissions: [],
    bindingCount: 1,
  },
  {
    id: 'custom-role',
    key: 'reader',
    name: { en: 'Reader', zh: '自定义只读' },
    builtin: false,
    permissions: [],
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
const direct: RoleBinding = {
  id: 'direct',
  roleId: 'member-role',
  roleKey: 'member',
  via: 'direct',
  subject: { type: 'member', id: 'alice' },
  createdAt: '2026-10-30T00:00:00Z',
};
let permissions: string[];
let wrapper: VueWrapper | undefined;
beforeEach(() => {
  permissions = [...all];
  vi.spyOn(adminApi, 'membership').mockImplementation(
    async () =>
      ({ permissions }) as Awaited<ReturnType<typeof adminApi.membership>>,
  );
  vi.spyOn(rolesApi, 'list').mockResolvedValue({ items: roles });
  vi.spyOn(rolesApi, 'memberBindings').mockResolvedValue({
    items: [
      direct,
      {
        ...direct,
        id: 'inherited',
        via: 'group',
        subject: { type: 'group', id: 'team' },
      },
    ],
  });
  vi.spyOn(orgAdminApi, 'groups').mockResolvedValue({
    items: [
      {
        id: 'team',
        name: '研发组',
        memberCount: 1,
        source: 'manual',
        createdAt: direct.createdAt,
      },
    ],
  });
  vi.spyOn(rolesApi, 'grant').mockResolvedValue(direct);
  vi.spyOn(rolesApi, 'revoke').mockResolvedValue(undefined);
});
afterEach(() => {
  wrapper?.unmount();
  wrapper = undefined;
  vi.restoreAllMocks();
});
async function render() {
  wrapper = mount(MemberRoles, {
    props: { memberId: 'alice', memberName: 'Alice', disabled: false },
    global: { plugins: [createAurigaI18n('zh-CN'), ElementPlus] },
  });
  await flushPromises();
}
async function click(label: string) {
  const button = wrapper!.findAll('button').find((b) => b.text() === label);
  expect(button, label).toBeDefined();
  await button!.trigger('click');
  await flushPromises();
}
async function confirm() {
  await wrapper!
    .findAllComponents({ name: 'ActionConfirm' })[0]
    .vm.$emit('confirm');
  await flushPromises();
}
it('shows direct and inherited roles, with only direct revocation and all role candidates', async () => {
  await render();
  expect(wrapper!.text()).toContain('直接绑定');
  expect(wrapper!.text()).toContain('研发组');
  expect(
    wrapper!.findAll('button').filter((b) => b.text() === '撤销授权'),
  ).toHaveLength(1);
  const options = wrapper!.findComponent({ name: 'ElSelect' }).props('options');
  expect(options).toEqual(
    expect.arrayContaining([
      expect.objectContaining({ value: 'owner-role', disabled: true }),
      expect.objectContaining({ value: 'custom-role', disabled: false }),
      expect.objectContaining({ value: 'member-role', disabled: true }),
    ]),
  );
});
it('grants a selected custom role after confirmation then requests authoritative member refresh', async () => {
  await render();
  wrapper!
    .findComponent({ name: 'ElSelect' })
    .vm.$emit('update:modelValue', 'custom-role');
  await flushPromises();
  await click('授予角色');
  expect(rolesApi.grant).not.toHaveBeenCalled();
  await confirm();
  expect(rolesApi.grant).toHaveBeenCalledWith({
    roleId: 'custom-role',
    subject: { type: 'member', id: 'alice' },
  });
  expect(wrapper!.emitted('updated')).toHaveLength(1);
});
it.each([[['org.roles.read']], [['org.roles.read', 'org.roles.write']]])(
  'does not treat role definition write as role grant: %j',
  async (allowed) => {
    permissions = allowed;
    await render();
    expect(
      wrapper!
        .findAll('button')
        .some((b) => ['授予角色', '撤销授权'].includes(b.text())),
    ).toBe(false);
  },
);
it('does not read protected roles without role-read permission', async () => {
  permissions = ['org.roles.grant'];
  await render();
  expect(rolesApi.list).not.toHaveBeenCalled();
  expect(rolesApi.memberBindings).not.toHaveBeenCalled();
});
it('rechecks grant permission immediately before mutation', async () => {
  await render();
  await click('撤销授权');
  permissions = ['org.roles.read'];
  await confirm();
  expect(rolesApi.revoke).not.toHaveBeenCalled();
  expect(wrapper!.text()).toContain('没有此操作');
});
it('surfaces last-owner rejection without replaying or refreshing as success', async () => {
  vi.mocked(rolesApi.revoke).mockRejectedValue(
    new ApiError({ status: 409, code: 'org.last_owner', title: 'Rejected' }),
  );
  await render();
  await click('撤销授权');
  await confirm();
  expect(wrapper!.text()).toContain('org.last_owner');
  expect(rolesApi.revoke).toHaveBeenCalledTimes(1);
  expect(wrapper!.emitted('updated')).toBeUndefined();
});
it('uses the group ID as source without requiring group-read permission', async () => {
  permissions = ['org.roles.read'];
  await render();
  expect(orgAdminApi.groups).not.toHaveBeenCalled();
  expect(wrapper!.text()).toContain('team');
});
it('loads after the parent finishes rereading a member following a role change', async () => {
  wrapper = mount(MemberRoles, {
    props: { memberId: 'alice', memberName: 'Alice', disabled: true },
    global: { plugins: [createAurigaI18n('zh-CN'), ElementPlus] },
  });
  await flushPromises();
  expect(rolesApi.memberBindings).not.toHaveBeenCalled();
  await wrapper.setProps({ disabled: false });
  await flushPromises();
  expect(rolesApi.memberBindings).toHaveBeenCalledWith('alice');
  expect(wrapper.text()).toContain('直接绑定');
});
