import type { OrgComponents, OrgPaths } from '@auriga/shared';
import { apiClient, readResource } from '../api';

export type Tenant = OrgComponents['schemas']['Tenant'];
export type Policy = OrgComponents['schemas']['Policy'];
export type WorkspacePolicy = OrgComponents['schemas']['WorkspacePolicy'];
// Defaults become required in generated TS, although the schema/service accept
// omitted fields. Derive the sparse editor value from the generated schema.
export type WorkspaceValue = Partial<WorkspacePolicy>;
export type TenantPatch = Pick<
  OrgPaths['/admin/tenant']['patch']['requestBody']['content']['application/merge-patch+json'],
  'displayName' | 'locale'
>;
type PolicyPut =
  OrgPaths['/admin/policies/{key}']['put']['requestBody']['content']['application/json'];
const base = '/api/org/v1';
export const settingsApi = {
  tenant: () => apiClient.get<Tenant>(`${base}/tenant`, { cache: 'no-store' }),
  patchTenant: (body: TenantPatch) =>
    apiClient.patch<Tenant>(`${base}/admin/tenant`, body, {
      headers: { 'Content-Type': 'application/merge-patch+json' },
    }),
  workspace: () => readResource<Policy>(`${base}/admin/policies/workspace`),
  putWorkspace: (value: WorkspaceValue, etag: string) =>
    apiClient.put<Policy>(
      `${base}/admin/policies/workspace`,
      { value } as PolicyPut,
      {
        headers: { 'If-Match': etag },
      },
    ),
};

// The current service parses a quoted signed Java long, not an arbitrary opaque tag.
export function policyEtag(value: string | null): value is string {
  if (!value || !/^"[0-9]+"$/.test(value)) return false;
  return BigInt(value.slice(1, -1)) <= 9223372036854775807n;
}
