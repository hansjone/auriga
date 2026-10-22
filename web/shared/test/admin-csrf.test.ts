import { afterEach, describe, expect, it, vi } from 'vitest';
import { createApiClient } from '../src/api/client';
import { readAccessMode } from '../src/api/access-mode';
import { createIamApi } from '../src/api/iam';

afterEach(() => {
  vi.unstubAllGlobals();
  vi.useRealTimers();
  document.head.innerHTML = '';
});
const json = (body: unknown, status = 200, headers = {}) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json', ...headers },
  });
function harness() {
  const calls: { url: string; init: RequestInit }[] = [];
  const handler = vi.fn(async (url: string, init: RequestInit) => {
    if (url.endsWith('/csrf-tokens'))
      return json({
        ...JSON.parse(String(init.body)),
        expiresAt: new Date(Date.now() + 600_000).toISOString(),
      });
    return new Response(null, { status: 204 });
  });
  const fetcher = vi.fn(
    async (input: RequestInfo | URL, init?: RequestInit) => {
      calls.push({ url: String(input), init: init! });
      return handler(String(input), init!);
    },
  );
  const client = createApiClient({
    getAdminAccessMode: () => 'ip',
    fetch: fetcher as typeof fetch,
    getCookie: () => 'auriga_csrf=portal-secret',
  });
  return { client, calls, handler, fetcher };
}
describe('IP administrative CSRF', () => {
  it('registers 32 random bytes once per client, never reads the portal cookie', async () => {
    const a = harness();
    const b = harness();
    await Promise.all([a.client.post('/write'), a.client.post('/write')]);
    await b.client.post('/write');
    expect(a.calls).toHaveLength(3);
    const token = JSON.parse(String(a.calls[0].init.body)).token;
    expect(token).toMatch(/^[A-Za-z0-9_-]{42}[AEIMQUYcgkosw048]$/);
    expect(JSON.parse(String(b.calls[0].init.body)).token).not.toBe(token);
    expect(new Headers(a.calls[1].init.headers).get('X-Auriga-CSRF')).toBe(
      token,
    );
    expect(a.calls[0].init).toMatchObject({
      credentials: 'same-origin',
      redirect: 'error',
      method: 'POST',
    });
    expect(new Headers(a.calls[0].init.headers).get('Origin')).toBeNull();
    expect(
      new Headers(a.calls[0].init.headers).get('X-Auriga-CSRF-Bootstrap'),
    ).toBe('admin-session-v1');
  });
  it('fails without secure randomness', async () => {
    vi.stubGlobal('crypto', undefined);
    const h = harness();
    await expect(h.client.post('/write')).rejects.toThrow('Secure randomness');
    expect(h.calls).toHaveLength(0);
  });
  it('renews only on expiry; ordinary reads do not extend registration', async () => {
    vi.useFakeTimers();
    const h = harness();
    await h.client.post('/write');
    await h.client.get('/me');
    vi.advanceTimersByTime(600_001);
    await h.client.post('/write');
    expect(h.calls.filter((c) => c.url.endsWith('/csrf-tokens'))).toHaveLength(
      2,
    );
  });
  it.each([-60_000, 60_000])(
    'uses the server Date for registration lifetime with clock offset %i',
    async (offset) => {
      vi.useFakeTimers();
      const h = harness();
      h.handler.mockImplementationOnce(async (_url, init) => {
        const serverNow = Date.now() + offset;
        return json(
          {
            ...JSON.parse(String(init.body)),
            expiresAt: new Date(serverNow + 900_000).toISOString(),
          },
          200,
          { Date: new Date(serverNow).toUTCString() },
        );
      });
      await h.client.post('/write');
      await h.client.post('/write');
      expect(
        h.calls.filter((c) => c.url.endsWith('/csrf-tokens')),
      ).toHaveLength(1);
      vi.advanceTimersByTime(900_001);
      await h.client.post('/write');
      expect(
        h.calls.filter((c) => c.url.endsWith('/csrf-tokens')),
      ).toHaveLength(2);
    },
  );
  it.each(['headers', 'body'])(
    'does not extend server expiry by delayed response %s',
    async (delayAt) => {
      vi.useFakeTimers();
      const h = harness();
      h.handler.mockImplementationOnce(async (_url, init) => {
        const issued = Date.now();
        const value = {
          ...JSON.parse(String(init.body)),
          expiresAt: new Date(issued + 10_000).toISOString(),
        };
        const response = json(value, 200, {
          Date: new Date(issued).toUTCString(),
        });
        if (delayAt === 'headers') vi.advanceTimersByTime(5_000);
        else
          vi.spyOn(response, 'json').mockImplementationOnce(async () => {
            vi.advanceTimersByTime(5_000);
            return value;
          });
        return response;
      });
      await h.client.post('/write');
      vi.advanceTimersByTime(6_000);
      await h.client.post('/write');
      expect(
        h.calls.filter((c) => c.url.endsWith('/csrf-tokens')),
      ).toHaveLength(2);
    },
  );
  it('rejects a registration that expires before its body arrives', async () => {
    vi.useFakeTimers();
    const h = harness();
    h.handler.mockImplementationOnce(async (_url, init) => {
      const issued = Date.now();
      const response = json(
        {
          ...JSON.parse(String(init.body)),
          expiresAt: new Date(issued + 10_000).toISOString(),
        },
        200,
        { Date: new Date(issued).toUTCString() },
      );
      vi.advanceTimersByTime(11_000);
      return response;
    });
    await expect(h.client.post('/write')).rejects.toThrow('Invalid CSRF');
    expect(h.calls).toHaveLength(1);
  });
  it('logs out using the existing token when all 32 slots are occupied', async () => {
    const h = harness();
    await h.client.post('/write');
    const token = JSON.parse(String(h.calls[0].init.body)).token;
    h.handler.mockImplementation(async (url, init) => {
      if (url.endsWith('/csrf-tokens'))
        return json({ code: 'common.rate_limited' }, 429, {
          'Retry-After': '600',
        });
      expect(new Headers(init.headers).get('X-Auriga-CSRF')).toBe(token);
      return new Response(null, { status: 204 });
    });
    await createIamApi(h.client).logout();
    expect(h.calls.filter((c) => c.url.endsWith('/logout'))).toHaveLength(1);
    expect(h.calls.filter((c) => c.url.endsWith('/csrf-tokens'))).toHaveLength(
      1,
    );
    await expect(h.client.post('/write')).rejects.toMatchObject({
      status: 429,
    });
  });
  it('preserves logout candidate and Retry-After after bootstrap 503', async () => {
    vi.useFakeTimers();
    const h = harness();
    h.handler.mockImplementation(async () =>
      json({ code: 'common.unavailable' }, 503, { 'Retry-After': '1' }),
    );
    const api = createIamApi(h.client);
    await expect(api.logout()).rejects.toMatchObject({ status: 503 });
    await expect(api.logout()).rejects.toMatchObject({ status: 503 });
    expect(h.calls).toHaveLength(1);
    vi.advanceTimersByTime(1_000);
    await expect(api.logout()).rejects.toMatchObject({ status: 503 });
    expect(h.calls[1].init.body).toBe(h.calls[0].init.body);
  });
  it('logout invalidates an older bootstrap and its waiting business write', async () => {
    const h = harness();
    let finish!: () => void;
    h.handler.mockImplementationOnce(
      (_url, init) =>
        new Promise((resolve) => {
          finish = () =>
            resolve(
              json({
                ...JSON.parse(String(init.body)),
                expiresAt: new Date(Date.now() + 60_000).toISOString(),
              }),
            );
        }),
    );
    const pending = h.client.post('/old-write');
    await createIamApi(h.client).logout();
    finish();
    await expect(pending).rejects.toThrow('Session changed');
    expect(h.calls.some((c) => c.url.endsWith('/old-write'))).toBe(false);
    await h.client.post('/new-write');
    expect(h.calls.filter((c) => c.url.endsWith('/csrf-tokens'))).toHaveLength(
      3,
    );
  });
  it.each([429, 503])(
    'respects Retry-After for %i and retries the same candidate',
    async (status) => {
      vi.useFakeTimers();
      const h = harness();
      h.handler.mockImplementationOnce(async () =>
        json(
          {
            code: status === 429 ? 'common.rate_limited' : 'common.unavailable',
          },
          status,
          { 'Retry-After': '30' },
        ),
      );
      await expect(h.client.post('/write')).rejects.toMatchObject({
        status,
        retryAfter: '30',
      });
      await expect(h.client.post('/write')).rejects.toMatchObject({ status });
      expect(h.calls).toHaveLength(1);
      vi.advanceTimersByTime(30_000);
      await h.client.post('/write');
      expect(h.calls[0].init.body).toBe(h.calls[1].init.body);
    },
  );
  it('never replays business forbidden responses', async () => {
    const h = harness();
    await h.client.post('/write');
    h.handler.mockImplementationOnce(async () =>
      json({ code: 'common.forbidden' }, 403),
    );
    await expect(h.client.post('/write')).rejects.toMatchObject({
      status: 403,
    });
    expect(h.calls).toHaveLength(3);
  });
  it.each(['mismatch', 'expired', 'too-long'])(
    'rejects invalid registration response: %s',
    async (invalid) => {
      const h = harness();
      h.handler.mockImplementationOnce(async (_url, init) =>
        json({
          token:
            invalid === 'mismatch'
              ? 'wrong'
              : JSON.parse(String(init.body)).token,
          expiresAt: new Date(
            Date.now() +
              (invalid === 'expired'
                ? -1000
                : invalid === 'too-long'
                  ? 901_000
                  : 60_000),
          ).toISOString(),
        }),
      );
      await expect(h.client.post('/write')).rejects.toThrow('Invalid CSRF');
      expect(h.calls).toHaveLength(1);
    },
  );
  it('invalidates a pending registration when the real logout API starts', async () => {
    const h = harness();
    let finish!: () => void;
    h.handler.mockImplementationOnce(
      (_url, init) =>
        new Promise((resolve) => {
          finish = () =>
            resolve(
              json({
                ...JSON.parse(String(init.body)),
                expiresAt: new Date(Date.now() + 60_000).toISOString(),
              }),
            );
        }),
    );
    const pending = h.client.post('/write');
    const logout = createIamApi(h.client).logout();
    finish();
    await expect(pending).rejects.toThrow('Session changed');
    await logout;
    await h.client.post('/write');
    expect(h.calls.filter((c) => c.url.endsWith('/csrf-tokens'))).toHaveLength(
      3,
    );
  });
  it('discards a registration after logout/session invalidation', async () => {
    const h = harness();
    let finish!: (value: Response) => void;
    h.handler.mockImplementationOnce(
      (_url, init) =>
        new Promise((resolve) => {
          finish = () =>
            resolve(
              json({
                ...JSON.parse(String(init.body)),
                expiresAt: new Date(Date.now() + 60_000).toISOString(),
              }),
            );
        }),
    );
    const write = h.client.post('/write');
    h.client.resetSession();
    finish(new Response());
    await expect(write).rejects.toThrow('Session changed');
    await h.client.post('/write');
    expect(h.calls).toHaveLength(3);
  });
  it('clears registration and reports 401, including reads', async () => {
    const h = harness();
    await h.client.post('/write');
    h.handler.mockImplementationOnce(async () => json({}, 401));
    await expect(h.client.get('/me')).rejects.toMatchObject({ status: 401 });
    await h.client.post('/write');
    expect(h.calls).toHaveLength(5);
  });
  it('a concurrent read returning 401 prevents an older bootstrap from publishing', async () => {
    const h = harness();
    let finish!: () => void;
    h.handler.mockImplementationOnce(
      (_url, init) =>
        new Promise((resolve) => {
          finish = () =>
            resolve(
              json({
                ...JSON.parse(String(init.body)),
                expiresAt: new Date(Date.now() + 60_000).toISOString(),
              }),
            );
        }),
    );
    const pending = h.client.post('/write');
    h.handler.mockImplementationOnce(async () => json({}, 401));
    await expect(h.client.get('/me')).rejects.toMatchObject({ status: 401 });
    finish();
    await expect(pending).rejects.toThrow('Session changed');
    expect(h.calls).toHaveLength(2);
  });
  it('rejects foreign paths/bases and Authorization before sending any token', async () => {
    const h = harness();
    await expect(h.client.post('https://evil.example/write')).rejects.toThrow(
      'same origin',
    );
    await expect(
      h.client.post(
        '/write',
        {},
        { headers: { Authorization: 'Bearer forged' } },
      ),
    ).rejects.toThrow('browser session');
    const c = createApiClient({
      baseUrl: 'https://evil.example',
      getAdminAccessMode: () => 'ip',
      fetch: h.fetcher as typeof fetch,
    });
    await expect(c.post('/write')).rejects.toThrow('same origin');
    expect(h.calls).toHaveLength(0);
  });
  it('forces redirect error on every request carrying an in-memory token', async () => {
    const h = harness();
    await h.client.post('/write', {}, { redirect: 'follow' });
    expect(h.calls.every((c) => c.init.redirect === 'error')).toBe(true);
  });
  it('stepup requires a session token and a completed callback invalidates it', async () => {
    const h = harness();
    const api = createIamApi(h.client);
    await api
      .createLoginTransaction({ purpose: 'stepup', providerId: 'provider' })
      .catch(() => {});
    expect(h.calls[0].url).toContain('/csrf-tokens');
    h.handler.mockImplementationOnce(async () =>
      json({
        id: 'tx',
        purpose: 'login',
        status: 'completed',
        step: { type: 'complete' },
      }),
    );
    await api.getLoginTransaction('tx');
    await h.client.post('/write');
    expect(h.calls.filter((c) => c.url.endsWith('/csrf-tokens'))).toHaveLength(
      2,
    );
  });
  it('keeps anonymous login create/advance/cancel working while link requires CSRF', async () => {
    const h = harness();
    const api = createIamApi(h.client);
    h.handler.mockImplementationOnce(async () =>
      json({ id: 'tx', purpose: 'login', step: { type: 'form' } }),
    );
    await api.createLoginTransaction({
      purpose: 'login',
      providerId: 'provider',
    });
    h.handler.mockImplementationOnce(async () =>
      json({ id: 'tx', purpose: 'login', step: { type: 'form' } }),
    );
    await api.advanceLoginTransaction('tx', {
      type: 'form',
      formId: 'password',
      values: {},
    });
    await api.cancelLoginTransaction('tx');
    expect(h.calls).toHaveLength(3);
    expect(
      h.calls.every((c) => !new Headers(c.init.headers).has('X-Auriga-CSRF')),
    ).toBe(true);
    await api
      .createLoginTransaction({ purpose: 'link', providerId: 'provider' })
      .catch(() => {});
    expect(h.calls[3].url).toContain('/csrf-tokens');
  });
  it('fails closed for missing, duplicate or invalid public mode', async () => {
    const c = createApiClient({ getAdminAccessMode: () => readAccessMode() });
    await expect(c.post('/write')).rejects.toThrow('入口模式');
    document.head.innerHTML = '<meta name="auriga-access-mode" content="ip">';
    expect(readAccessMode()).toBe('ip');
    document.head.innerHTML +=
      '<meta name="auriga-access-mode" content="domain">';
    expect(() => readAccessMode()).toThrow();
    document.head.innerHTML =
      '<meta name="auriga-access-mode" content="invalid">';
    document.documentElement.lang = 'en';
    expect(() => readAccessMode()).toThrow('Access mode');
    document.documentElement.lang = '';
  });
  it('preserves domain administrative cookie behavior', async () => {
    const fetcher = vi.fn<typeof fetch>(
      async () => new Response(null, { status: 204 }),
    );
    const c = createApiClient({
      getAdminAccessMode: () => 'domain',
      getCookie: () => 'auriga_csrf=domain-cookie',
      fetch: fetcher,
    });
    await c.post('/write');
    expect(
      new Headers(fetcher.mock.calls[0]?.[1]?.headers).get('X-Auriga-CSRF'),
    ).toBe('domain-cookie');
  });
});
