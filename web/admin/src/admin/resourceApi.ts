import type { components } from '../../../shared/src/generated/resource';
import { apiClient, readResource } from '../api';

export type ResourcePackage = components['schemas']['Package'];
export type ResourceVersion = components['schemas']['Version'];
export type PackageInput = components['schemas']['PackageInput'];
export type PackagePatch = components['schemas']['PackagePatch'];
export type PackagePage = components['schemas']['PackagePage'];
export type VersionPage = components['schemas']['VersionPage'];
export type GovernanceDetail = components['schemas']['MarketAdminDetail'];
export type ResourceDistribution = components['schemas']['Distribution'];
export type DistributionInput = components['schemas']['DistributionInput'];
export type DistributionPage =
  components['schemas']['MarketAdminDistributionPage'];
export type ResourceReview = components['schemas']['Review'];
export type ReviewDecision = components['schemas']['ReviewDecision'];
export type AdminPackagePatch = components['schemas']['AdminPackagePatch'];
export type UploadMetadata = components['schemas']['UploadMetadata'];
const base = '/api/resource/v1/packages';
const id = encodeURIComponent;
const versionPath = (packageId: string, versionId: string) =>
  `${base}/${id(packageId)}/versions/${id(versionId)}`;
const headers = (etag: string, key: string) => ({
  'If-Match': etag,
  'Idempotency-Key': key,
});
function query(cursor?: string) {
  const params = new URLSearchParams({ limit: '50' });
  if (cursor) params.set('cursor', cursor);
  return params;
}
export const resourceApi = {
  draft: (
    packageId: string,
    versionId: string,
    reason: string,
    etag: string,
    key: string,
  ) =>
    apiClient.post<ResourceVersion>(
      `${versionPath(packageId, versionId)}/drafts`,
      { reason },
      { headers: headers(etag, key) },
    ),
  personalSign: (
    packageId: string,
    versionId: string,
    reason: string,
    etag: string,
    key: string,
  ) =>
    apiClient.post<ResourceVersion>(
      `${versionPath(packageId, versionId)}/personal-signatures`,
      { reason },
      { headers: headers(etag, key) },
    ),
  governanceDetail: (
    phase: 'review' | 'published',
    packageId: string,
    versionId: string,
  ) =>
    apiClient.get<GovernanceDetail>(
      `/api/resource/v1/admin/${phase}/packages/${id(packageId)}/versions/${id(versionId)}`,
      { cache: 'no-store' },
    ),
  decide: (reviewId: string, body: ReviewDecision, etag: string, key: string) =>
    apiClient.post<ResourceReview>(
      `/api/resource/v1/admin/reviews/${id(reviewId)}/decisions`,
      body,
      { headers: headers(etag, key) },
    ),
  publish: (
    packageId: string,
    versionId: string,
    reason: string,
    etag: string,
    key: string,
  ) =>
    apiClient.post<ResourceVersion>(
      `/api/resource/v1/admin/packages/${id(packageId)}/versions/${id(versionId)}/publications`,
      { reason },
      { headers: headers(etag, key) },
    ),
  withdraw: (
    packageId: string,
    versionId: string,
    mode: 'withdrawn' | 'malicious',
    reason: string,
    etag: string,
    key: string,
  ) =>
    apiClient.post<ResourceVersion>(
      `/api/resource/v1/admin/packages/${id(packageId)}/versions/${id(versionId)}/withdrawals`,
      { mode, reason },
      { headers: headers(etag, key) },
    ),
  govern: (packageId: string, body: AdminPackagePatch, etag: string) =>
    apiClient.patch<ResourcePackage>(
      `/api/resource/v1/admin/packages/${id(packageId)}`,
      body,
      { headers: { 'If-Match': etag } },
    ),
  upload: (
    packageId: string,
    metadata: UploadMetadata,
    archive: File,
    etag: string,
    key: string,
  ) => {
    const form = new FormData();
    form.append(
      'metadata',
      new Blob([JSON.stringify(metadata)], { type: 'application/json' }),
    );
    form.append('archive', archive);
    return apiClient.post<ResourceVersion>(
      `${base}/${id(packageId)}/versions`,
      form,
      { headers: headers(etag, key) },
    );
  },
  submit: (
    packageId: string,
    versionId: string,
    reason: string,
    etag: string,
    key: string,
  ) =>
    apiClient.post<ResourceReview>(
      `${versionPath(packageId, versionId)}/submissions`,
      { reason },
      { headers: headers(etag, key) },
    ),
  distributions: (cursor?: string) =>
    apiClient.get<DistributionPage>(
      `/api/resource/v1/admin/distributions?${query(cursor)}`,
      { cache: 'no-store' },
    ),
  distribute: (body: DistributionInput, key: string) =>
    apiClient.post<ResourceDistribution>(
      '/api/resource/v1/distributions',
      body,
      { headers: { 'Idempotency-Key': key } },
    ),
  governance: (phase: 'review' | 'published', cursor?: string) =>
    apiClient.get<VersionPage>(
      `/api/resource/v1/admin/${phase}?${query(cursor)}`,
      {
        cache: 'no-store',
      },
    ),
  list: (cursor?: string) =>
    apiClient.get<PackagePage>(`${base}?${query(cursor)}`, {
      cache: 'no-store',
    }),
  get: (
    packageId: string,
  ): Promise<{ data: ResourcePackage; etag: string | null }> =>
    readResource<ResourcePackage>(`${base}/${id(packageId)}`),
  versions: (packageId: string, cursor?: string) =>
    apiClient.get<VersionPage>(
      `${base}/${id(packageId)}/versions?${query(cursor)}`,
      { cache: 'no-store' },
    ),
  version: (packageId: string, versionId: string) =>
    apiClient.get<ResourceVersion>(
      `${base}/${id(packageId)}/versions/${id(versionId)}`,
      { cache: 'no-store' },
    ),
  create: (body: PackageInput, key: string) =>
    apiClient.post<ResourcePackage>(base, body, {
      headers: { 'Idempotency-Key': key },
    }),
  patch: (packageId: string, body: PackagePatch, etag: string) =>
    apiClient.patch<ResourcePackage>(`${base}/${id(packageId)}`, body, {
      headers: { 'If-Match': etag, 'Content-Type': 'application/json' },
    }),
};
export const strongEtag = (value: string | null): value is string =>
  value !== null && /^"[\x21\x23-\x7e\x80-\xff]*"$/.test(value);
