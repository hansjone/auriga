import { describe, expect, it, vi } from 'vitest';
import { createApiClient } from '@auriga/shared';
import { createWorkspaceApi } from '../src/workspace';

describe('portal workspace API', () => {
  it('uses generated endpoint paths and forwards cancellation without arbitrary response URLs', async () => {
    const fetcher = vi
      .fn<typeof fetch>()
      .mockResolvedValue(
        new Response('{}', { headers: { 'Content-Type': 'application/json' } }),
      );
    const api = createWorkspaceApi(createApiClient({ fetch: fetcher }));
    const abort = new globalThis.AbortController();
    await api.get('id/next?query', abort.signal);
    expect(fetcher.mock.calls[0]![0]).toBe(
      '/api/workspace/v1/instances/id%2Fnext%3Fquery',
    );
    expect(fetcher.mock.calls[0]![1]?.signal).toBe(abort.signal);
    expect(
      new Headers(fetcher.mock.calls[0]![1]?.headers).has('Authorization'),
    ).toBe(false);
  });

  it('preserves a backend CSRF rejection without bypass or replay', async () => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(
      new Response(
        JSON.stringify({
          title: 'Forbidden',
          status: 403,
          code: 'common.forbidden',
        }),
        {
          status: 403,
          headers: { 'Content-Type': 'application/problem+json' },
        },
      ),
    );
    const unauthenticated = vi.fn();
    const api = createWorkspaceApi(
      createApiClient({
        fetch: fetcher,
        getCookie: () => '',
        onUnauthenticated: unauthenticated,
      }),
    );
    await expect(
      api.open(new globalThis.AbortController().signal),
    ).rejects.toMatchObject({
      status: 403,
      problem: { code: 'common.forbidden' },
    });
    expect(fetcher).toHaveBeenCalledTimes(1);
    expect(
      new Headers(fetcher.mock.calls[0]![1]?.headers).has('X-Auriga-CSRF'),
    ).toBe(false);
    expect(unauthenticated).not.toHaveBeenCalled();
  });

  it('uses the shared 401 handler for an expired workspace session', async () => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(
      new Response(
        JSON.stringify({
          title: 'Expired',
          status: 401,
          code: 'common.unauthenticated',
        }),
        {
          status: 401,
          headers: { 'Content-Type': 'application/problem+json' },
        },
      ),
    );
    const unauthenticated = vi.fn();
    const api = createWorkspaceApi(
      createApiClient({ fetch: fetcher, onUnauthenticated: unauthenticated }),
    );
    await expect(
      api.list(new globalThis.AbortController().signal),
    ).rejects.toMatchObject({ status: 401 });
    expect(unauthenticated).toHaveBeenCalledOnce();
    expect(fetcher).toHaveBeenCalledTimes(1);
  });
});
