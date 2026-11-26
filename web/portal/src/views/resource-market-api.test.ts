import { expect, it, vi } from 'vitest';
import { createApiClient } from '@auriga/shared';
import {
  createResourceMarketApi,
  type ResourcePackage,
} from './resource-market-api';

it('sends only the exact selection under the portal cookie and CSRF with its CAS revision', async () => {
  const fetch = vi.fn(
    async () =>
      new Response('{}', { headers: { 'Content-Type': 'application/json' } }),
  );
  const client = createApiClient({
    fetch,
    getCookie: () => 'auriga_csrf=csrf-test',
  });
  const api = createResourceMarketApi(client);
  const selection = {
    action: 'disable' as const,
    identity: {
      tenantId: 't',
      packageId: 'p',
      versionId: 'v',
      version: '1.0.0',
      kind: 'skill' as const,
      manifestDigest: 'sha256:' + 'a'.repeat(64),
    },
  };
  await api.select('placement', selection, 4, new AbortController().signal);
  const [path, init] = fetch.mock.calls[0] as unknown as [string, RequestInit];
  expect(path).toContain('/catalog/placements/placement/installations');
  expect(init.credentials).toBe('same-origin');
  expect(new Headers(init.headers).get('X-Auriga-CSRF')).toBe('csrf-test');
  expect(new Headers(init.headers).get('If-Match')).toBe('"4"');
  expect(new Headers(init.headers).get('Idempotency-Key')).toMatch(
    /^[a-f0-9]{32}$/,
  );
  expect(JSON.parse(init.body as string)).toEqual({ selection });
});

it('rejects mixed view revisions across installation pages', async () => {
  const client = createApiClient({
    fetch: vi
      .fn()
      .mockResolvedValueOnce(
        Response.json({
          placementId: 'p',
          mode: 'dedicated',
          epoch: 1,
          revision: 1,
          viewId: 'one',
          nodeObserved: true,
          items: [],
          nextCursor: 'cursor',
        }),
      )
      .mockResolvedValueOnce(
        Response.json({
          placementId: 'p',
          mode: 'dedicated',
          epoch: 1,
          revision: 1,
          viewId: 'two',
          nodeObserved: true,
          items: [],
        }),
      ),
  });
  await expect(
    createResourceMarketApi(client).installations(
      'p',
      new AbortController().signal,
    ),
  ).rejects.toThrow('changed');
});

it('chooses only the highest published release across all version pages', async () => {
  const client = createApiClient({
    fetch: vi
      .fn()
      .mockResolvedValueOnce(
        Response.json({
          items: [
            { id: 'draft', state: 'draft', releaseSequence: 99 },
            { id: 'old', state: 'published', releaseSequence: 1 },
          ],
          nextCursor: 'next',
        }),
      )
      .mockResolvedValueOnce(
        Response.json({
          items: [{ id: 'new', state: 'published', releaseSequence: 3 }],
        }),
      ),
  });
  const result = await createResourceMarketApi(client).latest(
    { id: 'pack' } as ResourcePackage,
    new AbortController().signal,
  );
  expect(result?.id).toBe('new');
});

it('sets the desired plugin like using the portal session and CSRF token', async () => {
  const fetch = vi.fn<
    (path: RequestInfo | URL, init?: RequestInit) => Promise<Response>
  >(async () => Response.json({ starred: false, starCount: 4 }));
  const client = createApiClient({
    fetch,
    getCookie: () => 'auriga_csrf=csrf-test',
  });
  const api = createResourceMarketApi(client);
  const result = await api.star(
    'plugin-id',
    false,
    new AbortController().signal,
  );
  const [path, init] = fetch.mock.calls[0];
  expect(path).toBe('/api/resource/v1/catalog/packages/plugin-id/star');
  expect(init?.method).toBe('POST');
  expect(init?.credentials).toBe('same-origin');
  expect(new Headers(init?.headers).get('X-Auriga-CSRF')).toBe('csrf-test');
  expect(new Headers(init?.headers).get('Idempotency-Key')).toBeTruthy();
  expect(JSON.parse(init?.body as string)).toEqual({ starred: false });
  expect(result).toEqual({ starred: false, starCount: 4 });
});
