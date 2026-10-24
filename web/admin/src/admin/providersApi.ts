import type { IAMComponents, IAMPaths } from '@auriga/shared';
import { apiClient } from '../api';
export type Provider = IAMComponents['schemas']['Provider'];
export type ProviderInput = IAMComponents['schemas']['ProviderInput'];
export type ProviderPatch = IAMComponents['schemas']['ProviderPatch'];
export type ConnectorType = IAMComponents['schemas']['ConnectorType'];
type Providers =
  IAMPaths['/admin/providers']['get']['responses']['200']['content']['application/json'];
type Connectors =
  IAMPaths['/admin/connectors']['get']['responses']['200']['content']['application/json'];
const base = '/api/iam/v1/admin';
export const providersApi = {
  list: (signal?: AbortSignal) =>
    apiClient.get<Providers>(`${base}/providers`, {
      signal,
      cache: 'no-store',
    }),
  connectors: (signal?: AbortSignal) =>
    apiClient.get<Connectors>(`${base}/connectors`, {
      signal,
      cache: 'no-store',
    }),
  get: (id: string, signal?: AbortSignal) =>
    apiClient.get<Provider>(`${base}/providers/${encodeURIComponent(id)}`, {
      signal,
      cache: 'no-store',
    }),
  create: (body: ProviderInput, signal?: AbortSignal) =>
    apiClient.post<Provider>(`${base}/providers`, body, { signal }),
  patch: (id: string, body: ProviderPatch, signal?: AbortSignal) =>
    apiClient.patch<Provider>(
      `${base}/providers/${encodeURIComponent(id)}`,
      body,
      { signal, headers: { 'Content-Type': 'application/merge-patch+json' } },
    ),
  disable: (id: string, signal?: AbortSignal) =>
    apiClient.delete<void>(`${base}/providers/${encodeURIComponent(id)}`, {
      signal,
    }),
};
