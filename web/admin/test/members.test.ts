import { mount, flushPromises } from '@vue/test-utils';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import ElementPlus from 'element-plus';
import {
  createAurigaI18n,
  disableAurigaMock,
  enableAurigaMock,
  resetMockState,
  setMockSession,
} from '@auriga/shared';
import MembersView from '../src/views/MembersView.vue';
import { adminApi } from '../src/admin/api';

beforeEach(async () => {
  resetMockState();
  await enableAurigaMock();
  setMockSession('admin');
  vi.spyOn(adminApi, 'membership').mockResolvedValue({
    permissions: ['org.members.read'],
  } as Awaited<ReturnType<typeof adminApi.membership>>);
});

afterEach(() => {
  disableAurigaMock();
  vi.restoreAllMocks();
});

describe('MembersView', () => {
  it('lists members from mock and filters', async () => {
    const wrapper = mount(MembersView, {
      global: {
        plugins: [createAurigaI18n('zh-CN'), ElementPlus],
      },
    });
    await flushPromises();
    expect(wrapper.text()).toContain('Alice');
    await wrapper.find('input').setValue('bob');
    await new Promise((r) => setTimeout(r, 300));
    await flushPromises();
    expect(wrapper.text()).toContain('Bob');
    expect(wrapper.text()).not.toContain('Alice');
    wrapper.unmount();
  });
});
