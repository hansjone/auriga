import type { components, paths } from '../../../shared/src/generated/ai';
import { apiClient } from '../api';

export type Usage = components['schemas']['Usage'];
export type Model = components['schemas']['Model'];
export type Provider = components['schemas']['Provider'];
type Page<P extends keyof paths> = paths[P] extends {
  get: { responses: { 200: { content: { 'application/json': infer R } } } };
}
  ? R
  : never;
const base = '/api/ai/v1/admin';
function query(cursor?: string) {
  const params = new URLSearchParams({ limit: '100' });
  if (cursor) params.set('cursor', cursor);
  return params;
}
export const modelUsageApi = {
  usage: (cursor?: string, signal?: AbortSignal) =>
    apiClient.get<Page<'/api/ai/v1/admin/usage'>>(
      `${base}/usage?${query(cursor)}`,
      { signal, cache: 'no-store' },
    ),
  models: (cursor?: string, signal?: AbortSignal) =>
    apiClient.get<Page<'/api/ai/v1/admin/models'>>(
      `${base}/models?${query(cursor)}`,
      { signal, cache: 'no-store' },
    ),
  providers: (cursor?: string, signal?: AbortSignal) =>
    apiClient.get<Page<'/api/ai/v1/admin/providers'>>(
      `${base}/providers?${query(cursor)}`,
      { signal, cache: 'no-store' },
    ),
};
