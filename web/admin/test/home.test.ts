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
          component: AdminLayout,
          children: [
            { path: '', component: { template: '<div>page</div>' } },
            { path: 'login', component: { template: '<div/>' } },
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
    expect(wrapper.text()).toContain('身份源');
    wrapper.unmount();
  });
});
