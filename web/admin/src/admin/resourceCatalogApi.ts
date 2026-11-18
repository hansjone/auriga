import type { components } from '../../../shared/src/generated/resource';
import { ApiError } from '@auriga/shared';
import { apiClient, readResource } from '../api';
import type { CatalogKind } from './resourceCatalogUpload';
export { browserUuid as catalogKey } from './browserUuid';

export type CatalogVersion = components['schemas']['UnifiedVersion'];
export type CatalogReview = components['schemas']['UnifiedReview'];
export type CatalogDistribution = components['schemas']['UnifiedDistribution'];
export type CatalogPackage = components['schemas']['UnifiedPackage'];
export type CatalogAssignment = components['schemas']['UnifiedAssignment'];
// CatalogController/ResourceWorkflowController encode the persisted revision as
// a quoted decimal ETag. Admin review embeds the version snapshot but only sends
// the review ETag; use its exact version revision for the version CAS.
export function catalogRevisionEtag(revision: number) {
  return Number.isSafeInteger(revision) && revision > 0
    ? `"${revision}"`
    : null;
}
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
const query = (cursor?: string, kind?: CatalogKind) =>
  new URLSearchParams({
    ...(kind ? { kind } : {}),
    limit: '50',
    ...(cursor ? { cursor } : {}),
  });
export const resourceCatalogApi = {
  packages: (cursor?: string, kind?: CatalogKind) =>
    apiClient.get<S['UnifiedPackagePage']>(`${base}?${query(cursor, kind)}`, {
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
  reviews: (cursor?: string, kind?: CatalogKind) =>
    apiClient.get<S['UnifiedReviewPage']>(
      `${admin}/review?${query(cursor, kind)}`,
      {
        cache: 'no-store',
      },
    ),
  review: (r: string): ReadResult<CatalogReview> =>
    readResource<CatalogReview>(`${admin}/reviews/${id(r)}`),
  published: (cursor?: string, kind?: CatalogKind) =>
    apiClient.get<S['UnifiedVersionPage']>(
      `${admin}/published?${query(cursor, kind)}`,
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
  distributions: (cursor?: string, kind?: CatalogKind) =>
    apiClient.get<S['UnifiedDistributionPage']>(
      `${admin}/distributions?${query(cursor, kind)}`,
      { cache: 'no-store' },
    ),
  distribution: (d: string): ReadResult<CatalogDistribution> =>
    readResource<CatalogDistribution>(`${admin}/distributions/${id(d)}`),
  assignments: (cursor?: string) =>
    apiClient.get<S['UnifiedAssignmentPage']>(
      `${admin}/assignments?${query(cursor)}`,
      { cache: 'no-store' },
    ),
  assign: (body: S['UnifiedAssignmentInput'], key: string) =>
    apiClient.post<CatalogAssignment>(`${admin}/assignments`, body, {
      headers: headers('"0"', key),
    }),
  removeAssignment: (assignmentId: string, etag: string, key: string) =>
    apiClient.delete<CatalogAssignment>(
      `${admin}/assignments/${id(assignmentId)}`,
      { headers: headers(etag, key) },
    ),
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
