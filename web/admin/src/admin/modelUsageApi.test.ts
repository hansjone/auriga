import { afterEach, expect, it, vi } from 'vitest';
import { ApiError } from '@auriga/shared';
import { modelUsageApi } from './modelUsageApi';

afterEach(() => vi.unstubAllGlobals());
it('requests only contract pagination parameters and forwards cancellation', async () => {
  const fetch = vi
    .fn()
    .mockImplementation(async () => Response.json({ items: [] }));
  vi.stubGlobal('fetch', fetch);
  const signal = new AbortController().signal;
  expect(await modelUsageApi.usage('a+/=', signal)).toEqual({ items: [] });
  expect(await modelUsageApi.providers(undefined, signal)).toEqual({
    items: [],
  });
  expect(await modelUsageApi.models(undefined, signal)).toEqual({ items: [] });
  expect(
    fetch.mock.calls.map(([url]) => {
      const parsed = new URL(url);
      return parsed.pathname + parsed.search;
    }),
  ).toEqual([
    '/api/ai/v1/admin/usage?limit=100&cursor=a%2B%2F%3D',
    '/api/ai/v1/admin/providers?limit=100',
    '/api/ai/v1/admin/models?limit=100',
  ]);
  expect(fetch.mock.calls[0]?.[1]).toMatchObject({
    signal,
    cache: 'no-store',
    credentials: 'same-origin',
  });
});
it('preserves Problem status, code and retry-after', async () => {
  vi.stubGlobal(
    'fetch',
    vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          title: 'Unavailable',
          status: 503,
          code: 'ai.budget_unavailable',
          traceId: 'trace-example',
        }),
        {
          status: 503,
          headers: {
            'Content-Type': 'application/problem+json',
            'Retry-After': '30',
          },
        },
      ),
    ),
  );
  const error = await modelUsageApi.usage().catch((e: unknown) => e);
  expect(error).toBeInstanceOf(ApiError);
  expect(error).toMatchObject({
    status: 503,
    retryAfter: '30',
    problem: { code: 'ai.budget_unavailable', traceId: 'trace-example' },
  });
});
