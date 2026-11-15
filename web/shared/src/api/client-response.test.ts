import { expect, it, vi } from 'vitest';
import { createApiClient } from './client';

it('exposes successful binary bodies and headers without forwarding responseType', async () => {
  const response = new Response('example-zip', {
    headers: { 'Content-Type': 'application/zip', ETag: '"3"' },
  });
  const fetch = vi.fn<typeof globalThis.fetch>(async () => response);
  const client = createApiClient({
    fetch,
    getAdminAccessMode: () => 'domain',
    getCookie: () => 'auriga_csrf=example',
  });
  const result = await client.post<Response>(
    '/api/iam/v1/admin/uac/diagnostic-bundles',
    {},
    { responseType: 'response' },
  );
  expect(result).toBe(response);
  expect(await result.text()).toBe('example-zip');
  expect(result.headers.get('ETag')).toBe('"3"');
  expect(fetch.mock.calls[0][1]).not.toHaveProperty('responseType');
});

it('retains normal API error and session expiry handling for raw responses', async () => {
  const expired = vi.fn();
  const client = createApiClient({
    onUnauthenticated: expired,
    fetch: async () =>
      new Response(
        JSON.stringify({ status: 401, code: 'common.unauthenticated' }),
        {
          status: 401,
          headers: { 'Content-Type': 'application/problem+json' },
        },
      ),
  });
  await expect(
    client.get('/api/iam/v1/admin/providers', { responseType: 'response' }),
  ).rejects.toMatchObject({ status: 401 });
  expect(expired).toHaveBeenCalledOnce();
});
