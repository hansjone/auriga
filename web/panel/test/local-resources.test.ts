import { afterEach, expect, it, vi } from 'vitest';
import { mountPanel, flushPromises, type NativeWrapper } from './native-host';
import type { LocalDetail } from '../src/local-resources/api';
import { localApi } from '../src/local-resources/api';
import type { PanelContext } from '../src/resources';

const wrappers: NativeWrapper[] = [];
afterEach(() => wrappers.splice(0).forEach((w) => w.unmount()));
function fixture() {
  const item: LocalDetail = {
    plugin: {
      installation: 'present',
      id: 'a'.repeat(64),
      name: 'example-plugin',
      version: '1.2.3',
      enabled: true,
      phase: 'active',
      revision: 'b'.repeat(64),
      readonly: null,
      canToggle: true,
      canConfigure: true,
    },
    configRevision: 0,
    fields: [{ key: 'label', type: 'string', value: 'first' }],
  };
  const transport = vi.fn(async (path: string, init?: RequestInit) => {
    if (path === '')
      return new Response(
        JSON.stringify({ protocol: 1, items: [item.plugin] }),
      );
    if (path === '/detail') return new Response(JSON.stringify(item));
    const input = JSON.parse(String(init?.body));
    if (path === '/enabled') item.plugin.enabled = input.enabled;
    if (path === '/config') item.fields[0].value = input.changes[0].value;
    item.plugin.revision = 'c'.repeat(64);
    return new Response(
      JSON.stringify({ changed: true, application: 'restart-required', item }),
    );
  });
  const context: PanelContext = {
    mode: 'standalone',
    protocol: 1,
    locale: 'en',
    localResources: transport,
  };
  return { item, transport, context };
}
async function click(w: NativeWrapper, label: string) {
  const button = w.findAll('button').find((b) => b.text() === label);
  expect(button, label).toBeDefined();
  await button!.trigger('click');
  await flushPromises();
}
async function mine(context: PanelContext) {
  const w = mountPanel(context);
  wrappers.push(w);
  await click(w, 'My resources');
  return w;
}
it('shows local installed entries without platform fetch and offers confirmed enable/config readbacks', async () => {
  const f = fixture();
  const w = await mine(f.context);
  expect(w.text()).toContain('example-plugin');
  expect(w.text()).toContain('Installed');
  await click(w, 'View details');
  expect(w.get('input[aria-label="label"]').element.value).toBe('first');
  await click(w, 'Disable plugin');
  await click(w, 'Cancel');
  expect(
    f.transport.mock.calls.filter(([path]) => path === '/enabled'),
  ).toHaveLength(0);
  await click(w, 'Disable plugin');
  await click(w, 'Confirm operation');
  expect(w.text()).toContain('requires a restart');
  expect(w.text()).toContain('Disabled');
  await w.get('input[aria-label="label"]').setValue('changed');
  await click(w, 'Save configuration');
  await click(w, 'Confirm operation');
  expect(w.get('input[aria-label="label"]').element.value).toBe('changed');
  expect(
    f.transport.mock.calls.filter(([path]) => path === '/config'),
  ).toHaveLength(1);
});
it('managed detail stays readonly and unsupported/secret fields are explicitly outside this editor', async () => {
  const f = fixture();
  Object.assign(f.item.plugin, {
    readonly: 'managed',
    canToggle: false,
    canConfigure: false,
  });
  const w = await mine({ ...f.context, mode: 'managed' });
  await click(w, 'View details');
  expect(w.text()).toContain('read-only');
  expect(w.text()).toContain('Secret, complex');
  expect(w.find('input').element?.hasAttribute('disabled')).toBe(true);
  expect(
    w.findAll('button').some((b) => b.text() === 'Save configuration'),
  ).toBe(false);
});
it('invalid inventory is refused, auth is surfaced and writes are never automatically replayed', async () => {
  const context: PanelContext = {
    protocol: 1,
    localResources: vi.fn(
      async () => new Response('{"protocol":1,"items":[{}]}'),
    ),
  };
  await expect(
    localApi(context).list(new AbortController().signal),
  ).rejects.toMatchObject({ reason: 'invalid' });
  const f = fixture();
  const w = await mine(f.context);
  await click(w, 'View details');
  f.transport.mockImplementation(
    async () => new Response('{}', { status: 401 }),
  );
  const auth = vi.fn();
  w.element.addEventListener('auriga-auth-required', auth);
  await click(w, 'Disable plugin');
  await click(w, 'Confirm operation');
  expect(auth).toHaveBeenCalledTimes(1);
  expect(w.text()).toContain('Sign in through the host');
  expect(
    f.transport.mock.calls.filter(([path]) => path === '/enabled'),
  ).toHaveLength(1);
});
it('tab switch, context revocation and unmount isolate late local replies', async () => {
  const f = fixture();
  let resolve!: (v: Response) => void;
  f.transport.mockImplementation(
    () =>
      new Promise<Response>((done) => {
        resolve = done;
      }),
  );
  const w = await mine(f.context);
  await click(w, 'Resource market');
  resolve(
    new Response(JSON.stringify({ protocol: 1, items: [f.item.plugin] })),
  );
  await flushPromises();
  expect(w.text()).not.toContain('example-plugin');
  await w.setProps({ context: { protocol: 1, locale: 'en' } });
  await click(w, 'My resources');
  expect(w.text()).not.toContain('example-plugin');
  w.unmount();
});
