import { afterEach, expect, it, vi } from 'vitest';
import { registerPanel } from '../src/index';
import { mountPanel, type NativeWrapper } from './native-host';
const wrappers: NativeWrapper[] = [];
afterEach(() => wrappers.splice(0).forEach((wrapper) => wrapper.unmount()));
it('rejects a foreign constructor instead of silently treating it as this module', () => {
  const occupied = vi
    .spyOn(customElements, 'get')
    .mockReturnValue(class Foreign extends HTMLElement {});
  try {
    expect(() => registerPanel()).toThrow('different module');
  } finally {
    occupied.mockRestore();
  }
});
it('renders localized native content', () => {
  const wrapper = mountPanel({ locale: 'en', theme: 'light' });
  wrappers.push(wrapper);
  expect(wrapper.text()).toContain('Team panel');
  expect(wrapper.attributes('data-theme')).toBe('light');
});
it('mounts a native custom element with DSH shadow styles and reacts to host context', async () => {
  const wrapper = mountPanel({ locale: 'en', theme: 'light' });
  wrappers.push(wrapper);
  expect(
    wrapper.element.shadowRoot?.querySelector('style')?.textContent,
  ).toContain('--dsw-alias-bg-layer-1');
  await wrapper.setProps({ context: { locale: 'zh-CN', theme: 'dark' } });
  expect(wrapper.text()).toContain('团队面板');
  expect(wrapper.attributes('data-theme')).toBe('dark');
  expect(wrapper.findAll('[role="tab"]')).toHaveLength(2);
  expect(wrapper.find('[role="dialog"], form, iframe')).toEqual(
    expect.objectContaining({ element: undefined }),
  );
});
it('host presentation changes preserve resource filters and selected tab', async () => {
  const wrapper = mountPanel({
    mode: 'connect',
    protocol: 1,
    locale: 'en',
    fetch: async () =>
      new Response(JSON.stringify({ items: [] }), {
        headers: { 'content-type': 'application/json' },
      }),
  });
  wrappers.push(wrapper);
  await new Promise((resolve) => setTimeout(resolve, 0));
  await wrapper.get('input').setValue('keep');
  const before = wrapper.element.context!;
  await wrapper.setProps({
    context: { ...before, locale: 'zh-CN', theme: 'dark' },
  });
  expect(wrapper.get('input').element.value).toBe('keep');
  await wrapper.findAll('[role="tab"]')[1].trigger('click');
  await wrapper.setProps({ context: { ...before, locale: 'en' } });
  expect(wrapper.text()).toContain('installation inventory is unavailable');
});
it('native tabs support keyboard navigation', () => {
  const wrapper = mountPanel({ locale: 'en' });
  wrappers.push(wrapper);
  wrapper
    .findAll('[role="tab"]')[0]
    .element!.dispatchEvent(
      new KeyboardEvent('keydown', { key: 'ArrowRight', bubbles: true }),
    );
  expect(
    wrapper.findAll('[role="tab"]')[1].element?.getAttribute('aria-selected'),
  ).toBe('true');
});

it('has no product palette, font stack or standalone theme controls', () => {
  const wrapper = mountPanel();
  wrappers.push(wrapper);
  const css = wrapper.element.shadowRoot!.querySelector('style')!.textContent!;
  expect(css).not.toMatch(
    /#[0-9a-f]{3,8}\b|color-mix|prefers-color-scheme|data-theme\s*=|font-family\s*:/i,
  );
  expect(css).toContain('font: inherit');
  expect(css).toContain('--dsw-alias-state-error-primary');
  expect(css).toContain('--dsw-focus-ring-color');
  expect(wrapper.attributes('data-theme')).toBe(null);
  expect(wrapper.findAll('select,[data-theme-toggle]')).toHaveLength(0);
});
