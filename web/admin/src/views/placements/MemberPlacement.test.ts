import { mount, flushPromises } from '@vue/test-utils';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
afterEach(() => vi.restoreAllMocks());
import { createAurigaI18n, ApiError } from '@auriga/shared';
import { placementMessages } from './messages';
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
function setup(locale: 'zh-CN' | 'en' = 'zh-CN') {
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
      plugins: [createAurigaI18n(locale)],
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
it.each(['zh-CN', 'en'] as const)(
  'shows the complete mandatory paragraph in %s',
  async (locale) => {
    const { view } = setup(locale);
    await flushPromises();
    const required =
      locale === 'zh-CN'
        ? '共享池仅限互信团队。成员可能互相看到文件，并可能以 owner 能力操作。成员之间会话内容可见，且可能以其他成员的身份操作内部应用、审计记在被冒用者名下。同一进程卡死、崩溃或取消超时会中断全池任务。需要保密或独立故障域请使用独立容器。'
        : "Shared pools are for mutually trusted teams only. Members may see each other's files, act with owner capabilities, read each other's sessions and operate internal applications as another member, with audit attributed to the impersonated member. Stalls, crashes and cancellation timeouts interrupt the entire pool. Use a dedicated container for confidentiality or an independent failure domain.";
    expect(view.text()).toContain(required);
    expect(placementMessages[locale].risks).toContain(required);
    view.unmount();
  },
);
it.each([400, 403, 404, 409, 412, 428])(
  'unlocks after deterministic HTTP %s and refresh',
  async (status) => {
    const { view, port } = setup();
    vi.mocked(port.switchMember).mockRejectedValueOnce(
      new ApiError({
        status,
        title: 'rejected',
        code: 'common.precondition_failed',
      }),
    );
    await flushPromises();
    await view.find('textarea').setValue('maintenance');
    await view.find('input[type=checkbox]').setValue(true);
    await view.find('form').trigger('submit');
    await flushPromises();
    expect(
      view.findAll('button').some((b) => b.text() === '按原幂等键查证'),
    ).toBe(false);
    await view
      .findAll('button')
      .find((b) => b.text() === '刷新状态')!
      .trigger('click');
    await flushPromises();
    await view.find('input[type=checkbox]').setValue(true);
    await view.find('form').trigger('submit');
    await flushPromises();
    expect(port.switchMember).toHaveBeenCalledTimes(2);
    view.unmount();
  },
);
it.each(['操作已取消', 'Operation cancelled'])(
  'unlocks a cancelled step-up: %s',
  async (message) => {
    const { view, port } = setup();
    vi.mocked(port.switchMember).mockRejectedValueOnce(new Error(message));
    await flushPromises();
    await view.find('textarea').setValue('maintenance');
    await view.find('input[type=checkbox]').setValue(true);
    await view.find('form').trigger('submit');
    await flushPromises();
    expect(
      view.findAll('button').some((b) => b.text() === '按原幂等键查证'),
    ).toBe(false);
    await view
      .findAll('button')
      .find((b) => b.text() === '刷新状态')!
      .trigger('click');
    await flushPromises();
    await view.find('input[type=checkbox]').setValue(true);
    await view.find('form').trigger('submit');
    await flushPromises();
    expect(port.switchMember).toHaveBeenCalledTimes(2);
    view.unmount();
  },
);
it('keeps operation ID on refresh and discards it on member change', async () => {
  const { view, port } = setup();
  await flushPromises();
  await view.find('textarea').setValue('maintenance');
  await view.find('input[type=checkbox]').setValue(true);
  await view.find('form').trigger('submit');
  await flushPromises();
  await view
    .findAll('button')
    .find((b) => b.text() === '刷新状态')!
    .trigger('click');
  await flushPromises();
  expect(view.text()).toContain('op');
  expect(port.switchMember).toHaveBeenCalledTimes(1);
  await view.setProps({ memberId: 'other' });
  await flushPromises();
  expect(view.find('div[role=status]').exists()).toBe(false);
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
  expect(
    view.findAll('button').some((b) => b.text() === '按原幂等键查证'),
  ).toBe(false);
  membership.mockResolvedValue({
    permissions: ['workspace.placements.read', 'workspace.placements.write'],
  });
  await view
    .findAll('button')
    .find((b) => b.text() === '刷新状态')!
    .trigger('click');
  await flushPromises();
  await view.find('input[type=checkbox]').setValue(true);
  await view.find('form').trigger('submit');
  await flushPromises();
  expect(port.switchMember).toHaveBeenCalledTimes(1);
  view.unmount();
});
it('requires explicit confirmation to abandon an uncertain key and reread', async () => {
  const { view, port } = setup();
  vi.mocked(port.switchMember).mockRejectedValue(new Error('network'));
  await flushPromises();
  await view.find('textarea').setValue('maintenance');
  await view.find('input[type=checkbox]').setValue(true);
  await view.find('form').trigger('submit');
  await flushPromises();
  const confirm = vi.spyOn(globalThis, 'confirm').mockReturnValue(false);
  const refresh = () =>
    view.findAll('button').find((b) => b.text() === '刷新状态')!;
  await refresh().trigger('click');
  await flushPromises();
  expect(port.readMember).toHaveBeenCalledTimes(1);
  confirm.mockReturnValue(true);
  await refresh().trigger('click');
  await flushPromises();
  expect(port.readMember).toHaveBeenCalledTimes(2);
  expect(view.find('form').exists()).toBe(true);
  expect(
    view.findAll('button').some((b) => b.text() === '按原幂等键查证'),
  ).toBe(false);
  view.unmount();
});
