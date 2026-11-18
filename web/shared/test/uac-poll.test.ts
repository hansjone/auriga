import { describe, expect, it, vi } from 'vitest';
import { createApiClient } from '../src/api/client';
import { createIamApi } from '../src/api/iam';

describe('UAC polling browser request proof', () => {
  it('uses the non-simple marker only for status reads and keeps same-origin credentials and no redirects', async () => {
    const fetcher = vi.fn<typeof fetch>(
      async () =>
        new Response('{}', {
          headers: { 'Content-Type': 'application/json' },
        }),
    );
    const api = createIamApi(createApiClient({ fetch: fetcher }));
    await api.getUacChallenge('challenge/id');
    await api.getDeviceRequest('device');
    await api.cancelUacChallenge('challenge/id');
    const [path, init] = fetcher.mock.calls[0]!;
    expect(path).toBe('/api/iam/v1/login/uac/challenges/challenge%2Fid');
    expect(init).toMatchObject({
      method: 'GET',
      mode: 'same-origin',
      credentials: 'same-origin',
      cache: 'no-store',
      redirect: 'error',
    });
    expect(new Headers(init?.headers).get('Auriga-UAC-Poll')).toBe(
      'same-origin',
    );
    expect(new Headers(init?.headers).has('Sec-Fetch-Site')).toBe(false);
    expect(new Headers(init?.headers).has('Origin')).toBe(false);
    for (const [, other] of fetcher.mock.calls.slice(1))
      expect(new Headers(other?.headers).has('Auriga-UAC-Poll')).toBe(false);
  });
});
