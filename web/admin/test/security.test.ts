import { flushPromises, mount } from '@vue/test-utils';
import { describe, expect, it, vi, beforeEach } from 'vitest';
import ElementPlus from 'element-plus';
import { createAurigaI18n, ApiError } from '@auriga/shared';
import EgressView from '../src/views/security/EgressView.vue';
import EntriesView from '../src/views/security/EntriesView.vue';
import { securityApi } from '../src/admin/securityApi';
import { adminApi } from '../src/admin/api';
import ActionConfirm from '../src/admin/ActionConfirm.vue';
vi.mock('../src/admin/securityApi', () => ({
  securityApi: {
    entries: vi.fn(),
    egress: vi.fn(),
    status: vi.fn(),
    save: vi.fn(),
  },
}));
vi.mock('../src/admin/api', () => ({ adminApi: { membership: vi.fn() } }));
const options = () => ({
  global: { plugins: [createAurigaI18n('zh-CN'), ElementPlus] },
});
beforeEach(() => {
  vi.resetAllMocks();
  vi.mocked(securityApi.save).mockResolvedValue({
    key: 'egress',
    version: 3,
    value: { categories: [], allowDomains: [], denyDomains: [] },
    updatedAt: '2026-01-01T00:00:00Z',
  });
  vi.mocked(adminApi.membership).mockResolvedValue({
    permissions: ['org.policies.read', 'org.policies.write'],
  } as never);
  vi.mocked(securityApi.status).mockResolvedValue({
    savedVersion: '2',
    distributedVersion: '2',
    appliedVersion: '1',
    phase: 'distributed',
    nodeId: 'egress-primary',
  });
  vi.mocked(securityApi.egress).mockResolvedValue({
    data: {
      key: 'egress',
      version: 2,
      value: { allowDomains: ['api.example.com'], denyDomains: [] },
      updatedAt: '2026-01-01T00:00:00Z',
    },
    etag: '"2"',
  });
});
describe('security pages', () => {
  it('keeps a reader unable to edit', async () => {
    vi.mocked(adminApi.membership).mockResolvedValue({
      permissions: ['org.policies.read'],
    } as never);
    const wrapper = mount(EgressView, options());
    await flushPromises();
    expect(
      wrapper.get('[data-testid="save-policy"]').attributes('disabled'),
    ).toBeDefined();
    expect(securityApi.save).not.toHaveBeenCalled();
    wrapper.unmount();
  });
  it('rechecks permission after confirmation', async () => {
    const wrapper = mount(EgressView, options());
    await flushPromises();
    await wrapper.get('[data-testid="save-policy"]').trigger('click');
    vi.mocked(adminApi.membership).mockResolvedValue({
      permissions: [],
    } as never);
    wrapper.findComponent(ActionConfirm).vm.$emit('confirm');
    await flushPromises();
    expect(securityApi.save).not.toHaveBeenCalled();
    expect(wrapper.find('[data-testid="save-policy"]').exists()).toBe(false);
    wrapper.unmount();
  });
  it('fails closed when there is no response ETag', async () => {
    vi.mocked(securityApi.egress).mockResolvedValue({
      data: {
        key: 'egress',
        version: 2,
        value: {},
        updatedAt: '2026-01-01T00:00:00Z',
      },
      etag: null,
    });
    const wrapper = mount(EgressView, options());
    await flushPromises();
    expect(
      wrapper.get('[data-testid="save-policy"]').attributes('disabled'),
    ).toBeDefined();
    wrapper.unmount();
  });
  it('does not drop unknown policy fields during editing', async () => {
    vi.mocked(securityApi.egress).mockResolvedValue({
      data: {
        key: 'egress',
        version: 2,
        value: { unknown: true } as never,
        updatedAt: '2026-01-01T00:00:00Z',
      },
      etag: '"2"',
    });
    const wrapper = mount(EgressView, options());
    await flushPromises();
    expect(
      wrapper.get('[data-testid="save-policy"]').attributes('disabled'),
    ).toBeDefined();
    wrapper.unmount();
  });
  it('does not request protected data without permission', async () => {
    vi.mocked(adminApi.membership).mockResolvedValue({
      permissions: [],
    } as never);
    const wrapper = mount(EgressView, options());
    await flushPromises();
    expect(securityApi.egress).not.toHaveBeenCalled();
    expect(wrapper.text()).toContain('\u6743\u9650');
    wrapper.unmount();
  });
  it('shows saved and applied versions separately', async () => {
    const wrapper = mount(EgressView, options());
    await flushPromises();
    expect(wrapper.text()).toContain('2');
    expect(wrapper.get('[data-testid="egress-phase"]').text()).toBe(
      '\u5df2\u4e0b\u53d1',
    );
    expect(wrapper.find('[data-testid="save-policy"]').exists()).toBe(true);
    await wrapper.get('[data-testid="save-policy"]').trigger('click');
    expect(securityApi.save).not.toHaveBeenCalled();
    await wrapper.findComponent(ActionConfirm).vm.$emit('confirm');
    await flushPromises();
    expect(securityApi.save).toHaveBeenCalledWith(
      expect.anything(),
      '"2"',
      expect.anything(),
    );
    expect(wrapper.get('[data-testid="egress-phase"]').text()).toBe(
      '\u5df2\u4fdd\u5b58',
    );
    wrapper.unmount();
  });
  it('preserves draft and requires refresh after failed save', async () => {
    vi.mocked(securityApi.save).mockRejectedValue(new Error('conflict'));
    const wrapper = mount(EgressView, options());
    await flushPromises();
    await wrapper.get('[data-testid="save-policy"]').trigger('click');
    await wrapper.findComponent(ActionConfirm).vm.$emit('confirm');
    await flushPromises();
    expect(
      wrapper.get('[data-testid="save-policy"]').attributes('disabled'),
    ).toBeDefined();
    expect(wrapper.text()).toContain('\u5237\u65b0');
    wrapper.unmount();
  });
  it.each([
    ['effective', '\u5df2\u751f\u6548'],
    ['failed', '\u5931\u8d25'],
    ['stale', '\u56de\u62a5\u5df2\u8fc7\u671f'],
    ['saved', '\u5df2\u4fdd\u5b58'],
  ] as const)('renders the reported %s state', async (phase, label) => {
    vi.mocked(securityApi.status).mockResolvedValue({
      savedVersion: '2',
      phase,
      nodeId: 'egress-primary',
    });
    const wrapper = mount(EgressView, options());
    await flushPromises();
    expect(wrapper.get('[data-testid="egress-phase"]').text()).toBe(label);
    wrapper.unmount();
  });
  it('does not claim effectiveness when observation fails', async () => {
    vi.mocked(securityApi.status).mockRejectedValue(new Error('offline'));
    const wrapper = mount(EgressView, options());
    await flushPromises();
    expect(wrapper.get('[data-testid="egress-phase"]').text()).not.toContain(
      '\u5df2\u751f\u6548',
    );
    expect(wrapper.find('[data-testid="save-policy"]').exists()).toBe(true);
    wrapper.unmount();
  });
  it('clears protected data when the status endpoint revokes access', async () => {
    vi.mocked(securityApi.status).mockRejectedValue(
      new ApiError({
        type: 'about:blank',
        title: 'Forbidden',
        status: 403,
        code: 'common.forbidden',
      }),
    );
    const wrapper = mount(EgressView, options());
    await flushPromises();
    expect(wrapper.find('[data-testid="save-policy"]').exists()).toBe(false);
    expect(wrapper.text()).toContain('\u6743\u9650');
    wrapper.unmount();
  });
  it('renders configured entries and no TLS health claims', async () => {
    vi.mocked(securityApi.entries).mockResolvedValue({
      mode: 'ip',
      portalOrigin: 'http://192.0.2.1:8080',
      adminOrigin: 'http://192.0.2.1:8081',
      instanceParentDomain: '',
      ingressKind: 'direct',
    });
    const wrapper = mount(EntriesView, options());
    await flushPromises();
    expect(wrapper.text()).toContain('http://192.0.2.1:8081');
    expect(wrapper.text()).toContain('\u914d\u7f6e');
    wrapper.unmount();
  });
});
