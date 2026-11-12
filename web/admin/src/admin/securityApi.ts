import type { OrgComponents } from '@auriga/shared';
import { apiClient, readResource } from '../api';
export type SecurityEntries = OrgComponents['schemas']['AdminSecNetEntries'];
export type EgressStatus = OrgComponents['schemas']['AdminSecNetEgress'];
export type EgressValue = OrgComponents['schemas']['EgressPolicy'];
export type Policy = OrgComponents['schemas']['Policy'];
const base = '/api/org/v1/admin';
export const securityApi = {
  entries: () =>
    apiClient.get<SecurityEntries>(`${base}/security/entries`, {
      cache: 'no-store',
    }),
  status: () =>
    apiClient.get<EgressStatus>(`${base}/security/egress`, {
      cache: 'no-store',
    }),
  egress: (): Promise<{ data: Policy; etag: string | null }> =>
    readResource<Policy>(`${base}/policies/egress`),
  save: (value: EgressValue, etag: string, signal: AbortSignal) =>
    apiClient.put<Policy>(
      `${base}/policies/egress`,
      { value },
      { headers: { 'If-Match': etag }, signal },
    ),
};
