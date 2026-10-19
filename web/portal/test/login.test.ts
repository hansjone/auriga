import { mount, flushPromises } from '@vue/test-utils';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { createPinia } from 'pinia';
import { createMemoryHistory, createRouter } from 'vue-router';
import {
  createAurigaI18n,
  disableAurigaMock,
  enableAurigaMock,
  resetMockState,
  MOCK_PASSWORD,
  MOCK_TOTP,
} from '@auriga/shared';
import LoginView from '../src/views/LoginView.vue';

beforeEach(async () => {
  resetMockState();
  await enableAurigaMock();
});

afterEach(() => {
  disableAurigaMock();
});

describe('LoginView', () => {
  it('runs provider → password → totp → complete', async () => {
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: '/login', component: LoginView },
        { path: '/', component: { template: '<div>home</div>' } },
      ],
    });
    await router.push('/login');
    const wrapper = mount(LoginView, {
      global: {
        plugins: [createPinia(), router, createAurigaI18n('zh-CN')],
      },
    });
    await flushPromises();
    expect(wrapper.find('[data-testid="provider-select"]').exists()).toBe(true);
    await wrapper.find('[data-provider-id]').trigger('click');
    await flushPromises();
    expect(wrapper.find('[data-testid="form-step"]').exists()).toBe(true);
    await wrapper.find('input[name="username"]').setValue('alice');
    await wrapper.find('input[name="password"]').setValue(MOCK_PASSWORD);
    await wrapper.find('form').trigger('submit');
    await flushPromises();
    await wrapper.find('input[name="code"]').setValue(MOCK_TOTP);
    await wrapper.find('form').trigger('submit');
    await flushPromises();
    expect(router.currentRoute.value.path).toBe('/');
    wrapper.unmount();
  });
});
