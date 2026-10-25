import {
  createApiClient,
  readAccessMode,
  type ApiClientOptions,
} from '@auriga/shared';
import type { components } from '../../../shared/src/generated/ai';
import { apiClient } from '../api';

type Schemas = components['schemas'];
export type Provider = Schemas['Provider'];
export type ProviderInput = Schemas['ProviderInput'];
export type Model = Schemas['Model'];
export type ModelInput = Schemas['ModelInput'];
export type ModelPatch = Schemas['ModelPatch'];
export type Credential = Schemas['Credential'];
export type ModelGrants = Schemas['ModelGrants'];
export type Versioned<T> = { data: T; etag: string | null };
const base = '/api/ai/v1/admin';
const id = encodeURIComponent;
const headers = (etag: string) => {
  if (!etag.trim() || etag === '*') throw new Error('ETag required');
  return { 'If-Match': etag };
};
export function createModelProvidersApi(options: ApiClientOptions = {}) {
  // Wrap successful AI responses inside the transport so each concurrent request
  // carries its own real response ETag. CSRF bootstrap and Problem stay untouched.
  const client = createApiClient({
    ...options,
    fetch: async (input: RequestInfo | URL, init?: RequestInit) => {
      const response = await (options.fetch ?? globalThis.fetch)(input, init);
      const path = new URL(
        String(input),
        globalThis.location?.href ?? 'http://localhost',
      ).pathname;
      if (!path.startsWith(base) || !response.ok || response.redirected)
        return response;
      return new Response(
        JSON.stringify({
          data: await response.json(),
          etag: response.headers.get('ETag'),
        }),
        {
          status: response.status,
          headers: { 'Content-Type': 'application/json' },
        },
      );
    },
  });
  const get = <T>(path: string, signal?: AbortSignal) =>
    client.get<Versioned<T>>(`${base}${path}`, { cache: 'no-store', signal });
  const post = <T>(
    path: string,
    body: unknown,
    key: string = globalThis.crypto.randomUUID(),
    etag?: string,
    signal?: AbortSignal,
  ) =>
    client.post<Versioned<T>>(`${base}${path}`, body, {
      signal,
      headers: {
        'Idempotency-Key': key,
        ...(etag === undefined ? {} : headers(etag)),
      },
    });
  const page = (resource: string, cursor?: string) =>
    `/${resource}?limit=200${cursor ? `&cursor=${id(cursor)}` : ''}`;
  return {
    providers: (cursor?: string, signal?: AbortSignal) =>
      get<Schemas['ProviderPage']>(page('providers', cursor), signal),
    provider: (providerId: string, signal?: AbortSignal) =>
      get<Provider>(`/providers/${id(providerId)}`, signal),
    createProvider: (body: ProviderInput, key?: string, signal?: AbortSignal) =>
      post<Provider>('/providers', body, key, undefined, signal),
    patchProvider: async (
      providerId: string,
      body: Schemas['ProviderPatch'],
      etag: string,
      signal?: AbortSignal,
    ) =>
      client.patch<Versioned<Provider>>(
        `${base}/providers/${id(providerId)}`,
        body,
        { signal, headers: headers(etag) },
      ),
    createCredential: (
      providerId: string,
      body: Schemas['CredentialInput'],
      key?: string,
      signal?: AbortSignal,
    ) =>
      post<Credential>(
        `/providers/${id(providerId)}/credentials`,
        body,
        key,
        undefined,
        signal,
      ),
    credentialAction: async (
      providerId: string,
      credentialId: string,
      action: 'activate' | 'revoke',
      body: Schemas['CredentialAction'],
      etag: string,
      key?: string,
      signal?: AbortSignal,
    ) =>
      post<Credential>(
        `/providers/${id(providerId)}/credentials/${id(credentialId)}/${action}`,
        body,
        key,
        etag,
        signal,
      ),
    models: (cursor?: string, signal?: AbortSignal) =>
      get<Schemas['ModelPage']>(page('models', cursor), signal),
    model: (modelId: string, signal?: AbortSignal) =>
      get<Model>(`/models/${id(modelId)}`, signal),
    readGrants: (modelId: string, signal?: AbortSignal) =>
      get<ModelGrants>(`/models/${id(modelId)}/grants`, signal),
    credential: (
      providerId: string,
      credentialId: string,
      signal?: AbortSignal,
    ) =>
      get<Credential>(
        `/providers/${id(providerId)}/credentials/${id(credentialId)}`,
        signal,
      ),
    createModel: (body: ModelInput, key?: string, signal?: AbortSignal) =>
      post<Model>('/models', body, key, undefined, signal),
    patchModel: async (
      modelId: string,
      body: Schemas['ModelPatch'],
      etag: string,
      signal?: AbortSignal,
    ) =>
      client.patch<Versioned<Model>>(`${base}/models/${id(modelId)}`, body, {
        signal,
        headers: headers(etag),
      }),
    grants: async (
      modelId: string,
      body: ModelGrants,
      etag: string,
      signal?: AbortSignal,
    ) =>
      client.put<Versioned<ModelGrants>>(
        `${base}/models/${id(modelId)}/grants`,
        body,
        { signal, headers: headers(etag) },
      ),
  };
}
export const modelProvidersApi = createModelProvidersApi({
  getAdminAccessMode: () => readAccessMode(),
  getLocale: () => document.documentElement.lang || 'zh-CN',
  onUnauthenticated: () => {
    apiClient.resetSession();
    void import('../router').then(({ router }) => {
      if (router.currentRoute.value.path !== '/login')
        void router.push({
          path: '/login',
          query: { returnTo: router.currentRoute.value.fullPath },
        });
    });
  },
});
