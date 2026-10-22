import { isWriteMethod, readCsrfCookie } from './csrf';
import { ApiError, type Problem } from './problem';
import type { AccessMode } from './access-mode';
import type { components } from '../generated/iam';

export type ApiRequestInit = RequestInit & { anonymousLogin?: boolean };

export type ApiClientOptions = {
  /** Prefix for API paths, default empty (paths already include /api/...). */
  baseUrl?: string;
  getLocale?: () => string;
  /** Called on HTTP 401 before the error is thrown. */
  onUnauthenticated?: () => void;
  /** Override fetch (tests / mock). */
  fetch?: typeof fetch;
  /** Cookie header string for CSRF (defaults to document.cookie). */
  getCookie?: () => string;
  /** Set only by the admin application; resolved lazily after mock assembly. */
  getAdminAccessMode?: () => AccessMode;
};

export type ApiClient = {
  resetSession: () => void;
  request: <T>(path: string, init?: ApiRequestInit) => Promise<T>;
  get: <T>(path: string, init?: ApiRequestInit) => Promise<T>;
  post: <T>(path: string, body?: unknown, init?: ApiRequestInit) => Promise<T>;
  put: <T>(path: string, body?: unknown, init?: ApiRequestInit) => Promise<T>;
  patch: <T>(path: string, body?: unknown, init?: ApiRequestInit) => Promise<T>;
  delete: <T>(path: string, init?: ApiRequestInit) => Promise<T>;
};

async function parseProblem(res: Response): Promise<Problem> {
  const fallback: Problem = {
    title: res.statusText || 'Error',
    status: res.status,
    code: res.status === 401 ? 'common.unauthenticated' : 'common.internal',
  };
  try {
    const data = (await res.json()) as Partial<Problem>;
    return {
      title: data.title ?? fallback.title,
      status: data.status ?? res.status,
      code: data.code ?? fallback.code,
      detail: data.detail,
      type: data.type,
      instance: data.instance,
      params: data.params,
      errors: data.errors,
      traceId: data.traceId,
    };
  } catch {
    return fallback;
  }
}

function cookieHeader(options: ApiClientOptions): string {
  if (options.getCookie) return options.getCookie();
  return typeof document !== 'undefined' ? document.cookie : '';
}

