import { afterEach, expect, it } from 'vitest';
import {
  loginPreference,
  orderLoginProviders,
  preferredLoginProvider,
} from '../src/login-preference';
import type { LoginProvider } from '../src/api/iam';
import { mount } from '@vue/test-utils';
import ProviderSelect from '../src/steps/ProviderSelect.vue';
import { createAurigaI18n } from '../src/i18n';
const items = ['uac', 'team-account', 'emergency'].map((connectorType) => ({
  id: connectorType,
  connectorType,
  order: 0,
})) as LoginProvider[];
afterEach(() => document.head.replaceChildren());
it('缺省或模糊元信息安全保持公司优先', () => {
  expect(preferredLoginProvider(items, document)?.id).toBe('uac');
  document.head.innerHTML =
    '<meta name="auriga-login-preference" content="invalid">';
  expect(loginPreference(document)).toBe('company');
  document.head.innerHTML =
    '<meta name="auriga-login-preference" content="team"><meta name="auriga-login-preference" content="team">';
  expect(loginPreference(document)).toBe('company');
});
it('team偏好使用真实团队身份源且保留UAC和应急入口', () => {
  document.head.innerHTML =
    '<meta name="auriga-login-preference" content="team">';
  expect(preferredLoginProvider(items, document)?.id).toBe('team-account');
  expect(orderLoginProviders(items, document).map((p) => p.id)).toEqual([
    'team-account',
    'uac',
    'emergency',
  ]);
  expect(preferredLoginProvider([items[0]], document)?.id).toBe('uac');
  const view = mount(ProviderSelect, {
    props: { providers: items },
    global: { plugins: [createAurigaI18n('zh-CN')] },
  });
  expect(
    view
      .findAll('[data-provider-id]')
      .map((node) => node.attributes('data-provider-id')),
  ).toEqual(['team-account', 'uac', 'emergency']);
  view.unmount();
});
