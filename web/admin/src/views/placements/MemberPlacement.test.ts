import { mount, flushPromises } from '@vue/test-utils';
import { beforeEach, expect, it, vi } from 'vitest';
import { createAurigaI18n, ApiError } from '@auriga/shared';
import MemberPlacement from './MemberPlacement.vue';
import {
  placementPortKey,
  type MemberPlacementSnapshot,
  type PlacementPort,
} from './api';
const membership = vi.hoisted(() => vi.fn());
vi.mock('../../admin/api', () => ({ adminApi: { membership } }));
const placement = {
  memberId: 'm',
  placementId: 'p',
  mode: 'dedicated' as const,
  state: 'ready' as const,
  revision: 1,
  epoch: 1,
};
const accepted = {
  operationId: 'op',
  memberId: 'm',
  fromMode: 'dedicated' as const,
  targetMode: 'shared' as const,
  state: 'accepted' as const,
  revision: 2,
  sessionsDiscarded: false,
};
function setup() {
  const port: PlacementPort = {
    entitlement: vi.fn().mockResolvedValue({}),
    lookup: vi.fn().mockResolvedValue(accepted),
    readMember: vi.fn().mockResolvedValue({
      placement,
      etag: '"opaque-tag"',
      dedicatedEntitled: true,
    }),
    switchMember: vi.fn().mockResolvedValue(accepted),
    progress: vi.fn().mockResolvedValue({
      ...accepted,
      state: 'blocked',
      failureCode: 'stop-unconfirmed',
      revision: 3,
    }),
  };
  const view = mount(MemberPlacement, {
    props: { memberId: 'm' },
    global: {
      plugins: [createAurigaI18n('zh-CN')],
      provide: { [placementPortKey as symbol]: port },
    },
  });
  return { view, port };
}
beforeEach(() =>
  membership.mockResolvedValue({
    permissions: ['workspace.placements.read', 'workspace.placements.write'],
  }),
);
it('denies reads and writes without placement permission', async () => {
  membership.mockResolvedValue({ permissions: [] });
  const { view, port } = setup();
  await flushPromises();
  expect(port.readMember).not.toHaveBeenCalled();
  expect(view.find('form').exists()).toBe(false);
  view.unmount();
});
it('requires impact confirmation, preserves opaque ETag, treats 202 as registered and blocked as unconfirmed', async () => {
  const { view, port } = setup();
  await flushPromises();
  expect(view.text()).toContain('成员之间会话内容可见');
  expect(view.text()).toContain('审计记在被冒用者名下');
  await view.find('textarea').setValue('maintenance');
  await view.find('form').trigger('submit');
  expect(port.switchMember).not.toHaveBeenCalled();
  await view.find('input[type=checkbox]').setValue(true);
  await view.find('form').trigger('submit');
  await flushPromises();
  expect(port.switchMember).toHaveBeenCalledWith(
    'm',
    {
      targetMode: 'shared',
      reason: 'maintenance',
      sessionPolicy: 'discard',
      filePolicy: 'archive-and-reset',
    },
    '"opaque-tag"',
    expect.any(String),
    expect.any(AbortSignal),
  );
  expect(view.text()).toContain('操作已登记，尚不表示新模式可用');
  const button = view
    .findAll('button')
    .find((b) => b.text().includes('重试查询'))!;
  await button.trigger('click');
  await flushPromises();
  expect(view.text()).toContain('无法确认旧执行已停止');
  expect(view.find('form').exists()).toBe(false);
  expect(port.switchMember).toHaveBeenCalledTimes(1);
  view.unmount();
});
it('never infers ETag or entitlement when admin authority is unavailable', async () => {
  const { view, port } = setup();
  vi.mocked(port.readMember).mockRejectedValue(new Error('unavailable'));
  await flushPromises();
  expect(view.find('form').exists()).toBe(false);
  expect(view.text()).toContain('切换已禁用');
  view.unmount();
});
it('discards a late read after changing members', async () => {
  let complete!: (value: MemberPlacementSnapshot) => void;
  const { view, port } = setup();
  vi.mocked(port.readMember).mockImplementationOnce(
    () =>
      new Promise((resolve) => {
        complete = resolve;
      }),
  );
  await flushPromises();
  await view.setProps({ memberId: 'other' });
  await flushPromises();
  complete({ placement, etag: 'old', dedicatedEntitled: true });
  await flushPromises();
  expect(view.find('form').exists()).toBe(false);
  view.unmount();
});
it('grants entitlement for an unassigned member without allocating or switching', async () => {
  const { view, port } = setup();
  vi.mocked(port.readMember).mockResolvedValue({
    etag: '"composite"',
    dedicatedEntitled: false,
  });
  await flushPromises();
  expect(view.text()).toContain('尚未分配');
  await view.find('textarea').setValue('approval');
  await view.find('input[type=checkbox]').setValue(true);
  await view
    .findAll('button')
    .find((b) => b.text() === '授予独立容器授权')!
    .trigger('click');
  await flushPromises();
  expect(port.entitlement).toHaveBeenCalledWith(
    'm',
    true,
    'approval',
    '"composite"',
    expect.any(String),
    expect.any(AbortSignal),
  );
  expect(port.switchMember).not.toHaveBeenCalled();
  view.unmount();
});
it('looks up an uncertain switch without resending, including after a 404', async () => {
  const { view, port } = setup();
  vi.mocked(port.switchMember).mockRejectedValue(new Error('network'));
  vi.mocked(port.lookup).mockRejectedValueOnce(
    new ApiError({ status: 404, title: 'missing', code: 'common.not_found' }),
  );
  await flushPromises();
  await view.find('textarea').setValue('maintenance');
  await view.find('input[type=checkbox]').setValue(true);
  await view.find('form').trigger('submit');
  await flushPromises();
  const key = vi.mocked(port.switchMember).mock.calls[0][3];
  const button = () =>
    view.findAll('button').find((b) => b.text() === '按原幂等键查证')!;
  await button().trigger('click');
  await flushPromises();
  expect(view.text()).toContain('404不证明从未执行');
  await button().trigger('click');
  await flushPromises();
  expect(port.lookup).toHaveBeenCalledWith('m', key, expect.any(AbortSignal));
  expect(port.switchMember).toHaveBeenCalledTimes(1);
  expect(view.text()).toContain('操作已登记');
  view.unmount();
});
it('rechecks revoked write permission immediately before a confirmed mutation', async () => {
  const { view, port } = setup();
  await flushPromises();
  await view.find('textarea').setValue('maintenance');
  await view.find('input[type=checkbox]').setValue(true);
  membership.mockResolvedValue({ permissions: ['workspace.placements.read'] });
  await view.find('form').trigger('submit');
  await flushPromises();
  expect(port.switchMember).not.toHaveBeenCalled();
  view.unmount();
});
