import { mount, flushPromises } from '@vue/test-utils';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiError, createAurigaI18n, type Member } from '@auriga/shared';
import OverviewView from '../src/views/OverviewView.vue';
import { adminApi } from '../src/admin/api';
import { orgApi } from '../src/api';
import { router } from '../src/router';
import AuditView from '../src/views/AuditView.vue';
import AlertsView from '../src/views/ops/AlertsView.vue';
import NotificationsView from '../src/views/ops/NotificationsView.vue';
import UpgradesView from '../src/views/ops/UpgradesView.vue';
import BackupsView from '../src/views/ops/BackupsView.vue';
import LifecycleView from '../src/views/ops/LifecycleView.vue';

afterEach(() => vi.restoreAllMocks());
describe('overview snapshots', () => {
  it('makes overview the default and connects operations and audit pages', () => {
    expect(
      router
        .getRoutes()
        .some((route) => route.path === '/' && route.redirect === '/overview'),
    ).toBe(true);
    expect(
      router.resolve('/overview').matched.at(-1)?.components?.default,
    ).toBe(OverviewView);
    expect(
      router.resolve('/ops/alerts').matched.at(-1)?.components?.default,
    ).toBe(AlertsView);
    expect(
      router.resolve('/ops/notifications').matched.at(-1)?.components?.default,
    ).toBe(NotificationsView);
    expect(router.resolve('/audit').matched.at(-1)?.components?.default).toBe(
      AuditView,
    );
    expect(
      router.resolve('/ops/upgrades').matched.at(-1)?.components?.default,
    ).toBe(UpgradesView);
    expect(
      router.resolve('/ops/backups').matched.at(-1)?.components?.default,
    ).toBe(BackupsView);
    expect(
      router.resolve('/ops/lifecycle').matched.at(-1)?.components?.default,
    ).toBe(LifecycleView);
  });
  it('reads every page and leaves independent failures unknown', async () => {
    vi.spyOn(orgApi, 'listMembers')
      .mockResolvedValueOnce({ items: [], nextCursor: 'next' })
      .mockResolvedValue({ items: [] });
    vi.spyOn(adminApi, 'nodes').mockRejectedValue(new Error('offline'));
    vi.spyOn(adminApi, 'instances').mockResolvedValue({ items: [] });
    const wrapper = mount(OverviewView, {
      global: {
        plugins: [createAurigaI18n('zh-CN')],
        stubs: { RouterLink: { template: '<a><slot /></a>' } },
      },
    });
    await flushPromises();
    expect(orgApi.listMembers).toHaveBeenLastCalledWith({
      cursor: 'next',
      limit: 100,
    });
    expect(wrapper.get('[data-summary="nodes"]').text()).toContain('读取失败');
    expect(wrapper.get('[data-summary="members"]').text()).toContain(
      '暂无数据',
    );
    expect(wrapper.find('[data-summary="usage"]').exists()).toBe(false);
    expect(wrapper.find('a[href="/ai/ui/"]').exists()).toBe(false);
    expect(wrapper.text()).toContain('更新时间');
    wrapper.unmount();
  });
  it('counts later member pages and does not turn permission errors into zero', async () => {
    const member: Member = {
      id: 'member',
      displayName: 'Alice',
      memberType: 'member',
      status: 'active',
      groupIds: [],
      roleKeys: [],
      createdAt: '2026-10-27T00:00:00Z',
    };
    vi.spyOn(orgApi, 'listMembers')
      .mockResolvedValueOnce({ items: [member], nextCursor: 'next' })
      .mockResolvedValue({ items: [member] });
    vi.spyOn(adminApi, 'nodes').mockRejectedValue(
      new ApiError({
        status: 403,
        code: 'common.forbidden',
        title: 'Forbidden',
      }),
    );
    vi.spyOn(adminApi, 'instances').mockResolvedValue({ items: [] });
    const wrapper = mount(OverviewView, {
      global: {
        plugins: [createAurigaI18n('zh-CN')],
        stubs: { RouterLink: { template: '<a><slot /></a>' } },
      },
    });
    await flushPromises();
    expect(wrapper.get('[data-summary="members"] strong').text()).toBe('2');
    expect(wrapper.get('[data-summary="nodes"]').text()).toContain('无权查看');
    expect(wrapper.find('[data-summary="nodes"] strong').exists()).toBe(false);
    wrapper.unmount();
  });
});