export function createApiClient(options: ApiClientOptions = {}): ApiClient {
  const baseUrl = options.baseUrl ?? '';
  let generation = 0;
  let registered: components['schemas']['AdminCsrfToken'] | undefined;
  let registeredUntil = 0;
  let candidate: string | undefined;
  let flight: Promise<string> | undefined;
  let retryAt = 0;
  let retryError: ApiError | undefined;

  function resetSession() {
    generation++;
    registered = undefined;
    registeredUntil = 0;
    candidate = undefined;
    flight = undefined;
    retryAt = 0;
    retryError = undefined;
  }

  function sameOriginUrl(path: string): string {
    const base = new URL(baseUrl || '/', globalThis.location.href);
    const url = new URL(path, base);
    if (
      base.origin !== globalThis.location.origin ||
      base.pathname !== '/' ||
      base.search ||
      base.hash ||
      url.origin !== globalThis.location.origin ||
      url.username ||
      url.password ||
      url.hash
    ) {
      throw new Error(
        '管理请求必须同源 / Administrative requests must use the same origin',
      );
    }
    return url.href;
  }

  async function responseError(res: Response): Promise<ApiError> {
    if (res.status === 401) {
      resetSession();
      options.onUnauthenticated?.();
    }
    return new ApiError(
      await parseProblem(res),
      res.headers.get('Retry-After') ?? undefined,
    );
  }

  async function memoryToken(): Promise<string> {
    if (registered && registeredUntil > Date.now()) return registered.token;
    if (flight) return flight;
    if (Date.now() < retryAt && retryError) throw retryError;
    const epoch = generation;
    const pending = (async () => {
      if (!candidate) {
        const bytes = new Uint8Array(32);
        if (!globalThis.crypto?.getRandomValues)
          throw new Error('安全随机源不可用 / Secure randomness unavailable');
        globalThis.crypto.getRandomValues(bytes);
        candidate = btoa(String.fromCharCode(...bytes))
          .replaceAll('+', '-')
          .replaceAll('/', '_')
          .replaceAll('=', '');
      }
      const token = candidate;
      const body: components['schemas']['AdminCsrfTokenRegistration'] = {
        token,
      };
      const res = await fetchImpl(
        sameOriginUrl('/api/iam/v1/admin/session/csrf-tokens'),
        {
          method: 'POST',
          credentials: 'same-origin',
          redirect: 'error',
          headers: {
            'Content-Type': 'application/json',
            Accept: 'application/json',
            'X-Auriga-CSRF-Bootstrap': 'admin-session-v1',
          },
          body: JSON.stringify(body),
        },
      );
      if (!res.ok) {
        const error = await responseError(res);
        if (
          epoch === generation &&
          (res.status === 429 || res.status === 503)
        ) {
          const after = error.retryAfter;
          const seconds =
            after && /^\d+$/.test(after)
              ? Number(after) * 1000
              : Date.parse(after ?? '') - Date.now();
          retryAt =
            Date.now() +
            (Number.isFinite(seconds) && seconds > 0 ? seconds : 1000);
          retryError = error;
        }
        throw error;
      }
      const value =
        (await res.json()) as components['schemas']['AdminCsrfToken'];
      const expires = Date.parse(value.expiresAt);
      // HTTP Date uses the server clock and whole seconds. Account for its
      // rounding conservatively instead of rejecting valid tokens when the
      // browser clock differs from IAM's clock.
      const date = res.headers.get('Date');
      const reference = date === null ? Date.now() : Date.parse(date);
      const remaining = expires - reference;
      const lifetime = remaining - (date === null ? 0 : 1000);
      if (
        res.redirected ||
        !res.headers.get('content-type')?.includes('application/json') ||
        value.token !== token ||
        typeof value.expiresAt !== 'string' ||
        !/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})$/.test(
          value.expiresAt,
        ) ||
        !Number.isFinite(expires) ||
        !Number.isFinite(reference) ||
        lifetime <= 0 ||
        remaining > (date === null ? 900_000 : 901_000)
      ) {
        throw new Error(
          '无效的 CSRF 登记响应 / Invalid CSRF registration response',
        );
      }
      if (epoch !== generation) throw new Error('会话已改变 / Session changed');
      registered = value;
      registeredUntil = Date.now() + Math.min(lifetime, 900_000);
      candidate = undefined;
      return token;
    })();
    flight = pending;
    try {
      return await pending;
    } finally {
      if (flight === pending) flight = undefined;
    }
  }

  function fetchImpl(
    input: RequestInfo | URL,
    init?: RequestInit,
  ): Promise<Response> {
    // Resolve fetch at call time so enableAurigaMock() can patch globalThis.fetch
    // after the client module has already been imported.
    if (options.fetch) return options.fetch(input, init);
    return globalThis.fetch(input, init);
  }

  async function request<T>(
    path: string,
    init: ApiRequestInit = {},
  ): Promise<T> {
    const method = (init.method ?? 'GET').toUpperCase();
    const epoch = generation;
    const admin = !!options.getAdminAccessMode;
    const target = admin ? sameOriginUrl(path) : `${baseUrl}${path}`;
    const headers = new Headers(init.headers);
    if (admin && headers.has('Authorization'))
      throw new Error(
        '管理请求需要浏览器会话 / Administrative requests require a browser session',
      );
    if (!headers.has('Accept')) headers.set('Accept', 'application/json');
    const locale = options.getLocale?.() ?? 'zh-CN';
    if (!headers.has('Accept-Language')) headers.set('Accept-Language', locale);

    if (isWriteMethod(method)) {
      const mode = options.getAdminAccessMode?.();
      const token =
        mode === 'ip'
          ? init.anonymousLogin
            ? undefined
            : await memoryToken()
          : readCsrfCookie(cookieHeader(options));
      if (mode === 'ip') headers.delete('X-Auriga-CSRF');
      if (token && !headers.has('X-Auriga-CSRF')) {
        headers.set('X-Auriga-CSRF', token);
      }
      if (
        init.body !== undefined &&
        init.body !== null &&
        !(init.body instanceof FormData) &&
        !headers.has('Content-Type')
      ) {
        headers.set('Content-Type', 'application/json');
      }
    }

    if (epoch !== generation) throw new Error('会话已改变 / Session changed');
    const fetchInit = { ...init };
    delete fetchInit.anonymousLogin;
    const res = await fetchImpl(target, {
      ...fetchInit,
      method,
      headers,
      credentials: admin ? 'same-origin' : (init.credentials ?? 'same-origin'),
      ...(admin ? { redirect: 'error' as const } : {}),
    });

    if (admin && res.redirected)
      throw new Error(
        '管理请求不允许重定向 / Administrative redirects are blocked',
      );

    if (res.status === 204) {
      return undefined as T;
    }

    if (!res.ok) {
      throw await responseError(res);
    }

    const ct = res.headers.get('content-type') ?? '';
    if (!ct.includes('json')) {
      return undefined as T;
    }
    return (await res.json()) as T;
  }

  function withBody<T>(
    method: string,
    path: string,
    body?: unknown,
    init?: ApiRequestInit,
  ): Promise<T> {
    const payload =
      body === undefined || body === null
        ? undefined
        : typeof body === 'string' || body instanceof FormData
          ? body
          : JSON.stringify(body);
    return request<T>(path, { ...init, method, body: payload });
  }

  return {
    resetSession,
    request,
    get: (path, init) => request(path, { ...init, method: 'GET' }),
    post: (path, body, init) => withBody('POST', path, body, init),
    put: (path, body, init) => withBody('PUT', path, body, init),
    patch: (path, body, init) => withBody('PATCH', path, body, init),
    delete: (path, init) => request(path, { ...init, method: 'DELETE' }),
  };
}
