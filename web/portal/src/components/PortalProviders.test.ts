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
it('只显示部署方式，UAC 优先，应急图标最后且不折叠，当前项高亮', async () => {
  const providers = ['team-account', 'emergency', 'oidc', 'uac', 'remote'].map(
    provider,
  );
  const view = mount(PortalProviders, {
    props: { providers, selectedId: 'uac' },
    global: { plugins: [createAurigaI18n('zh-CN')] },
  });
  expect(
    view
      .findAll('[data-provider-id]')
      .map((n) => n.attributes('data-provider-id')),
  ).toEqual(['uac', 'team-account', 'oidc', 'remote', 'emergency']);
  expect(view.find('details').exists()).toBe(false);
  expect(view.get('[data-provider-id=uac]').attributes('aria-pressed')).toBe(
    'true',
  );
  expect(view.findAll('svg')).toHaveLength(5);
  await view.get('[data-provider-id=emergency]').trigger('click');
  expect(view.emitted('select')?.[0]?.[0]).toEqual(providers[1]);
  await view.setProps({ disabled: true });
  expect(
    view.findAll('button').every((n) => n.attributes('disabled') !== undefined),
  ).toBe(true);
  view.unmount();
});
it.each([{ providers: [] }, { providers: [provider('emergency')] }])(
  '只有零/一种方式时不显示图标行',
  ({ providers }) => {
    const view = mount(PortalProviders, {
      props: { providers },
      global: { plugins: [createAurigaI18n('en')] },
    });
    expect(view.find('[data-testid=login-methods]').exists()).toBe(false);
    view.unmount();
  },
);
