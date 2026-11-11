import { mount, flushPromises } from '@vue/test-utils';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { createAurigaI18n } from '@auriga/shared';
import AlertsView from '../src/views/ops/AlertsView.vue';
import ActionConfirm from '../src/admin/ActionConfirm.vue';
import NotificationsView from '../src/views/ops/NotificationsView.vue';
import { adminApi } from '../src/admin/api';
import { opsApi } from '../src/admin/opsApi';
const plugins = () => [createAurigaI18n('zh-CN')];
function permissions(perms: string[]) {
  vi.spyOn(adminApi, 'membership').mockResolvedValue({
    permissions: perms,
  } as Awaited<ReturnType<typeof adminApi.membership>>);
}
afterEach(() => vi.restoreAllMocks());
describe('ops pages', () => {
  it('creates only after confirmation and clears the credential', async () => {
    permissions(['ops.notifications.read', 'ops.notifications.write']);
    vi.spyOn(opsApi, 'channels').mockResolvedValue({ items: [] });
    const create = vi
      .spyOn(opsApi, 'create')
      .mockResolvedValue({ id: 'channel' });
    const w = mount(NotificationsView, { global: { plugins: plugins() } });
    await flushPromises();
    await w
      .findAll('button')
      .find((b) => b.text() === '新增渠道')!
      .trigger('click');
    await w.get('input[name="name"]').setValue('primary');
    await w.get('input[name="url"]').setValue('https://example.com/hook');
    await w.get('input[name="bearer"]').setValue('ephemeral');
    await w.get('form').trigger('submit');
    expect(create).not.toHaveBeenCalled();
    w.getComponent(ActionConfirm).vm.$emit('confirm');
    await flushPromises();
    expect(create).toHaveBeenCalledWith(
      expect.objectContaining({ name: 'primary', bearer: 'ephemeral' }),
      expect.any(AbortSignal),
    );
    expect(w.find('input[name="bearer"]').exists()).toBe(false);
    w.unmount();
  });
  it('does not request protected data without permission', async () => {
    permissions([]);
    const spy = vi.spyOn(opsApi, 'alerts');
    const w = mount(AlertsView, { global: { plugins: plugins() } });
    await flushPromises();
    expect(w.text()).toContain('无权限');
    expect(spy).not.toHaveBeenCalled();
    w.unmount();
  });
  it('loads alerts and filters resolved history', async () => {
    permissions(['ops.alerts.read']);
    vi.spyOn(opsApi, 'alerts').mockResolvedValue({
      limit: 500,
      items: [
        {
          id: 'a',
          source: 'node_offline',
          subject: 'node-a',
          severity: 'critical',
          first_seen: '2026-11-11T00:00:00Z',
          last_seen: '2026-11-11T00:00:00Z',
          acknowledged_at: null,
          silenced_until: null,
          resolved_at: null,
        },
      ],
    });
    const w = mount(AlertsView, { global: { plugins: plugins() } });
    await flushPromises();
    expect(w.text()).toContain('node-a');
    expect(
      w
        .findAll('tbody button')
        .every((b) => b.attributes('disabled') !== undefined),
    ).toBe(true);
    await w.findAll('select')[1]!.setValue('resolved');
    expect(w.text()).not.toContain('node-a');
    w.unmount();
  });
  it('shows channel metadata and clears secret fields on cancel', async () => {
    permissions(['ops.notifications.read', 'ops.notifications.write']);
    vi.spyOn(opsApi, 'channels').mockResolvedValue({
      items: [
        {
          id: 'a',
          name: 'channel-a',
          enabled: true,
          minSeverity: 'critical',
          configured: true,
        },
      ],
    });
    const w = mount(NotificationsView, { global: { plugins: plugins() } });
    await flushPromises();
    expect(w.text()).toContain('channel-a');
    await w.findAll('tbody button')[0]!.trigger('click');
    expect(w.get('input[name="url"]').element).toHaveProperty('value', '');
    await w.get('input[name="bearer"]').setValue('secret');
    await w.get('form button[type="button"]').trigger('click');
    expect(w.find('input[name="bearer"]').exists()).toBe(false);
    expect(w.text()).toContain('邮件尚未接入');
    w.unmount();
  });
});
