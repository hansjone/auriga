import { describe, it, expect, vi } from 'vitest';
import { devicesApi } from '../src/admin/devicesApi';
import { apiClient } from '../src/api';
import { router } from '../src/router';
import AlertsView from '../src/views/ops/AlertsView.vue';
import NotificationsView from '../src/views/ops/NotificationsView.vue';
import DevicesView from '../src/views/DevicesView.vue';
import UpgradesView from '../src/views/ops/UpgradesView.vue';
import BackupsView from '../src/views/ops/BackupsView.vue';
import LifecycleView from '../src/views/ops/LifecycleView.vue';

describe('merged administration routes', () => {
  it('resolves devices and both operations pages to their implemented views', () => {
    for (const [path, view] of [
      ['/runtime/devices', DevicesView],
      ['/ops/alerts', AlertsView],
      ['/ops/notifications', NotificationsView],
      ['/ops/upgrades', UpgradesView],
      ['/ops/backups', BackupsView],
      ['/ops/lifecycle', LifecycleView],
    ] as const) {
      expect(router.resolve(path).matched.at(-1)?.components?.default).toBe(
        view,
      );
    }
  });
});
describe('devices API', () => {
  it('preserves the acknowledged revocation when read-back is temporarily unavailable', async () => {
    const previous = {
      id: 'device',
      status: 'active',
      online: 'online',
    } as Awaited<ReturnType<typeof devicesApi.get>>;
    vi.spyOn(apiClient, 'delete').mockResolvedValue({
      credentialRevoked: true,
      disconnectStatus: 'pending',
    });
    vi.spyOn(apiClient, 'get').mockRejectedValue(
      new ApiError({
        status: 503,
        code: 'common.unavailable',
        title: 'Unavailable',
      }),
    );
    expect(await devicesApi.revoke('device', previous)).toMatchObject({
      status: 'revoked',
      online: 'unknown',
      disconnectStatus: 'pending',
    });
    vi.restoreAllMocks();
  });
  it('requires an offline read-back after credential revocation', async () => {
    vi.spyOn(apiClient, 'delete').mockResolvedValue(undefined);
    vi.spyOn(apiClient, 'get').mockResolvedValue({
      id: 'device',
      status: 'revoked',
      online: 'unknown',
    });
    expect((await devicesApi.revoke('device')).disconnectStatus).toBe(
      'pending',
    );
    vi.restoreAllMocks();
  });
  it('does not report success when read-back still shows active access', async () => {
    vi.spyOn(apiClient, 'delete').mockResolvedValue(undefined);
    vi.spyOn(apiClient, 'get').mockResolvedValue({
      id: 'device',
      status: 'active',
    });
    await expect(devicesApi.revoke('device')).rejects.toThrow(
      'Revocation not confirmed',
    );
    vi.restoreAllMocks();
  });
  it('encodes filters and reads back after revoke', async () => {
    const get = vi.spyOn(apiClient, 'get').mockResolvedValue({
      id: 'device',
      status: 'revoked',
      online: 'offline',
    });
    const del = vi.spyOn(apiClient, 'delete').mockResolvedValue(undefined);
    await devicesApi.list('member /', 'unknown');
    expect(get).toHaveBeenCalledWith(
      '/api/iam/v1/admin/devices?member=member+%2F&online=unknown',
      { cache: 'no-store' },
    );
    await devicesApi.revoke('device');
    expect(del).toHaveBeenCalledWith('/api/iam/v1/admin/devices/device');
    expect(get).toHaveBeenLastCalledWith('/api/iam/v1/admin/devices/device', {
      cache: 'no-store',
    });
    vi.restoreAllMocks();
  });
});

