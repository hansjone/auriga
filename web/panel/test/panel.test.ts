import { mount } from '@vue/test-utils';
import { nextTick } from 'vue';
import { expect, it } from 'vitest';
import TeamPanel from '../src/TeamPanel.ce.vue';
import { registerPanel } from '../src/index';
it('renders localized content', () => {
  const wrapper = mount(TeamPanel, {
    props: { context: { locale: 'en', theme: 'light' } },
  });
  expect(wrapper.text()).toContain('Team panel');
  expect(wrapper.attributes('data-theme')).toBe('light');
});
it('mounts a custom element with shadow styles and reacts to host context', async () => {
  registerPanel();
  const panel = document.createElement('auriga-team-panel') as HTMLElement & {
    context: { locale: string; theme: 'light' };
  };
  panel.context = { locale: 'en', theme: 'light' };
  document.body.append(panel);
  await nextTick();
  expect(panel.shadowRoot?.textContent).toContain('Team panel');
  expect(panel.shadowRoot?.querySelector('style')?.textContent).toContain(
    '--auriga-bg',
  );
  panel.context = { locale: 'zh-CN', theme: 'light' };
  await nextTick();
  expect(panel.shadowRoot?.textContent).toContain('团队面板');
  panel.remove();
});
