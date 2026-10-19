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
import HomeView from '../src/views/HomeView.vue';

beforeEach(async () => {
  resetMockState();
  await enableAurigaMock();
  setMockSession('portal');
});

afterEach(() => {
  disableAurigaMock();
});

describe('HomeView', () => {
  it('shows shell and switches language', async () => {
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: '/', component: HomeView },
        { path: '/login', component: { template: '<div/>' } },
      ],
    });
    await router.push('/');
    const wrapper = mount(HomeView, {
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
    expect(wrapper.text()).toContain('首页');
    const buttons = wrapper.findAll('button');
    await buttons[0]!.trigger('click');
    expect(wrapper.text()).toContain('Home');
    wrapper.unmount();
  });
});