import { mount, flushPromises, type VueWrapper } from '@vue/test-utils';
import { afterEach } from 'vitest';
import ElementPlus from 'element-plus';
import { ApiError, createAurigaI18n } from '@auriga/shared';
import { adminApi } from '../src/admin/api';
let wrapper: VueWrapper | undefined;
afterEach(() => {
  wrapper?.unmount();
  wrapper = undefined;
  document.body.innerHTML = '';
  vi.restoreAllMocks();
});
const device = {
  id: 'device',
  accountId: 'member',
  memberName: 'Alice',
  name: 'Desktop',
  status: 'active' as const,
  online: 'unknown' as const,
  accessMode: 'native_tx' as const,
};
function page() {
  wrapper = mount(DevicesView, {
    attachTo: document.body,
    global: { plugins: [ElementPlus, createAurigaI18n()] },
  });
  return wrapper;
}
describe('field terminals page', () => {
  it('reads back a 503 revocation, updates the drawer and permits retrying disconnect', async () => {
    vi.spyOn(adminApi, 'membership').mockResolvedValue({
      permissions: ['iam.network.manage'],
    } as never);
    vi.spyOn(devicesApi, 'list').mockResolvedValue({ items: [device] });
    vi.spyOn(devicesApi, 'get').mockResolvedValue(device);
    const view = page();
    await flushPromises();
    await view
      .findAll('button')
      .find((button) => button.text() === '详情')!
      .trigger('click');
    await flushPromises();
    vi.spyOn(apiClient, 'delete').mockRejectedValue(
      new ApiError({
        status: 503,
        code: 'common.unavailable',
        title: 'Unavailable',
      }),
    );
    vi.mocked(devicesApi.get).mockResolvedValue({
      ...device,
      status: 'revoked',
      online: 'online',
    });
    Array.from(document.querySelectorAll('button'))
      .find((button) => button.textContent?.trim() === '撤销接入')!
      .click();
    await flushPromises();
    (
      document.querySelector(
        '[data-testid="confirm-action"]',
      ) as HTMLButtonElement
    ).click();
    await flushPromises();
    expect(document.body.textContent).toContain('凭据已撤销，断开连接未确认');
    expect(document.body.textContent).not.toContain('已读回：终端接入已撤销');
    expect(
      Array.from(document.querySelectorAll('button')).some(
        (button) => button.textContent?.trim() === '重试断开',
      ),
    ).toBe(true);
    vi.mocked(apiClient.delete).mockResolvedValue(undefined);
    vi.mocked(devicesApi.get).mockResolvedValue({
      ...device,
      status: 'revoked',
      online: 'offline',
    });
    Array.from(document.querySelectorAll('button'))
      .find((button) => button.textContent?.trim() === '重试断开')!
      .click();
    await flushPromises();
    (
      document.querySelector(
        '[data-testid="confirm-action"]',
      ) as HTMLButtonElement
    ).click();
    await flushPromises();
    expect(document.body.textContent).toContain('已读回：终端接入已撤销');
  });
  it('renders authoritative heartbeat and reports monitoring outages as unknown', async () => {
    vi.spyOn(adminApi, 'membership').mockResolvedValue({
      permissions: ['iam.network.manage'],
    } as never);
    vi.spyOn(devicesApi, 'list').mockResolvedValue({
      items: [
        {
          ...device,
          online: 'online',
          lastHeartbeatAt: '2026-11-11T12:00:00Z',
        },
      ],
    });
    const view = page();
    await flushPromises();
    expect(view.text()).toContain('2026-11-11T12:00:00Z');
    expect(view.text()).toContain('在线');
    vi.mocked(devicesApi.list).mockResolvedValue({ items: [device] });
    await view
      .findAll('button')
      .find((button) => button.text() === '刷新')!
      .trigger('click');
    await flushPromises();
    expect(view.text()).toContain('状态未知');
    expect(view.text()).not.toContain('2026-11-11T12:00:00Z');
  });
  it('rejects missing permissions before listing', async () => {
    vi.spyOn(adminApi, 'membership').mockResolvedValue({
      permissions: [],
    } as never);
    const list = vi.spyOn(devicesApi, 'list');
    const view = page();
    await flushPromises();
    expect(view.text()).toContain('没有此操作所需权限');
    expect(list).not.toHaveBeenCalled();
  });
  it('shows real rows and honest unavailable telemetry', async () => {
    vi.spyOn(adminApi, 'membership').mockResolvedValue({
      permissions: ['iam.network.manage'],
    } as never);
    vi.spyOn(devicesApi, 'list').mockResolvedValue({ items: [device] });
    const view = page();
    await flushPromises();
    expect(view.text()).toContain('Desktop');
    expect(view.text()).toContain('Alice');
    expect(view.text()).toContain('尚未接入');
  });
  it('confirms revocation and displays the read-back result', async () => {
    vi.spyOn(adminApi, 'membership').mockResolvedValue({
      permissions: ['iam.network.manage'],
    } as never);
    vi.spyOn(devicesApi, 'list').mockResolvedValue({ items: [device] });
    vi.spyOn(devicesApi, 'get').mockResolvedValue(device);
    const revoke = vi
      .spyOn(devicesApi, 'revoke')
      .mockResolvedValue({ ...device, status: 'revoked', online: 'offline' });
    const view = page();
    await flushPromises();
    const details = view.findAll('button').find((b) => b.text() === '详情');
    await details!.trigger('click');
    await flushPromises();
    const button = Array.from(document.querySelectorAll('button')).find(
      (b) => b.textContent?.trim() === '撤销接入',
    );
    button!.click();
    await flushPromises();
    expect(revoke).not.toHaveBeenCalled();
    (
      document.querySelector(
        '[data-testid="confirm-action"]',
      ) as HTMLButtonElement
    ).click();
    await flushPromises();
    expect(revoke).toHaveBeenCalledWith('device', device);
    expect(document.body.textContent).toContain('已读回：终端接入已撤销');
  });
});
