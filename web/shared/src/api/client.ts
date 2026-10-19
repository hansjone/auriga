import { isWriteMethod, readCsrfCookie } from './csrf';
import { ApiError, type Problem } from './problem';

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
};

export type ApiClient = {
  request: <T>(path: string, init?: RequestInit) => Promise<T>;
  get: <T>(path: string, init?: RequestInit) => Promise<T>;
  post: <T>(path: string, body?: unknown, init?: RequestInit) => Promise<T>;
  put: <T>(path: string, body?: unknown, init?: RequestInit) => Promise<T>;
  patch: <T>(path: string, body?: unknown, init?: RequestInit) => Promise<T>;
  delete: <T>(path: string, init?: RequestInit) => Promise<T>;
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

  function fetchImpl(
    input: RequestInfo | URL,
    init?: RequestInit,
  ): Promise<Response> {
    // Resolve fetch at call time so enableAurigaMock() can patch globalThis.fetch
    // after the client module has already been imported.
    if (options.fetch) return options.fetch(input, init);
    return globalThis.fetch(input, init);
  }

  async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
    const method = (init.method ?? 'GET').toUpperCase();
    const headers = new Headers(init.headers);
    if (!headers.has('Accept')) headers.set('Accept', 'application/json');
    const locale = options.getLocale?.() ?? 'zh-CN';
    if (!headers.has('Accept-Language')) headers.set('Accept-Language', locale);

    if (isWriteMethod(method)) {
      const token = readCsrfCookie(cookieHeader(options));
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

    const res = await fetchImpl(`${baseUrl}${path}`, {
      ...init,
      method,
      headers,
      credentials: init.credentials ?? 'same-origin',
    });

    if (res.status === 401) {
      options.onUnauthenticated?.();
    }

    if (res.status === 204) {
      return undefined as T;
    }

    if (!res.ok) {
      throw new ApiError(await parseProblem(res));
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
    init?: RequestInit,
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
    request,
    get: (path, init) => request(path, { ...init, method: 'GET' }),
    post: (path, body, init) => withBody('POST', path, body, init),
    put: (path, body, init) => withBody('PUT', path, body, init),
    patch: (path, body, init) => withBody('PATCH', path, body, init),
    delete: (path, init) => request(path, { ...init, method: 'DELETE' }),
  };
}
