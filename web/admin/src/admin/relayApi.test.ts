import { describe, expect, it, vi } from 'vitest';
import { createApiClient } from '@auriga/shared';
import { createRelayApi } from './relayApi';

describe('relay management transport', () => {
  it('uses atomic create-only semantics for import while retaining edit upsert', async () => {
    const fetch = vi.fn<typeof globalThis.fetch>(
      async () =>
        new Response('{}', { headers: { 'Content-Type': 'application/json' } }),
    );
    const api = createRelayApi(
      createApiClient({ fetch, getCookie: () => 'auriga_csrf=csrf' }),
    );
    const pool = {
      id: 'public',
      enabled: true,
      contextWindow: 32000,
      maxOutputTokens: 4096,
      routes: [{ providerId: 'provider', model: 'upstream', weight: 1 }],
      price: {
        currency: 'USD' as const,
        inputMicrosPerMillionTokens: 1,
        outputMicrosPerMillionTokens: 1,
        version: 'v',
        effectiveAt: '2026-01-01T00:00:00Z',
      },
    };
    await api.createPool(pool, 'stable-import');
    await api.savePool(pool, 'edit');
    expect(
      new Headers(fetch.mock.calls[0][1]?.headers).get('If-None-Match'),
    ).toBe('*');
    expect(
      new Headers(fetch.mock.calls[0][1]?.headers).get('Idempotency-Key'),
    ).toBe('stable-import');
    expect(
      new Headers(fetch.mock.calls[0][1]?.headers).get('X-Auriga-CSRF'),
    ).toBe('csrf');
    expect(
      new Headers(fetch.mock.calls[1][1]?.headers).has('If-None-Match'),
    ).toBe(false);
  });
  it('uses same-origin management paths, CSRF and stable POST idempotency', async () => {
    const calls: { path: string; init: RequestInit }[] = [];
    const fetch = vi.fn(
      async (input: RequestInfo | URL, init?: RequestInit) => {
        calls.push({ path: String(input), init: init ?? {} });
        return new Response('{"items":[]}', {
          headers: { 'Content-Type': 'application/json' },
        });
      },
    );
    const api = createRelayApi(
      createApiClient({ fetch, getCookie: () => 'auriga_csrf=csrf' }),
    );
    await api.providers();
    await api.saveProvider(
      '',
      {
        name: '测试',
        kind: 'minimax',
        baseUrl: 'https://api.example.com',
        enabled: true,
      },
      'same-key',
    );
    expect(calls.map((c) => c.path)).toEqual([
      '/api/ai/v1/admin/relay/providers?limit=200',
      '/api/ai/v1/admin/relay/providers',
    ]);
    const headers = new Headers(calls[1]!.init.headers);
    expect(headers.get('Idempotency-Key')).toBe('same-key');
    expect(headers.get('X-Auriga-CSRF')).toBe('csrf');
    expect(headers.has('Authorization')).toBe(false);
    expect(headers.has('X-Auriga-Tenant')).toBe(false);
    expect(calls[1]!.init.credentials).toBe('same-origin');
  });
  it('encodes identifiers and excludes unused log selectors', async () => {
    const fetch = vi.fn<typeof globalThis.fetch>(
      async () =>
        new Response('{"items":[]}', {
          headers: { 'Content-Type': 'application/json' },
        }),
    );
    const api = createRelayApi(createApiClient({ fetch }));
    await api.providerModels('provider/id');
    expect(String(fetch.mock.calls[0]?.[0])).toBe(
      '/api/ai/v1/admin/relay/providers/provider%2Fid/models',
    );
    await api.usage('accountId', 'member/id', 'next token');
    expect(String(fetch.mock.calls[1]?.[0])).toBe(
      '/api/ai/v1/admin/relay/logs?accountId=member%2Fid&limit=50&cursor=next+token',
    );
  });
  it('reads every administration page and rejects a repeated cursor', async () => {
    const fetch = vi.fn<typeof globalThis.fetch>(
      async (url) =>
        new Response(
          JSON.stringify(
            String(url).includes('cursor=')
              ? { items: [{ id: 'last' }] }
              : { items: [{ id: 'first' }], nextCursor: 'next' },
          ),
          {
            headers: { 'Content-Type': 'application/json' },
          },
        ),
    );
    const api = createRelayApi(createApiClient({ fetch }));
    expect((await api.keys()).items.map((row) => row.id)).toEqual([
      'first',
      'last',
    ]);
    fetch.mockImplementation(
      async () =>
        new Response('{"items":[],"nextCursor":"same"}', {
          headers: { 'Content-Type': 'application/json' },
        }),
    );
    await expect(api.providers()).rejects.toThrow('Repeated relay cursor');
  });
});
