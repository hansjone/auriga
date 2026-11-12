import { afterEach, describe, expect, it, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { createI18n } from 'vue-i18n';
import ElementPlus from 'element-plus';
import LifecycleView from '../src/views/ops/LifecycleView.vue';
import UpgradesView from '../src/views/ops/UpgradesView.vue';
import BackupsView from '../src/views/ops/BackupsView.vue';
import { adminApi } from '../src/admin/api';
import { settingsApi } from '../src/admin/settingsApi';
import { runtimeApi } from '../src/admin/runtimeApi';
import ActionConfirm from '../src/admin/ActionConfirm.vue';
import { ApiError } from '@auriga/shared';

vi.mock('../src/admin/api', () => ({
  adminApi: { membership: vi.fn(), instances: vi.fn() },
}));
vi.mock('../src/admin/settingsApi', async (original) => ({
  ...(await original<object>()),
  settingsApi: { workspace: vi.fn(), putWorkspace: vi.fn() },
}));
vi.mock('../src/admin/runtimeApi', () => ({
  runtimeApi: { runtimes: vi.fn() },
}));
const wrappers: ReturnType<typeof mount>[] = [];
function render(view: typeof LifecycleView) {
  const wrapper = mount(view, {
    global: {
      plugins: [ElementPlus, createI18n({ legacy: false, locale: 'zh-CN' })],
      stubs: { RouterLink: { template: '<a><slot /></a>' } },
    },
  });
  wrappers.push(wrapper);
  return wrapper;
}
const policy = {
  key: 'workspace',
  version: 1,
  value: { idleMinutes: 30, maxConcurrentStarts: 2, future: 'preserve' },
  updatedAt: '2026-11-01T00:00:00Z',
};
afterEach(() => {
  wrappers.splice(0).forEach((w) => w.unmount());
  vi.resetAllMocks();
});
async function editable() {
  vi.mocked(adminApi.membership).mockResolvedValue({
    permissions: ['org.policies.read', 'org.policies.write'],
  } as never);
  vi.mocked(settingsApi.workspace).mockResolvedValue({
    data: policy,
    etag: '"1"',
  } as never);
  const w = render(LifecycleView);
  await flushPromises();
  return w;
}
describe('runtime operations', () => {
  it.each(['-1', '0', '4.5', 'NaN', '2147483648', '9007199254740992'])(
    'rejects invalid idle duration %s',
    async (value) => {
      const w = await editable();
      await w.get('[data-testid="idle-minutes"]').setValue(value);
      await w.get('[data-testid="save-lifecycle"]').trigger('click');
      expect(settingsApi.putWorkspace).not.toHaveBeenCalled();
      expect(w.getComponent(ActionConfirm).props('message')).toBeUndefined();
      expect(w.text()).toContain('至少 5');
    },
  );
  it('rechecks write permission at confirmation', async () => {
    const w = await editable();
    await w.get('[data-testid="save-lifecycle"]').trigger('click');
    vi.mocked(adminApi.membership).mockResolvedValue({
      permissions: ['org.policies.read'],
    } as never);
    w.getComponent(ActionConfirm).vm.$emit('confirm');
    await flushPromises();
    expect(settingsApi.putWorkspace).not.toHaveBeenCalled();
    expect(w.text()).toContain('没有此操作');
  });
  it('requires reload after a stale ETag and does not replay the write', async () => {
    const w = await editable();
    vi.mocked(settingsApi.putWorkspace).mockRejectedValue(
      new ApiError({
        status: 412,
        code: 'common.precondition_failed',
        title: 'Conflict',
      }),
    );
    await w.get('[data-testid="save-lifecycle"]').trigger('click');
    w.getComponent(ActionConfirm).vm.$emit('confirm');
    await flushPromises();
    expect(settingsApi.putWorkspace).toHaveBeenCalledTimes(1);
    expect(w.find('[data-testid="save-lifecycle"]').exists()).toBe(false);
    expect(w.text()).toContain('数据已变化');
  });
  it('does not allow a second write if readback fails after saving', async () => {
    const w = await editable();
    vi.mocked(settingsApi.workspace).mockRejectedValue(new Error('offline'));
    await w.get('[data-testid="save-lifecycle"]').trigger('click');
    w.getComponent(ActionConfirm).vm.$emit('confirm');
    await flushPromises();
    expect(settingsApi.putWorkspace).toHaveBeenCalledTimes(1);
    expect(
      w.get('[data-testid="save-lifecycle"]').attributes('disabled'),
    ).toBeDefined();
  });
  it('discards confirmation on cancel', async () => {
    const w = await editable();
    await w.get('[data-testid="save-lifecycle"]').trigger('click');
    w.getComponent(ActionConfirm).vm.$emit('cancel');
    await flushPromises();
    expect(settingsApi.putWorkspace).not.toHaveBeenCalled();
    expect(w.getComponent(ActionConfirm).props('message')).toBeUndefined();
  });
  it('does not request protected data without permission', async () => {
    vi.mocked(adminApi.membership).mockResolvedValue({
      permissions: [],
    } as never);
    const w = render(LifecycleView);
    await flushPromises();
    expect(w.text()).toContain('没有此操作');
    expect(settingsApi.workspace).not.toHaveBeenCalled();
  });
  it('validates idle boundaries and requires confirmation before saving with ETag', async () => {
    vi.mocked(adminApi.membership).mockResolvedValue({
      permissions: ['org.policies.read', 'org.policies.write'],
    } as never);
    vi.mocked(settingsApi.workspace).mockResolvedValue({
      data: policy,
      etag: '"1"',
    } as never);
    const w = render(LifecycleView);
    await flushPromises();
    await w.get('[data-testid="idle-minutes"]').setValue('4');
    await w.get('[data-testid="save-lifecycle"]').trigger('click');
    expect(settingsApi.putWorkspace).not.toHaveBeenCalled();
    expect(w.text()).toContain('至少 5');
    await w.get('[data-testid="idle-minutes"]').setValue('5');
    await w.get('[data-testid="save-lifecycle"]').trigger('click');
    expect(settingsApi.putWorkspace).not.toHaveBeenCalled();
    expect(w.getComponent(ActionConfirm).props('message')).toContain('30 → 5');
    w.getComponent(ActionConfirm).vm.$emit('confirm');
    await flushPromises();
    expect(settingsApi.putWorkspace).toHaveBeenCalledWith(
      { ...policy.value, idleMinutes: 5 },
      '"1"',
      expect.any(AbortSignal),
    );
  });
  it('disables saving without an authoritative ETag', async () => {
    vi.mocked(adminApi.membership).mockResolvedValue({
      permissions: ['org.policies.read', 'org.policies.write'],
    } as never);
    vi.mocked(settingsApi.workspace).mockResolvedValue({
      data: policy,
      etag: null,
    } as never);
    const w = render(LifecycleView);
    await flushPromises();
    expect(
      w.get('[data-testid="save-lifecycle"]').attributes('disabled'),
    ).toBeDefined();
  });
  it('shows available releases and never substitutes a deployment action', async () => {
    vi.mocked(adminApi.membership).mockResolvedValue({
      permissions: ['workspace.runtimes.read'],
    } as never);
    vi.mocked(runtimeApi.runtimes).mockResolvedValue({
      defaultVersion: '1.2.3',
      items: [
        {
          version: '1.2.3',
          instances: 2,
          artifacts: [
            {
              artifactId: 'release-1',
              os: 'linux',
              arch: 'amd64',
              digest: 'sha256:example',
              image: 'example.com/dsh@sha256:example',
            },
          ],
          createdAt: '2026-11-01T00:00:00Z',
        },
      ],
    });
    const w = render(UpgradesView);
    await flushPromises();
    expect(w.text()).toContain('1.2.3');
    expect(w.text()).toContain('实际运行镜像');
    expect(adminApi.instances).not.toHaveBeenCalled();
  });
  it('shows recovery instructions without inventing backup results', () => {
    const w = render(BackupsView);
    expect(w.find('[data-testid="trigger-backup"]').exists()).toBe(false);
    expect(w.text()).toContain('pg_restore');
  });
});
