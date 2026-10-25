import { mount, flushPromises, type VueWrapper } from '@vue/test-utils';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { ApiError, createAurigaI18n } from '@auriga/shared';
import ElementPlus from 'element-plus';
import JoinMaterials from './JoinMaterials.vue';
import NodeDiagnostics from './NodeDiagnostics.vue';
import NodesView from '../NodesView.vue';
import { nodeProgress, retirementBlock } from './state';
import { nodesApi } from '../../admin/nodesApi';
import { adminApi, type Node } from '../../admin/api';

vi.mock('../../admin/nodesApi', () => ({
  nodesApi: {
    tokens: vi.fn(),
    create: vi.fn(),
    revokeToken: vi.fn(),
    retire: vi.fn(),
  },
}));
vi.mock('../../admin/api', () => ({
  adminApi: {
    membership: vi.fn(),
    nodes: vi.fn(),
    node: vi.fn(),
    updateNode: vi.fn(),
    drainNode: vi.fn(),
  },
}));
let wrapper: VueWrapper;
const node: Node = {
  id: 'node-example',
  name: 'Example',
  hostname: 'node.example.com',
  os: 'linux',
  arch: 'amd64',
  agentVersion: '1.0.0',
  drivers: ['docker'],
  status: 'draining',
  schedulable: false,
  labels: {},
  capacity: {},
  usage: { instancesTotal: 0 },
  joinedAt: '2026-10-25T00:00:00Z',
};
const token = {
  id: 'token-example',
  name: 'Example',
  createdBy: 'alice',
  createdAt: '2026-10-25T00:00:00Z',
  expiresAt: '2099-10-25T00:00:00Z',
};
const global = () => ({
  plugins: [ElementPlus, createAurigaI18n('zh-CN')],
  stubs: {
    ElDialog: {
      props: ['modelValue'],
      template: '<div v-if="modelValue"><slot/><slot name="footer"/></div>',
    },
  },
});
async function click(label: string) {
  if (label === '生成加入材料') {
    await wrapper.get('form').trigger('submit');
    await flushPromises();
    return;
  }
  const button = wrapper.findAll('button').find((b) => b.text() === label);
  expect(button, label).toBeDefined();
  await button!.trigger('click');
  await flushPromises();
}
beforeEach(() => {
  vi.mocked(nodesApi.tokens).mockResolvedValue({ items: [token] });
  vi.mocked(nodesApi.create).mockResolvedValue({
    ...token,
    token: 'secret-example',
    command: 'example join secret-example',
    caSha256: 'example-fingerprint',
  });
  vi.mocked(nodesApi.revokeToken).mockResolvedValue();
  vi.mocked(adminApi.membership).mockResolvedValue({
    permissions: ['workspace.nodes.read', 'workspace.nodes.admin'],
  } as Awaited<ReturnType<typeof adminApi.membership>>);
  vi.mocked(adminApi.nodes).mockResolvedValue({ items: [node] });
  vi.mocked(adminApi.node).mockResolvedValue(node);
  vi.mocked(nodesApi.retire).mockResolvedValue();
});
afterEach(() => {
  wrapper?.unmount();
  vi.clearAllMocks();
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

const denied = (status: number) =>
  new ApiError({
    status,
    code: status === 401 ? 'common.unauthenticated' : 'common.forbidden',
    title: 'Access denied',
  });
async function showMaterial() {
  wrapper = mount(NodesView, { global: global() });
  await flushPromises();
  await click('详情');
  await click('加入材料');
  await wrapper.getComponent(JoinMaterials).get('form').trigger('submit');
  await flushPromises();
  expect(wrapper.text()).toContain('secret-example');
}
it.each(['membership', 'nodes', 'node'] as const)(
  'clears secrets and write controls after polling %s rejects with 403',
  async (endpoint) => {
    vi.useFakeTimers();
    await showMaterial();
    vi.mocked(adminApi[endpoint]).mockRejectedValueOnce(denied(403));
    await vi.advanceTimersByTimeAsync(10000);
    await flushPromises();
    expect(wrapper.text()).not.toContain('secret-example');
    expect(wrapper.text()).not.toContain('复制命令');
    expect(wrapper.findComponent(JoinMaterials).exists()).toBe(false);
    expect(
      wrapper
        .findAll('button')
        .some((b) => ['加入材料', '保存配置', '退役节点'].includes(b.text())),
    ).toBe(false);
    expect(wrapper.text()).toContain('common.forbidden');
    const reads = vi.mocked(adminApi.membership).mock.calls.length;
    await vi.advanceTimersByTimeAsync(20000);
    expect(adminApi.membership).toHaveBeenCalledTimes(reads);
  },
);
it.each([401, 403])(
  'does not restore a late join issuance after polling rejection %s',
  async (status) => {
    vi.useFakeTimers();
    await showMaterial();
    await wrapper
      .getComponent(JoinMaterials)
      .findAll('button')
      .find((b) => b.text() === '关闭并清除')!
      .trigger('click');
    let resolve!: (value: Awaited<ReturnType<typeof nodesApi.create>>) => void;
    vi.mocked(nodesApi.create).mockImplementationOnce(
      () =>
        new Promise((r) => {
          resolve = r;
        }),
    );
    await wrapper.getComponent(JoinMaterials).get('form').trigger('submit');
    await flushPromises();
    vi.mocked(adminApi.nodes).mockRejectedValueOnce(denied(status));
    await vi.advanceTimersByTimeAsync(10000);
    await flushPromises();
    expect(wrapper.findComponent(JoinMaterials).exists()).toBe(false);
    resolve({
      ...token,
      token: 'late-secret',
      command: 'late-command',
      caSha256: 'example',
    });
    await flushPromises();
    expect(wrapper.text()).not.toContain('late-secret');
    expect(wrapper.text()).not.toContain('late-command');
    expect(wrapper.findComponent(JoinMaterials).exists()).toBe(false);
  },
);
it.each([
  new Error('offline'),
  new ApiError({
    status: 503,
    code: 'common.unavailable',
    title: 'Unavailable',
  }),
])(
  'keeps secrets and configuration drafts on a transient read failure, and permits manual retry: %s',
  async (readError) => {
    vi.useFakeTimers();
    await showMaterial();
    await wrapper.get('input[aria-label="名称"]').setValue('Draft node');
    vi.mocked(adminApi.nodes).mockRejectedValueOnce(readError);
    await vi.advanceTimersByTimeAsync(10000);
    await flushPromises();
    expect(wrapper.text()).toContain('secret-example');
    expect(wrapper.findComponent(JoinMaterials).exists()).toBe(true);
    expect(
      (wrapper.get('input[aria-label="名称"]').element as HTMLInputElement)
        .value,
    ).toBe('Draft node');
    expect(wrapper.text()).toContain('自动刷新已暂停');
    await click('刷新');
    expect(wrapper.text()).not.toContain('自动刷新已暂停');
    expect(wrapper.findComponent(JoinMaterials).exists()).toBe(true);
  },
);

it.each([['workspace.nodes.read'], []])(
  'invalidates join material when polling membership loses required permissions: %j',
  async (...permissions) => {
    vi.useFakeTimers();
    await showMaterial();
    const membership = await adminApi.membership();
    vi.mocked(adminApi.membership).mockResolvedValueOnce({
      ...membership,
      permissions,
    });
    await vi.advanceTimersByTimeAsync(10000);
    await flushPromises();
    expect(wrapper.findComponent(JoinMaterials).exists()).toBe(false);
    expect(wrapper.text()).not.toContain('secret-example');
    expect(wrapper.findAll('button').some((b) => b.text() === '加入材料')).toBe(
      false,
    );
  },
);

it('blocks a confirmed write waiting for membership after an in-flight poll denies access, and only recovers on manual refresh', async () => {
  vi.useFakeTimers();
  await showMaterial();
  let rejectPoll!: (error: ApiError) => void;
  vi.mocked(adminApi.nodes).mockImplementationOnce(
    () =>
      new Promise((_, reject) => {
        rejectPoll = reject;
      }),
  );
  await vi.advanceTimersByTimeAsync(10000);
  await flushPromises();
  await wrapper.get('.details').get('form').trigger('submit');
  await flushPromises();
  let resolveMembership!: (
    value: Awaited<ReturnType<typeof adminApi.membership>>,
  ) => void;
  vi.mocked(adminApi.membership).mockImplementationOnce(
    () =>
      new Promise((resolve) => {
        resolveMembership = resolve;
      }),
  );
  await click('确认操作');
  rejectPoll(denied(403));
  await flushPromises();
  expect(wrapper.findComponent(JoinMaterials).exists()).toBe(false);
  resolveMembership({
    permissions: ['workspace.nodes.read', 'workspace.nodes.admin'],
  } as Awaited<ReturnType<typeof adminApi.membership>>);
  await flushPromises();
  expect(adminApi.updateNode).not.toHaveBeenCalled();
  expect(wrapper.findAll('button').some((b) => b.text() === '加入材料')).toBe(
    false,
  );
  await click('刷新');
  expect(wrapper.findAll('button').some((b) => b.text() === '加入材料')).toBe(
    true,
  );
  expect(wrapper.findComponent(JoinMaterials).exists()).toBe(false);
  expect(wrapper.text()).not.toContain('secret-example');
});

it('confirms retirement before deletion and rechecks fresh node state', async () => {
  wrapper = mount(NodesView, { global: global() });
  await flushPromises();
  await click('详情');
  await click('退役节点');
  expect(nodesApi.retire).not.toHaveBeenCalled();
  await click('确认操作');
  expect(nodesApi.retire).toHaveBeenCalledWith(node.id);
  expect(wrapper.text()).toContain('节点已经退役');
});
it('opens join material on demand, preserves the detail form and clears material when hidden', async () => {
  wrapper = mount(NodesView, { global: global() });
  await flushPromises();
  expect(nodesApi.tokens).not.toHaveBeenCalled();
  await click('详情');
  await click('加入材料');
  expect(nodesApi.tokens).toHaveBeenCalledTimes(1);
  expect(wrapper.get('h2').text()).toBe(node.name);
  expect(wrapper.get('form').find('input[aria-label="名称"]').exists()).toBe(
    true,
  );
  await wrapper.getComponent(JoinMaterials).get('form').trigger('submit');
  await flushPromises();
  expect(wrapper.text()).toContain('secret-example');
  await click('加入材料');
  expect(wrapper.findComponent(JoinMaterials).exists()).toBe(false);
  expect(wrapper.text()).not.toContain('secret-example');
  await click('加入材料');
  expect(wrapper.text()).not.toContain('secret-example');
  expect(nodesApi.create).toHaveBeenCalledTimes(1);
});
it('rejects retirement when instances appear after confirmation opens', async () => {
  wrapper = mount(NodesView, { global: global() });
  await flushPromises();
  await click('详情');
  await click('退役节点');
  vi.mocked(adminApi.node).mockResolvedValue({
    ...node,
    usage: { instancesTotal: 1 },
  });
  await click('确认操作');
  expect(nodesApi.retire).not.toHaveBeenCalled();
  expect(wrapper.text()).toContain('节点仍有实例');
});
it('refreshes at ten seconds and stops on unmount', async () => {
  vi.useFakeTimers();
  wrapper = mount(NodesView, { global: global() });
  await flushPromises();
  expect(adminApi.nodes).toHaveBeenCalledTimes(1);
  await vi.advanceTimersByTimeAsync(9999);
  expect(adminApi.nodes).toHaveBeenCalledTimes(1);
  await vi.advanceTimersByTimeAsync(1);
  await flushPromises();
  expect(adminApi.nodes).toHaveBeenCalledTimes(2);
  wrapper.unmount();
  await vi.advanceTimersByTimeAsync(20000);
  expect(adminApi.nodes).toHaveBeenCalledTimes(2);
});

it('pauses automatic refresh after a failed read instead of retrying forever', async () => {
  vi.useFakeTimers();
  wrapper = mount(NodesView, { global: global() });
  await flushPromises();
  vi.mocked(adminApi.nodes).mockRejectedValueOnce(new Error('offline'));
  await vi.advanceTimersByTimeAsync(10000);
  await flushPromises();
  expect(wrapper.text()).toContain('自动刷新已暂停');
  await vi.advanceTimersByTimeAsync(30000);
  expect(adminApi.nodes).toHaveBeenCalledTimes(2);
});

it('shows join material once, copies it and clears it after closing', async () => {
  const writeText = vi.fn().mockResolvedValue(undefined);
  vi.stubGlobal('navigator', { clipboard: { writeText } });
  wrapper = mount(JoinMaterials, {
    props: { canWrite: true, refreshKey: 0 },
    global: global(),
  });
  await flushPromises();
  await click('生成加入材料');
  expect(wrapper.text()).toContain('secret-example');
  await click('复制命令');
  expect(writeText).toHaveBeenCalledWith('example join secret-example');
  await click('关闭并清除');
  expect(wrapper.text()).not.toContain('secret-example');
  await wrapper.setProps({ refreshKey: 1 });
  await flushPromises();
  expect(wrapper.text()).not.toContain('secret-example');
  expect(nodesApi.create).toHaveBeenCalledTimes(1);
  vi.unstubAllGlobals();
});
it('requires confirmation before revoking an unused token', async () => {
  wrapper = mount(JoinMaterials, {
    props: { canWrite: true, refreshKey: 0 },
    global: global(),
  });
  await flushPromises();
  await click('撤销');
  expect(nodesApi.revokeToken).not.toHaveBeenCalled();
  await click('取消');
  expect(nodesApi.revokeToken).not.toHaveBeenCalled();
  await click('撤销');
  await click('确认操作');
  expect(nodesApi.revokeToken).toHaveBeenCalledWith(token.id);
});
it('discards late create responses after permission loss', async () => {
  let resolve!: (value: Awaited<ReturnType<typeof nodesApi.create>>) => void;
  vi.mocked(nodesApi.create).mockImplementation(
    () =>
      new Promise((r) => {
        resolve = r;
      }),
  );
  wrapper = mount(JoinMaterials, {
    props: { canWrite: true, refreshKey: 0 },
    global: global(),
  });
  await flushPromises();
  await click('生成加入材料');
  await wrapper.setProps({ canWrite: false });
  resolve({
    ...token,
    token: 'late-secret',
    command: 'late-command',
    caSha256: 'example',
  });
  await flushPromises();
  expect(wrapper.text()).not.toContain('late-secret');
});
it('maps unused material and all contract node states without inventing pending node status', () => {
  expect(nodeProgress()).toBe('waiting');
  for (const status of ['online', 'offline', 'draining', 'revoked'] as const)
    expect(nodeProgress({ ...node, status })).toBe(status);
});
it('offers retirement only for a drained, unschedulable, known empty node', async () => {
  expect(retirementBlock(node)).toBeUndefined();
  wrapper = mount(NodeDiagnostics, {
    props: { node, canWrite: true, locked: false },
    global: global(),
  });
  await click('退役节点');
  expect(wrapper.emitted('retire')).toHaveLength(1);
});
it.each([
  { ...node, status: 'online' as const },
  { ...node, usage: undefined },
  { ...node, usage: { instancesTotal: 1 } },
  { ...node, schedulable: true },
  { ...node, status: 'revoked' as const },
])(
  'blocks retirement when drain/empty conditions are not met: %j',
  async (value) => {
    expect(retirementBlock(value)).toBeDefined();
    wrapper = mount(NodeDiagnostics, {
      props: { node: value, canWrite: true, locked: false },
      global: global(),
    });
    const button = wrapper
      .findAll('button')
      .find((b) => b.text() === '退役节点')!;
    expect(button.attributes('disabled')).toBeDefined();
    expect(wrapper.find('[data-testid="retirement-reason"]').text()).not.toBe(
      '',
    );
    await button.trigger('click');
    expect(wrapper.emitted('retire')).toBeUndefined();
  },
);
