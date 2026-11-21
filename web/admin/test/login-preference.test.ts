import { mount, flushPromises } from '@vue/test-utils';
import { afterEach, expect, it, vi } from 'vitest';
import { createMemoryHistory, createRouter } from 'vue-router';
import { createAurigaI18n } from '@auriga/shared';
import LoginView from '../src/views/LoginView.vue';
const api = vi.hoisted(() => ({
  listLoginProviders: vi.fn(),
  createLoginTransaction: vi.fn(),
}));
vi.mock('../src/api', () => ({ iamApi: api }));
afterEach(() => {
  document
    .querySelectorAll('meta[name="auriga-login-preference"]')
    .forEach((node) => node.remove());
  vi.clearAllMocks();
});
it.each([false, true])('管理登录团队优先=%s，仍保留其他方式', async (team) => {
  if (team)
    document.head.insertAdjacentHTML(
      'beforeend',
      '<meta name="auriga-login-preference" content="team">',
    );
  const items = ['uac', 'team-account'].map((connectorType) => ({
    id: connectorType,
    connectorType,
    order: 0,
    displayName: { en: connectorType, 'zh-CN': connectorType },
    primaryStep: 'form',
  }));
  api.listLoginProviders.mockResolvedValue({ items });
  api.createLoginTransaction.mockResolvedValue({
    id: 'fixture',
    purpose: 'login',
    status: 'pending',
    provider: items[1],
    step: {
      type: 'form',
      formId: 'password',
      fields: [{ name: 'username', kind: 'text', label: { en: 'Account' } }],
    },
  });
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/login', component: LoginView }],
  });
  await router.push('/login');
  const view = mount(LoginView, {
    global: { plugins: [router, createAurigaI18n('zh-CN')] },
  });
  await flushPromises();
  expect(api.createLoginTransaction).toHaveBeenCalledTimes(team ? 1 : 0);
  if (team) {
    expect(view.find('input[name=username]').exists()).toBe(true);
    expect(view.find('.auriga-login-back').exists()).toBe(false);
    expect(view.get('.auriga-login-topbar .back').text()).toBe('返回');
    expect(
      view
        .findAll('[data-provider-id]')
        .map((node) => node.attributes('data-provider-id')),
    ).toEqual(['team-account', 'uac']);
  } else expect(view.find('[data-provider-id=uac]').exists()).toBe(true);
  view.unmount();
});
