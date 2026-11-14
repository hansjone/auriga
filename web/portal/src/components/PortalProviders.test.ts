import { mount } from '@vue/test-utils';
import { expect, it } from 'vitest';
import { createAurigaI18n, type LoginProvider } from '@auriga/shared';
import PortalProviders from './PortalProviders.vue';

const provider = (connectorType: string): LoginProvider => ({
  id: connectorType,
  connectorType,
  displayName: { 'zh-CN': connectorType, en: connectorType },
  order: 0,
  primaryStep: 'form',
});

it('UAC 优先，仅将应急账号折叠；团队密码、OIDC 和 remote 保持主要入口', async () => {
  const providers = ['team-account', 'emergency', 'oidc', 'uac', 'remote'].map(
    provider,
  );
  const view = mount(PortalProviders, {
    props: { providers },
    global: { plugins: [createAurigaI18n('zh-CN')] },
  });
  expect(
    view
      .get('.provider-select')
      .findAll('[data-provider-id]')
      .map((node) => node.attributes('data-provider-id')),
  ).toEqual(['uac', 'team-account', 'oidc', 'remote']);
  const secondary = view.get('details');
  expect(secondary.attributes('open')).toBeUndefined();
  expect(
    secondary
      .findAll('[data-provider-id]')
      .map((node) => node.attributes('data-provider-id')),
  ).toEqual(['emergency']);
  await view.get('[data-provider-id="team-account"]').trigger('click');
  expect(view.emitted('select')?.[0]?.[0]).toEqual(providers[0]);
  view.unmount();
});

it('只配置应急账号时仍能直接登录', () => {
  const view = mount(PortalProviders, {
    props: { providers: [provider('emergency')] },
    global: { plugins: [createAurigaI18n('en')] },
  });
  expect(view.find('details').exists()).toBe(false);
  expect(
    view.get('[data-provider-id="emergency"]').attributes('data-provider-id'),
  ).toBe('emergency');
  view.unmount();
});
