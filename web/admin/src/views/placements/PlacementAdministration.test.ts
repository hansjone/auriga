import { mount, flushPromises } from '@vue/test-utils';
import { beforeEach, expect, it, vi } from 'vitest';
import { createAurigaI18n, ApiError } from '@auriga/shared';
import View from './PlacementAdministration.vue';
const mock = vi.hoisted(() => ({
  membership: vi.fn(),
  capacity: vi.fn(),
  create: vi.fn(),
  plan: vi.fn(),
  update: vi.fn(),
  start: vi.fn(),
  progress: vi.fn(),
}));
vi.mock('../../admin/api', () => ({
  adminApi: { membership: mock.membership },
}));
vi.mock('./api', async (importOriginal) => ({
  ...(await importOriginal<object>()),
  placementAdminApi: mock,
}));
const memberId = '11111111-1111-4111-8111-111111111111',
  planId = '22222222-2222-4222-8222-222222222222';
const members = [
  {
    memberId,
    legacyInstanceId: '33333333-3333-4333-8333-333333333333',
    targetMode: 'dedicated' as const,
  },
];
const plan = {
  planId,
  revision: 1,
  state: 'draft',
  reason: 'maintenance',
  members,
};
function view() {
  return mount(View, { global: { plugins: [createAurigaI18n('zh-CN')] } });
}
async function click(wrapper: ReturnType<typeof view>, label: string) {
  await wrapper
    .findAll('button')
    .find((b) => b.text() === label)!
    .trigger('click');
  await flushPromises();
}
async function load(wrapper: ReturnType<typeof view>) {
  await wrapper.find('input').setValue(planId);
  await click(wrapper, '读取计划');
}
beforeEach(() => {
  vi.clearAllMocks();
  mock.membership.mockResolvedValue({
    permissions: ['workspace.placements.read', 'workspace.placements.write'],
  });
  mock.capacity.mockResolvedValue({
    observedAt: Math.floor(Date.now() / 1000),
    validUntil: Math.floor(Date.now() / 1000) + 60,
    pools: [],
    impacts: [],
  });
  mock.plan.mockResolvedValue({ data: plan, etag: '"opaque-plan"' });
  mock.create.mockResolvedValue(plan);
  mock.update.mockResolvedValue(plan);
  mock.start.mockResolvedValue({
    planId,
    revision: 2,
    state: 'running',
    members: [
      {
        memberId,
        revision: 1,
        state: 'accepted',
        sessionsDiscarded: false,
        workspaceEmpty: false,
      },
    ],
  });
});
it('does not read capacity without permission', async () => {
  mock.membership.mockResolvedValue({ permissions: [] });
  const w = view();
  await flushPromises();
  expect(mock.capacity).not.toHaveBeenCalled();
  expect(w.find('[role=alert]').exists()).toBe(false);
  expect(w.find('fieldset').attributes('disabled')).toBeDefined();
  w.unmount();
});
it('saves remain-dedicated lists without starting or deleting sessions', async () => {
  const w = view();
  await flushPromises();
  const row = w.find('.member-row');
  const inputs = row.findAll('input');
  await inputs[0].setValue(memberId);
  await inputs[1].setValue(members[0].legacyInstanceId);
  await row.find('select').setValue('dedicated');
  await w.find('textarea').setValue('maintenance');
  await w.find('form').trigger('submit');
  await flushPromises();
  expect(mock.create).toHaveBeenCalledWith(
    { reason: 'maintenance', members },
    expect.any(String),
    expect.any(AbortSignal),
  );
  expect(mock.start).not.toHaveBeenCalled();
  expect(w.text()).toContain('名单已保存');
  w.unmount();
});
it('rejects duplicate members rather than truncating the list', async () => {
  const w = view();
  await flushPromises();
  await load(w);
  await click(w, '添加成员');
  const rows = w.findAll('.member-row');
  await rows[1].findAll('input')[0].setValue(memberId);
  await rows[1]
    .findAll('input')[1]
    .setValue('44444444-4444-4444-8444-444444444444');
  await w.find('form').trigger('submit');
  await flushPromises();
  expect(mock.update).not.toHaveBeenCalled();
  expect(w.text()).toContain('不重复');
  w.unmount();
});
it('requires explicit export confirmation, preserves ETag, and treats 202 only as registered', async () => {
  const w = view();
  await flushPromises();
  await load(w);
  expect(w.find('.rebuild-risks').text()).toContain('审计记在被冒用者名下');
  expect(w.find('details').exists()).toBe(false);
  await click(w, '确认导出并开始重建');
  expect(mock.start).not.toHaveBeenCalled();
  await w.find('input[type=checkbox]').setValue(true);
  await click(w, '确认导出并开始重建');
  expect(mock.start).toHaveBeenCalledWith(
    planId,
    '"opaque-plan"',
    expect.any(String),
    expect.any(AbortSignal),
  );
  expect(w.text()).toContain('尚不代表完成');
  mock.progress.mockResolvedValue({
    planId,
    revision: 3,
    state: 'blocked',
    members: [
      {
        memberId,
        state: 'blocked',
        failureCode: 'stop-unconfirmed',
        revision: 1,
        sessionsDiscarded: false,
        workspaceEmpty: false,
      },
    ],
  });
  await click(w, '读取进度');
  expect(w.text()).toContain('无法确认旧执行已停止');
  w.unmount();
});
it('retries an uncertain start with the same key and label, without automatic resend', async () => {
  mock.start.mockRejectedValueOnce(new Error('network'));
  const w = view();
  await flushPromises();
  await load(w);
  await w.find('input[type=checkbox]').setValue(true);
  await click(w, '确认导出并开始重建');
  const args = mock.start.mock.calls[0];
  expect(mock.start).toHaveBeenCalledTimes(1);
  await click(w, '重试原请求');
  expect(mock.start.mock.calls[1]).toEqual(args);
  w.unmount();
});
it('clears stale ETag on 412 and requires reread before a new write', async () => {
  mock.start.mockRejectedValue(
    new ApiError({
      status: 412,
      title: 'changed',
      code: 'common.precondition_failed',
    }),
  );
  const w = view();
  await flushPromises();
  await load(w);
  await w.find('input[type=checkbox]').setValue(true);
  await click(w, '确认导出并开始重建');
  expect(w.text()).toContain('重新读取权威状态');
  expect(
    w
      .findAll('button')
      .find((b) => b.text() === '确认导出并开始重建')!
      .attributes('disabled'),
  ).toBeDefined();
  w.unmount();
});
it('renders unknown telemetry, expired admission and retained blocked pool impacts', async () => {
  const pool = {
    physicalInstanceId: planId,
    registered: 3,
    active: 1,
    registeredLimit: 20,
    activeLimit: 4,
    memoryUsedBytes: null,
    memoryLimitBytes: undefined,
    accepting: true,
  };
  const impact = {
    operationId: 'op',
    physicalInstanceId: planId,
    reason: 'cancel-timeout',
    state: 'blocked',
    observedAt: 1,
    affected: [{ memberId }],
  };
  mock.capacity.mockResolvedValueOnce({
    observedAt: 1,
    validUntil: 2,
    pools: [pool],
    impacts: [impact],
  });
  const w = view();
  await flushPromises();
  expect(w.text()).toContain('未知 / 未知');
  expect(w.text()).toContain('快照已过期');
  expect(w.text()).toContain('停止未确认');
  await click(w, '刷新');
  expect(w.text()).toContain('停止未确认');
  expect(w.text()).toContain(memberId);
  w.unmount();
});
it('rejects a completed response that omitted the planned member', async () => {
  const w = view();
  await flushPromises();
  await load(w);
  mock.progress.mockResolvedValue({
    planId,
    revision: 4,
    state: 'completed',
    members: [],
  });
  await click(w, '读取进度');
  expect(w.text()).toContain('请求未完成');
  expect(
    w.findAll('[role=status]').some((v) => v.text().includes('全部成功')),
  ).toBe(false);
  w.unmount();
});
it('uses unknown text for an unrecognized impact reason and state', async () => {
  mock.capacity.mockResolvedValue({
    observedAt: 1,
    validUntil: 2,
    pools: [],
    impacts: [
      {
        operationId: 'op',
        physicalInstanceId: planId,
        reason: 'future-reason',
        state: 'future-state',
        observedAt: 1,
        affected: [],
      },
    ],
  });
  const w = view();
  await flushPromises();
  expect(w.text()).not.toContain('reasons.future-reason');
  expect(w.text()).not.toContain('impactStates.future-state');
  expect(w.text()).toContain('未知');
  w.unmount();
});
