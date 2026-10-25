import { describe, expect, it, vi } from 'vitest';
import { createModelProvidersApi } from './modelProvidersApi';

describe('modelProvidersApi', () => {
  it('uses encoded paths, pagination, JSON, idempotency and actual ETags', async () => {
    const fetch = vi.fn(
      async () =>
        new Response(JSON.stringify({ id: 'p', revision: 3 }), {
          headers: {
            'Content-Type': 'application/json',
            ETag: '"opaque-version"',
          },
        }),
    );
    const api = createModelProvidersApi({ fetch, getCookie: () => '' });
    const result = await api.createCredential(
      'p/a',
      { secret: 'fixture' },
      'request-key',
    );
    expect(result.etag).toBe('"opaque-version"');
    expect(result.data.id).toBe('p');
    expect(fetch.mock.calls[0]).toBeDefined();
    const [path, init] = (
      fetch.mock.calls as unknown as [string, RequestInit][]
    )[0]!;
    expect(path).toBe('/api/ai/v1/admin/providers/p%2Fa/credentials');
    expect(new Headers(init.headers).get('Idempotency-Key')).toBe(
      'request-key',
    );
    expect(JSON.parse(init.body as string)).toEqual({ secret: 'fixture' });
    await api.credentialAction(
      'p/a',
      'c/b',
      'revoke',
      { reason: 'rotate' },
      result.etag!,
      'action-key',
    );
    const action = (fetch.mock.calls as unknown as [string, RequestInit][])[1]!;
    expect(action[0]).toContain('/c%2Fb/revoke');
    expect(new Headers(action[1].headers).get('If-Match')).toBe(
      '"opaque-version"',
    );
    await api.models('cursor/value');
    expect(
      (fetch.mock.calls as unknown as [string, RequestInit][])[2]![0],
    ).toContain('cursor=cursor%2Fvalue');
  });
  it('parses Problem responses and rejects missing ETags before writing', async () => {
    const fetch = vi.fn(
      async () =>
        new Response(
          JSON.stringify({
            status: 412,
            code: 'common.precondition_failed',
            title: 'Conflict',
          }),
          {
            status: 412,
            headers: { 'Content-Type': 'application/problem+json' },
          },
        ),
    );
    const api = createModelProvidersApi({ fetch });
    await expect(
      api.patchModel('m', { enabled: false }, '"old"'),
    ).rejects.toMatchObject({
      problem: { code: 'common.precondition_failed' },
    });
    await expect(api.patchModel('m', { enabled: false }, '')).rejects.toThrow();
    expect(fetch).toHaveBeenCalledTimes(1);
  });
});

it('wraps absolute administrative URLs and preserves concurrent response ETags', async () => {
  const fetch = vi.fn(async (input: RequestInfo | URL) => {
    const path = new URL(String(input), 'http://localhost').pathname;
    const name = path.split('/').at(-1)!;
    return new Response(JSON.stringify({ id: name }), {
      headers: { 'Content-Type': 'application/json', ETag: `"${name}"` },
    });
  });
  const api = createModelProvidersApi({
    fetch,
    getAdminAccessMode: () => 'domain',
  });
  const [a, b] = await Promise.all([
    api.provider('first'),
    api.provider('second'),
  ]);
  expect(a).toEqual({ data: { id: 'first' }, etag: '"first"' });
  expect(b).toEqual({ data: { id: 'second' }, etag: '"second"' });
  expect(new URL(String(fetch.mock.calls[0]![0])).origin).toBe(
    globalThis.location.origin,
  );
});

it('uses contract JSON PATCH and PUT bodies, and gives each create an idempotency key', async () => {
  const fetch = vi.fn<typeof globalThis.fetch>(
    async () =>
      new Response('{}', { headers: { 'Content-Type': 'application/json' } }),
  );
  const api = createModelProvidersApi({ fetch });
  await api.patchProvider('p', { enabled: false }, '"p-etag"');
  await api.patchModel('m/a', { displayName: 'New name' }, '"m-etag"');
  await api.grants('m/a', { items: [] }, '"grants-etag"');
  await api.createCredential('p', { secret: 'fixture' });
  await api.createCredential('p', { secret: 'fixture' });
  const calls = fetch.mock.calls;
  expect(calls.map((c) => c[1]!.method)).toEqual([
    'PATCH',
    'PATCH',
    'PUT',
    'POST',
    'POST',
  ]);
  expect(new Headers(calls[0]![1]!.headers).get('Content-Type')).toBe(
    'application/json',
  );
  expect(new Headers(calls[1]![1]!.headers).get('If-Match')).toBe('"m-etag"');
  expect(calls[2]![0]).toBe('/api/ai/v1/admin/models/m%2Fa/grants');
  expect(JSON.parse(calls[2]![1]!.body as string)).toEqual({ items: [] });
  const firstKey = new Headers(calls[3]![1]!.headers).get('Idempotency-Key');
  expect(firstKey).toBeTruthy();
  expect(new Headers(calls[4]![1]!.headers).get('Idempotency-Key')).not.toBe(
    firstKey,
  );
});

it('reads resource versions from the frozen model, grants and credential GET paths', async () => {
  const fetch = vi.fn<typeof globalThis.fetch>(
    async () =>
      new Response('{"items":[]}', {
        headers: { 'Content-Type': 'application/json', ETag: '"read-version"' },
      }),
  );
  const api = createModelProvidersApi({ fetch });
  expect((await api.model('m/a')).etag).toBe('"read-version"');
  expect((await api.readGrants('m/a')).etag).toBe('"read-version"');
  expect((await api.credential('p/a', 'c/b')).etag).toBe('"read-version"');
  expect(fetch.mock.calls.map((c) => c[0])).toEqual([
    '/api/ai/v1/admin/models/m%2Fa',
    '/api/ai/v1/admin/models/m%2Fa/grants',
    '/api/ai/v1/admin/providers/p%2Fa/credentials/c%2Fb',
  ]);
});

it('reuses a supplied submission key across attempts and never retries writes automatically', async () => {
  const fetch = vi
    .fn<typeof globalThis.fetch>()
    .mockResolvedValueOnce(
      new Response(
        '{"status":503,"code":"common.unavailable","title":"Unavailable"}',
        {
          status: 503,
          headers: { 'Content-Type': 'application/problem+json' },
        },
      ),
    )
    .mockResolvedValue(
      new Response('{}', { headers: { 'Content-Type': 'application/json' } }),
    );
  const api = createModelProvidersApi({ fetch });
  await expect(
    api.createCredential('p', { secret: 'fixture' }, 'same-submission'),
  ).rejects.toMatchObject({ status: 503 });
  expect(fetch).toHaveBeenCalledTimes(1);
  await api.createCredential('p', { secret: 'fixture' }, 'same-submission');
  expect(
    fetch.mock.calls.map((c) =>
      new Headers(c[1]?.headers).get('Idempotency-Key'),
    ),
  ).toEqual(['same-submission', 'same-submission']);
});
