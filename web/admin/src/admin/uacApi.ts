import type { IAMComponents } from '@auriga/shared';
import { apiClient } from '../api';
export type UacConfig = IAMComponents['schemas']['UacProviderConfig'];
export type DiagnosticSettings =
  IAMComponents['schemas']['UacDiagnosticSettings'];
type DiagnosticWrite = IAMComponents['schemas']['UacDiagnosticSettingsWrite'];
type DiagnosticExport = IAMComponents['schemas']['UacDiagnosticExport'];
const base = '/api/iam/v1/admin';
const path = (id: string) => `${base}/providers/${encodeURIComponent(id)}/uac`;
async function read<T>(url: string, signal?: AbortSignal) {
  const response = await apiClient.get<Response>(url, {
    responseType: 'response',
    cache: 'no-store',
    signal,
  });
  return {
    data: (await response.json()) as T,
    etag: response.headers.get('ETag'),
  };
}
export const uacApi = {
  get: (id: string, signal?: AbortSignal) => read<UacConfig>(path(id), signal),
  save: (id: string, body: UacConfig, etag: string, signal?: AbortSignal) =>
    apiClient.put<UacConfig>(path(id), body, {
      headers: { 'If-Match': etag },
      signal,
    }),
  diagnostics: (id: string, signal?: AbortSignal) =>
    read<DiagnosticSettings>(`${path(id)}/diagnostics`, signal),
  setDiagnostics: (
    id: string,
    body: DiagnosticWrite,
    etag: string,
    signal?: AbortSignal,
  ) =>
    apiClient.put<DiagnosticSettings>(`${path(id)}/diagnostics`, body, {
      headers: { 'If-Match': etag },
      signal,
    }),
  bundle: (body: DiagnosticExport, signal?: AbortSignal) =>
    apiClient.post<Response>(`${base}/uac/diagnostic-bundles`, body, {
      responseType: 'response',
      signal,
    }),
};
