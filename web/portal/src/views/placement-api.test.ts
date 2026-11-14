import { expect, it, vi, afterEach } from 'vitest';
import { exchangeTarget, exchangePlacement } from './placement-api';
afterEach(() => vi.unstubAllGlobals());
it('posts ticket as JSON and navigates only after the edge root redirect', async () => {
  const assign = vi.fn();
  vi.stubGlobal('location', { origin: 'http://localhost:3000', assign });
  const fetch = vi.fn().mockResolvedValue({
    ok: true,
    redirected: true,
    url: 'http://localhost:3000/i/abc/',
  });
  vi.stubGlobal('fetch', fetch);
  const ticket = crypto.randomUUID();
  await exchangePlacement(
    {
      placement: {
        memberId: 'm',
        placementId: 'p',
        mode: 'dedicated',
        state: 'ready',
        epoch: 1,
        revision: 1,
      },
      ticket,
      expiresAt: Math.floor(Date.now() / 1000) + 60,
      exchangeUrl: '/i/abc/_auriga/placement-ticket',
    },
    new AbortController().signal,
  );
  expect(fetch).toHaveBeenCalledWith(
    expect.any(URL),
    expect.objectContaining({
      method: 'POST',
      credentials: 'include',
      body: JSON.stringify({ ticket }),
    }),
  );
  expect(String(fetch.mock.calls[0][0])).not.toContain(ticket);
  expect(assign).toHaveBeenCalledWith('http://localhost:3000/i/abc/');
});
it('allows only configured ticket endpoints, without query/fragment or credentials', () => {
  expect(exchangeTarget('/i/abc/_auriga/placement-ticket').pathname).toBe(
    '/i/abc/_auriga/placement-ticket',
  );
  for (const path of [
    'https://evil.example/_auriga/placement-ticket',
    '/_auriga/placement-ticket?ticket=x',
    '/_auriga/placement-ticket#x',
    '//evil.example/x',
    '/open/abc',
    '/_auriga/../other',
  ])
    expect(() => exchangeTarget(path)).toThrow();
  expect(
    exchangeTarget('https://dsh.example/_auriga/placement-ticket', [
      'https://dsh.example',
    ]).origin,
  ).toBe('https://dsh.example');
});
it('does not send expired or nonready tickets', async () => {
  const fetch = vi.fn();
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
    exchangeUrl: '/_auriga/placement-ticket',
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
});
