import type { components } from '../../../shared/src/generated/resource';
import { apiClient, readResource } from '../api';

export type ResourcePackage = components['schemas']['Package'];
export type ResourceVersion = components['schemas']['Version'];
export type PackageInput = components['schemas']['PackageInput'];
export type PackagePatch = components['schemas']['PackagePatch'];
export type PackagePage = components['schemas']['PackagePage'];
export type VersionPage = components['schemas']['VersionPage'];
const base = '/api/resource/v1/packages';
const id = encodeURIComponent;
function query(cursor?: string) {
  const params = new URLSearchParams({ limit: '50' });
  if (cursor) params.set('cursor', cursor);
  return params;
}
export const resourceApi = {
  list: (cursor?: string) =>
    apiClient.get<PackagePage>(`${base}?${query(cursor)}`, {
      cache: 'no-store',
    }),
  get: (packageId: string) =>
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
