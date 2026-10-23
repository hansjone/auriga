import type { OrgComponents } from '@auriga/shared';
import { apiClient } from '../api';
export type Quota = OrgComponents['schemas']['Quota'];
export type QuotaTarget =
  { scope: 'default' } | { scope: 'group' | 'member'; id: string };
export function quotaPath(target: QuotaTarget) {
  const base = '/api/org/v1/admin';
  return target.scope === 'default'
    ? `${base}/quotas/default`
    : `${base}/${target.scope === 'group' ? 'groups' : 'members'}/${encodeURIComponent(target.id)}/quota`;
}
export const quotaApi = {
  get: (target: QuotaTarget) => apiClient.get<Quota>(quotaPath(target)),
  put: (target: QuotaTarget, body: Quota) =>
    apiClient.put<Quota>(quotaPath(target), body),
  delete: (target: Exclude<QuotaTarget, { scope: 'default' }>) =>
    apiClient.delete<void>(quotaPath(target)),
  effective: (accountId: string) =>
    apiClient.get<Quota>(
      `/api/org/v1/admin/members/${encodeURIComponent(accountId)}/quota/effective`,
    ),
};
