import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { createI18n } from 'vue-i18n';
import ElementPlus, { ElSelect } from 'element-plus';
import { adminApi } from '../src/admin/api';
import { opsRuntimeApi } from '../src/admin/opsRuntimeApi';
import BackupsView from '../src/views/ops/BackupsView.vue';
import UpgradesView from '../src/views/ops/UpgradesView.vue';
import ActionConfirm from '../src/admin/ActionConfirm.vue';
vi.mock('../src/admin/api', () => ({ adminApi: { membership: vi.fn() } }));
vi.mock('../src/admin/opsRuntimeApi', () => ({
  opsRuntimeApi: {
    upgrades: vi.fn(),
    register: vi.fn(),
    backups: vi.fn(),
    plan: vi.fn(),
    trigger: vi.fn(),
    download: vi.fn(),
    remove: vi.fn(),
  },
}));
const wrappers: ReturnType<typeof mount>[] = [];
let permissions: string[];
const backupStatus = {
  plan: { enabled: false, intervalHours: 24, revision: 3 },
  items: [
    {
      id: '01900000-0000-7000-8000-000000000001',
      status: 'succeeded',
      createdAt: '2026-11-11T00:00:00Z',
      sizeBytes: 18,
      sha256: 'a'.repeat(64),
    },
  ],
  worker: null,
};
function render(view: typeof BackupsView) {
  const w = mount(view, {
    global: {
      plugins: [ElementPlus, createI18n({ legacy: false, locale: 'zh-CN' })],
    },
  });
  wrappers.push(w);
  return w;
}
beforeEach(() => {
  permissions = [
    'ops.backups.read',
    'ops.backups.write',
    'ops.backups.download',
    'ops.upgrades.read',
    'ops.upgrades.write',
  ];
  vi.mocked(adminApi.membership).mockImplementation(
    async () => ({ permissions }) as never,
  );
  vi.mocked(opsRuntimeApi.backups).mockResolvedValue(backupStatus);
  vi.mocked(opsRuntimeApi.upgrades).mockResolvedValue({
    inventory: {
      reportedAt: '2026-11-11T00:00:00Z',
      items: [
        {
          name: 'platform',
          image: 'example.com/platform@sha256:actual',
          imageId: 'sha256:actual',
          state: 'running',
          version: '1.0',
        },
      ],
    },
    available: [
      {
        id: 'approved',
        label: 'Approved release',
        image: 'example.com/platform@sha256:new',
      },
    ],
    requests: [],
  });
});
afterEach(() => {
  wrappers.splice(0).forEach((w) => w.unmount());
  vi.resetAllMocks();
});
describe('runtime backend workflows', () => {
  it('requires read permission before requesting backup metadata', async () => {
    permissions = [];
    const w = render(BackupsView);
    await flushPromises();
    expect(opsRuntimeApi.backups).not.toHaveBeenCalled();
    expect(w.text()).toContain('没有此操作');
  });
  it('validates schedule bounds and preserves the authoritative revision', async () => {
    const w = render(BackupsView);
    await flushPromises();
    await w.get('[data-testid="backup-interval"]').setValue('721');
    await w.get('[data-testid="save-backup-plan"]').trigger('click');
    expect(opsRuntimeApi.plan).not.toHaveBeenCalled();
    expect(w.text()).toContain('1–720');
    await w.get('[data-testid="backup-interval"]').setValue('1');
    await w.get('[data-testid="save-backup-plan"]').trigger('click');
    expect(opsRuntimeApi.plan).not.toHaveBeenCalled();
    expect(w.getComponent(ActionConfirm).props('message')).toContain(
      '关闭定时备份',
    );
    expect(w.getComponent(ActionConfirm).props('message')).toContain(': 1');
    w.getComponent(ActionConfirm).vm.$emit('confirm');
    await flushPromises();
    expect(opsRuntimeApi.plan).toHaveBeenCalledWith(
      {
        enabled: false,
        intervalHours: 1,
        revision: 3,
        retentionCount: 7,
        retentionDays: 30,
        minFreeMiB: 1024,
      },
      expect.any(AbortSignal),
    );
  });
  it('validates retention and requires confirmation before deleting an archive', async () => {
    const w = render(BackupsView);
    await flushPromises();
    await w.get('[data-testid="retention-count"]').setValue('0');
    await w.get('[data-testid="save-backup-plan"]').trigger('click');
    expect(opsRuntimeApi.plan).not.toHaveBeenCalled();
    const button = w.findAll('button').find((b) => b.text() === '删除备份');
    await button!.trigger('click');
    expect(opsRuntimeApi.remove).not.toHaveBeenCalled();
    expect(w.getComponent(ActionConfirm).props('message')).toContain(
      '永久删除',
    );
    w.getComponent(ActionConfirm).vm.$emit('confirm');
    await flushPromises();
    expect(opsRuntimeApi.remove).toHaveBeenCalledWith(
      backupStatus.items[0].id,
      expect.any(AbortSignal),
    );
  });
  it('does not trigger after permission is revoked during confirmation', async () => {
    const w = render(BackupsView);
    await flushPromises();
    await w.get('[data-testid="trigger-backup"]').trigger('click');
    permissions = ['ops.backups.read'];
    w.getComponent(ActionConfirm).vm.$emit('confirm');
    await flushPromises();
    expect(opsRuntimeApi.trigger).not.toHaveBeenCalled();
  });
  it('blocks changed writes after an unknown trigger result until readback', async () => {
    const w = render(BackupsView);
    await flushPromises();
    vi.mocked(opsRuntimeApi.trigger).mockRejectedValue(
      new Error('connection lost'),
    );
    await w.get('[data-testid="trigger-backup"]').trigger('click');
    w.getComponent(ActionConfirm).vm.$emit('confirm');
    await flushPromises();
    expect(opsRuntimeApi.trigger).toHaveBeenCalledTimes(1);
    expect(w.find('[data-testid="trigger-backup"]').exists()).toBe(false);
    expect(w.text()).toContain('请求失败');
  });
  it('asks before issuing a session-bound archive download', async () => {
    const w = render(BackupsView);
    await flushPromises();
    const button = w.findAll('button').find((b) => b.text() === '下载与保存');
    expect(button).toBeDefined();
    await button!.trigger('click');
    expect(opsRuntimeApi.download).not.toHaveBeenCalled();
    expect(w.getComponent(ActionConfirm).props('message')).toContain('60 秒');
    w.getComponent(ActionConfirm).vm.$emit('confirm');
    await flushPromises();
    expect(opsRuntimeApi.download).toHaveBeenCalledWith(
      backupStatus.items[0].id,
      expect.any(AbortSignal),
    );
  });
  it('shows actual image IDs and only queues an approved target after confirmation', async () => {
    const w = render(UpgradesView);
    await flushPromises();
    expect(w.text()).toContain('sha256:actual');
    w.getComponent(ElSelect).vm.$emit('update:modelValue', 'approved');
    await flushPromises();
    await w.get('[data-testid="register-upgrade"]').trigger('click');
    expect(opsRuntimeApi.register).not.toHaveBeenCalled();
    w.getComponent(ActionConfirm).vm.$emit('confirm');
    await flushPromises();
    expect(opsRuntimeApi.register).toHaveBeenCalledWith(
      'approved',
      expect.any(String),
      expect.any(AbortSignal),
    );
  });
});
