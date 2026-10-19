import { mount } from '@vue/test-utils';
import { describe, expect, it } from 'vitest';
import { createAurigaI18n } from '../src/i18n';
import Placeholder from '../src/Placeholder.vue';

describe('Placeholder', () => {
  for (const locale of ['zh-CN', 'en'] as const)
    it('renders ' + locale, () => {
      const wrapper = mount(Placeholder, {
        props: { title: 'Auriga' },
        global: { plugins: [createAurigaI18n(locale)] },
      });
      expect(wrapper.text()).toContain(
        locale === 'en' ? 'Coming soon' : '即将推出',
      );
    });
});
