import { mount } from '@vue/test-utils';
import { expect, it } from 'vitest';
import { createAurigaI18n } from '@auriga/shared';
import Home from '../src/Home.vue';
it('renders and switches language and theme', async () => {
  const wrapper = mount(Home, { global: { plugins: [createAurigaI18n()] } });
  expect(wrapper.text()).toContain('???');
  await wrapper.findAll('button')[0]!.trigger('click');
  expect(wrapper.text()).toContain('Administration');
  await wrapper.findAll('button')[1]!.trigger('click');
  expect(document.documentElement.dataset.theme).toBe('light');
  wrapper.unmount();
});
