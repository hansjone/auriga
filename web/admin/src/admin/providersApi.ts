import { createApiClient, readAccessMode } from '@auriga/shared';
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

export type UacConfig = IAMComponents['schemas']['UacProviderConfig'];
export type UacDiagnostics = IAMComponents['schemas']['UacDiagnosticSettings'];
export type UacExport = IAMComponents['schemas']['UacDiagnosticExport'];
// Each reader captures only its own response headers. The shared client still
// owns writes, CSRF registration and step-up continuation.
async function versioned<T>(path: string, signal?: AbortSignal) {
  let etag: string | null = null;
  const reader = createApiClient({
    getAdminAccessMode: () => readAccessMode(),
    onUnauthenticated: () => apiClient.resetSession(),
    fetch: async (input, init) => {
      const response = await globalThis.fetch(input, init);
      etag = response.headers.get('ETag');
      return response;
    },
  });
  const data = await reader.get<T>(path, { signal, cache: 'no-store' });
  return { data, etag };
}
const uacPath = (id: string) =>
  `${base}/providers/${encodeURIComponent(id)}/uac`;
export const uacApi = {
  provider: (id: string, signal?: AbortSignal) =>
    versioned<Provider>(`${base}/providers/${encodeURIComponent(id)}`, signal),
  get: (id: string, signal?: AbortSignal) =>
    versioned<UacConfig>(uacPath(id), signal),
  diagnostics: (id: string, signal?: AbortSignal) =>
    versioned<UacDiagnostics>(`${uacPath(id)}/diagnostics`, signal),
  put: (id: string, body: UacConfig, etag: string, signal?: AbortSignal) =>
    apiClient.put<UacConfig>(uacPath(id), body, {
      signal,
      headers: { 'If-Match': etag },
    }),
  putDiagnostics: (
    id: string,
    body: IAMComponents['schemas']['UacDiagnosticSettingsWrite'],
    etag: string,
    signal?: AbortSignal,
  ) =>
    apiClient.put<UacDiagnostics>(`${uacPath(id)}/diagnostics`, body, {
      signal,
      headers: { 'If-Match': etag },
    }),
  async download(body: UacExport, signal?: AbortSignal) {
    // Adapt a binary response for the existing JSON client without bypassing
    // its Origin, CSRF, redirect and problem-response handling.
    let bundle: { blob: Blob; partial: boolean } | undefined;
    const client = createApiClient({
      getAdminAccessMode: () => readAccessMode(),
      onUnauthenticated: () => apiClient.resetSession(),
      fetch: async (input, init) => {
        const response = await globalThis.fetch(input, init);
        if (
          !response.ok ||
          !new URL(String(input), location.href).pathname.endsWith(
            '/diagnostic-bundles',
          )
        )
          return response;
        if (
          response.redirected ||
          response.headers.get('Content-Type')?.split(';')[0] !==
            'application/zip'
        )
          throw new Error('Invalid diagnostic bundle');
        const blob = await response.blob();
        if (!blob.size || blob.size > 50 * 1024 * 1024)
          throw new Error('Invalid diagnostic bundle size');
        bundle = {
          blob,
          partial:
            response.headers.get('X-Auriga-Diagnostic-Partial') === 'true',
        };
        return new Response('{}', {
          headers: { 'Content-Type': 'application/json' },
        });
      },
    });
    await client.post(`${base}/uac/diagnostic-bundles`, body, {
      signal,
      headers: { Accept: 'application/zip' },
    });
    if (!bundle) throw new Error('Missing diagnostic bundle');
    return bundle;
  },
};
