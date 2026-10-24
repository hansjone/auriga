import { mount, flushPromises } from '@vue/test-utils';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { createPinia } from 'pinia';
import { createMemoryHistory, createRouter } from 'vue-router';
import ElementPlus from 'element-plus';
import {
  createAurigaI18n,
  disableAurigaMock,
  enableAurigaMock,
  resetMockState,
  setMockSession,
} from '@auriga/shared';
import AdminLayout from '../src/layouts/AdminLayout.vue';

beforeEach(async () => {
  resetMockState();
  await enableAurigaMock();
  setMockSession('admin');
});

afterEach(() => {
  disableAurigaMock();
});

describe('AdminLayout', () => {
  it('renders sidebar navigation', async () => {
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        {
          path: '/',
          component: { template: '<div/>' },
          children: [
            { path: '', component: { template: '<div>page</div>' } },
            { path: 'login', component: { template: '<div/>' } },
            ...['members', 'instances', 'providers'].map((path) => ({
              path,
              component: { template: '<div/>' },
            })),
          ],
        },
        { path: '/login', component: { template: '<div/>' } },
      ],
    });
    await router.push('/');
    const wrapper = mount(AdminLayout, {
      global: {
        plugins: [
          createPinia(),
          router,
          createAurigaI18n('zh-CN'),
          ElementPlus,
        ],
      },
    });
    await flushPromises();
    expect(wrapper.text()).toContain('成员');
    expect(wrapper.text()).toContain('运行环境');
    expect(wrapper.text()).toContain('成员与权限');
    expect(wrapper.findAll('.admin-navlink')).toHaveLength(8);
    await router.push('/members');
    await flushPromises();
    expect(wrapper.text()).toContain('邀请');
    expect(wrapper.text()).toContain('配额');
    await router.push('/instances');
    await flushPromises();
    expect(wrapper.text()).toContain('节点');
    expect(wrapper.text()).toContain('实例');
    await router.push('/providers');
    await flushPromises();
    expect(wrapper.text()).toContain('身份源');
    wrapper.unmount();
  });
});
