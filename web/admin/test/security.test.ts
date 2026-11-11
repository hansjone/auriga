import { flushPromises, mount } from '@vue/test-utils';
import { describe, expect, it, vi, beforeEach } from 'vitest';
import ElementPlus from 'element-plus';
import { createAurigaI18n } from '@auriga/shared';
import EgressView from '../src/views/security/EgressView.vue';
import EntriesView from '../src/views/security/EntriesView.vue';
import { securityApi } from '../src/admin/securityApi';
import { adminApi } from '../src/admin/api';
import ActionConfirm from '../src/admin/ActionConfirm.vue';
vi.mock('../src/admin/securityApi', () => ({
  securityApi: { entries: vi.fn(), egress: vi.fn(), save: vi.fn() },
}));
vi.mock('../src/admin/api', () => ({ adminApi: { membership: vi.fn() } }));
const options = () => ({
  global: { plugins: [createAurigaI18n('zh-CN'), ElementPlus] },
});
beforeEach(() => {
  vi.resetAllMocks();
  vi.mocked(adminApi.membership).mockResolvedValue({
    permissions: ['org.policies.read', 'org.policies.write'],
  } as never);
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
  it('shows saved version separately from unavailable observation', async () => {
    const wrapper = mount(EgressView, options());
    await flushPromises();
    expect(wrapper.text()).toContain('2');
    expect(wrapper.text()).toContain('\u5c1a\u672a\u63a5\u5165');
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
