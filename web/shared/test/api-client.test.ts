import { afterEach, describe, expect, it, vi } from 'vitest';
import { createApiClient } from '../src/api/client';
import { ApiError, mapProblemMessage } from '../src/api/problem';
import { createAurigaI18n } from '../src/i18n';

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('createApiClient', () => {
  it('attaches X-Auriga-CSRF on write requests', async () => {
    let captured: RequestInit | undefined;
    const fetchMock = (async (
      _input: RequestInfo | URL,
      init?: RequestInit,
    ) => {
      captured = init;
      return new Response(null, { status: 204 });
    }) as typeof fetch;
    const client = createApiClient({
      fetch: fetchMock,
      getCookie: () => 'auriga_csrf=token-123',
    });
    await client.post('/api/iam/v1/logout');
    expect(new Headers(captured?.headers).get('X-Auriga-CSRF')).toBe(
      'token-123',
    );
  });

  it('does not attach CSRF on GET', async () => {
    let captured: RequestInit | undefined;
    const fetchMock = (async (
      _input: RequestInfo | URL,
      init?: RequestInit,
    ) => {
      captured = init;
      return new Response(JSON.stringify({ ok: true }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      });
    }) as typeof fetch;
    const client = createApiClient({
      fetch: fetchMock,
      getCookie: () => 'auriga_csrf=token-123',
    });
    await client.get('/api/iam/v1/me');
    expect(new Headers(captured?.headers).get('X-Auriga-CSRF')).toBeNull();
  });

  it('maps problem+json and calls onUnauthenticated on 401', async () => {
    const onUnauthenticated = vi.fn();
    const fetchMock = (async () =>
      new Response(
        JSON.stringify({
          title: 'Unauthenticated',
          status: 401,
          code: 'common.unauthenticated',
        }),
        {
          status: 401,
          headers: { 'Content-Type': 'application/problem+json' },
        },
      )) as typeof fetch;
    const client = createApiClient({
      fetch: fetchMock,
      onUnauthenticated,
    });
    await expect(client.get('/api/iam/v1/me')).rejects.toBeInstanceOf(ApiError);
    expect(onUnauthenticated).toHaveBeenCalledOnce();
  });
});

describe('mapProblemMessage', () => {
  it('resolves code to zh/en copy', () => {
    const i18n = createAurigaI18n('zh-CN');
    const t = i18n.global.t;
    expect(
      mapProblemMessage(
        { title: 'x', status: 401, code: 'common.unauthenticated' },
        t,
      ),
    ).toBe('请先登录');
    i18n.global.locale.value = 'en';
    expect(
      mapProblemMessage(
        { title: 'x', status: 400, code: 'iam.invalid_credentials' },
        t,
      ),
    ).toBe('Invalid username or password');
  });
});
