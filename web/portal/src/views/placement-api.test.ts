import { expect, it, vi, afterEach } from 'vitest';
import { exchangeTarget, exchangePlacement } from './placement-api';
const id = '11111111-1111-4111-8111-111111111111';
const origin = 'https://' + id + '.instances.example.test';
function configure() {
  const meta = document.createElement('meta');
  meta.name = 'auriga-instance-parent-domain';
  meta.content = 'instances.example.test';
  document.head.append(meta);
}
afterEach(() => {
  vi.unstubAllGlobals();
  document
    .querySelectorAll('meta[name="auriga-instance-parent-domain"]')
    .forEach((tag) => tag.remove());
});
it('handles a cross-origin manual opaqueredirect without following the instance root', async () => {
  configure();
  const assign = vi.fn();
  vi.stubGlobal('location', { origin: 'https://portal.example.test', assign });
  const fetch = vi.fn(function (
    this: typeof globalThis,
    ...args: Parameters<typeof globalThis.fetch>
  ) {
    expect(this).toBe(globalThis);
    expect(args[1]?.method).toBe('POST');
    return Promise.resolve({
      type: 'opaqueredirect',
      status: 0,
      ok: false,
      url: '',
      headers: new Headers(),
    });
  });
  vi.stubGlobal('fetch', fetch);
  const ticket = crypto.randomUUID();
  await exchangePlacement(
    {
      placement: {
        memberId: 'm',
        placementId: 'p',
        mode: 'shared',
        state: 'ready',
        epoch: 1,
        revision: 1,
      },
      ticket,
      expiresAt: Date.now() / 1000 + 60,
      exchangeUrl: origin + '/_auriga/placement-ticket',
    },
    new AbortController().signal,
  );
  expect(fetch).toHaveBeenCalledExactlyOnceWith(
    expect.any(URL),
    expect.objectContaining({
      method: 'POST',
      redirect: 'manual',
      credentials: 'include',
      body: JSON.stringify({ ticket }),
    }),
  );
  expect(String(fetch.mock.calls[0][0])).not.toContain(ticket);
  expect(assign).toHaveBeenCalledExactlyOnceWith(origin + '/');
});
it('only accepts exact runtime pool domains and canonical lowercase UUID paths', () => {
  configure();
  vi.stubGlobal('location', { origin: 'https://portal.example.test' });
  expect(
    exchangeTarget('/i/' + id + '/_auriga/placement-ticket').pathname,
  ).toBe('/i/' + id + '/_auriga/placement-ticket');
  expect(exchangeTarget(origin + '/_auriga/placement-ticket').origin).toBe(
    origin,
  );
  for (const path of [
    'http://' + id + '.instances.example.test/_auriga/placement-ticket',
    origin + ':8443/_auriga/placement-ticket',
    'https://attacker.instances.example.test/_auriga/placement-ticket',
    origin + '.evil.test/_auriga/placement-ticket',
    'https://' + id + '.nested.instances.example.test/_auriga/placement-ticket',
    '/i/abc/_auriga/placement-ticket',
    '/i/aaaaaaaa-AAAA-aaaa-aaaa-aaaaaaaaaaaa/_auriga/placement-ticket',
    '/_auriga/placement-ticket?ticket=x',
    '/_auriga/placement-ticket#x',
    '//evil.example/x',
    '/open/abc',
    '/_auriga/../other',
    'https://user:password@' +
      id +
      '.instances.example.test/_auriga/placement-ticket',
  ])
    expect(() => exchangeTarget(path)).toThrow();
});
it('accepts the configured portal port for pool domains and rejects other ports', () => {
  configure();
  vi.stubGlobal('location', { origin: 'https://portal.example.test:8443' });
  expect(exchangeTarget(origin + ':8443/_auriga/placement-ticket').origin).toBe(
    origin + ':8443',
  );
  expect(() => exchangeTarget(origin + '/_auriga/placement-ticket')).toThrow();
  expect(() =>
    exchangeTarget(origin + ':8444/_auriga/placement-ticket'),
  ).toThrow();
});
it('rejects failed exchange and expired/nonready tickets without navigation', async () => {
  const assign = vi.fn();
  vi.stubGlobal('location', { origin: 'http://localhost:3000', assign });
  const fetch = vi
    .fn()
    .mockResolvedValue({ type: 'cors', status: 403, ok: false });
  vi.stubGlobal('fetch', fetch);
  const value = {
    placement: {
      memberId: 'm',
      placementId: 'p',
      mode: 'shared' as const,
      state: 'ready' as const,
      epoch: 1,
      revision: 1,
    },
    ticket: crypto.randomUUID(),
    expiresAt: 1,
    exchangeUrl: '/i/' + id + '/_auriga/placement-ticket',
  };
  await expect(
    exchangePlacement(value, new AbortController().signal),
  ).rejects.toThrow();
  await expect(
    exchangePlacement(
      {
        ...value,
        expiresAt: Date.now() / 1000 + 60,
        placement: { ...value.placement, state: 'switching' },
      },
      new AbortController().signal,
    ),
  ).rejects.toThrow();
  expect(fetch).not.toHaveBeenCalled();
  await expect(
    exchangePlacement(
      { ...value, expiresAt: Date.now() / 1000 + 60 },
      new AbortController().signal,
    ),
  ).rejects.toThrow();
  expect(assign).not.toHaveBeenCalled();
});
