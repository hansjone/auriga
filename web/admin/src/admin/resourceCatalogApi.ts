import type { components } from '../../../shared/src/generated/resource';
import { ApiError } from '@auriga/shared';
import { apiClient, readResource } from '../api';

export type CatalogVersion = components['schemas']['UnifiedVersion'];
export type CatalogReview = components['schemas']['UnifiedReview'];
export type CatalogDistribution = components['schemas']['UnifiedDistribution'];
export type CatalogPackage = components['schemas']['UnifiedPackage'];
type S = components['schemas'];
type ReadResult<T> = Promise<{ data: T; etag: string | null }>;
const base = '/api/resource/v1/catalog/packages';
const admin = '/api/resource/v1/admin/catalog';
const id = encodeURIComponent;
const path = (p: string, v: string) => `${base}/${id(p)}/versions/${id(v)}`;
const headers = (etag: string, key: string) => ({
  'If-Match': etag,
  'Idempotency-Key': key,
});
const query = (cursor?: string) =>
  new URLSearchParams({
    kind: 'skill',
    limit: '50',
    ...(cursor ? { cursor } : {}),
  });
export const resourceCatalogApi = {
  packages: (cursor?: string) =>
    apiClient.get<S['UnifiedPackagePage']>(`${base}?${query(cursor)}`, {
      cache: 'no-store',
    }),
  get: (p: string): ReadResult<CatalogPackage> =>
    readResource<CatalogPackage>(`${base}/${id(p)}`),
  create: (body: S['UnifiedPackageInput'], key: string) =>
    apiClient.post<CatalogPackage>(base, body, {
      headers: headers('"0"', key),
    }),
  versions: (p: string, cursor?: string) =>
    apiClient.get<S['UnifiedVersionPage']>(
      `${base}/${id(p)}/versions?${new URLSearchParams({ limit: '50', ...(cursor ? { cursor } : {}) })}`,
      { cache: 'no-store' },
    ),
  version: (p: string, v: string): ReadResult<CatalogVersion> =>
    readResource<CatalogVersion>(path(p, v)),
  reviews: (cursor?: string) =>
    apiClient.get<S['UnifiedReviewPage']>(`${admin}/review?${query(cursor)}`, {
      cache: 'no-store',
    }),
  review: (r: string): ReadResult<CatalogReview> =>
    readResource<CatalogReview>(`${admin}/reviews/${id(r)}`),
  published: (cursor?: string) =>
    apiClient.get<S['UnifiedVersionPage']>(
      `${admin}/published?${query(cursor)}`,
      { cache: 'no-store' },
    ),
  decide: (
    r: string,
    body: S['UnifiedReviewDecision'],
    etag: string,
    key: string,
  ) =>
    apiClient.post<CatalogReview>(`${admin}/reviews/${id(r)}/decisions`, body, {
      headers: headers(etag, key),
    }),
  publish: (p: string, v: string, reason: string, etag: string, key: string) =>
    apiClient.post<CatalogVersion>(
      `${admin}/packages/${id(p)}/versions/${id(v)}/publications`,
      { reason },
      { headers: headers(etag, key) },
    ),
  withdraw: (
    p: string,
    v: string,
    mode: 'withdrawn' | 'malicious',
    reason: string,
    etag: string,
    key: string,
  ) =>
    apiClient.post<CatalogVersion>(
      `${admin}/packages/${id(p)}/versions/${id(v)}/withdrawals`,
      { mode, reason },
      { headers: headers(etag, key) },
    ),
  submit: (p: string, v: string, reason: string, etag: string, key: string) =>
    apiClient.post<CatalogReview>(
      `${path(p, v)}/submissions`,
      { reason },
      { headers: headers(etag, key) },
    ),
  upload: (
    p: string,
    metadata: S['UnifiedUploadMetadata'],
    manifest: File,
    artifacts: File[],
    etag: string,
    key: string,
  ) => {
    const body = new FormData();
    body.append(
      'metadata',
      new Blob([JSON.stringify(metadata)], { type: 'application/json' }),
    );
    body.append('manifest', manifest);
    for (const file of artifacts) body.append('artifacts', file);
    return apiClient.post<CatalogVersion>(`${base}/${id(p)}/versions`, body, {
      headers: headers(etag, key),
    });
  },
  distributions: (cursor?: string) =>
    apiClient.get<S['UnifiedDistributionPage']>(
      `${admin}/distributions?${query(cursor)}`,
      { cache: 'no-store' },
    ),
  distribution: (d: string): ReadResult<CatalogDistribution> =>
    readResource<CatalogDistribution>(`${admin}/distributions/${id(d)}`),
  distribute: (
    body: S['UnifiedDistributionInput'],
    etag: string,
    key: string,
  ) =>
    apiClient.post<CatalogDistribution>(`${admin}/distributions`, body, {
      headers: headers(etag, key),
    }),
  // Preview only. Never navigate to server-provided locators or render HTML.
  artifact: async (p: string, v: string, a: string) => {
    const response = await fetch(`${path(p, v)}/artifacts/${id(a)}`, {
      credentials: 'same-origin',
      redirect: 'error',
      cache: 'no-store',
    });
    if (!response.ok) {
      if (response.status === 401) apiClient.invalidatePendingRequests();
      const problem = await response.json().catch(() => undefined);
      if (problem && typeof problem.code === 'string')
        throw new ApiError({ ...problem, status: response.status });
      throw new ApiError({
        title: 'Preview',
        status: response.status,
        code: 'common.internal',
      });
    }
    return response;
  },
};
