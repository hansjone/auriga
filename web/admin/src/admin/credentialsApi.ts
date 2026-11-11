import type { IAMComponents } from '@auriga/shared';
import { apiClient } from '../api';
export type Credential = IAMComponents['schemas']['CredentialMetadata'];
type List = IAMComponents['schemas']['CredentialList'];
const base = '/api/iam/v1/admin/credentials';
export const credentialsApi = {
  list(type: string, status: string, expiringSoon: boolean) {
    const query = new URLSearchParams();
    if (type) query.set('type', type);
    if (status) query.set('status', status);
    if (expiringSoon) query.set('expiringSoon', 'true');
    return apiClient.get<List>(`${base}?${query}`);
  },
  revoke(instanceId: string, version: number) {
    const body: IAMComponents['schemas']['CredentialRevoke'] = {
      version,
      confirm: true,
    };
    return apiClient.post<Credential>(
      `${base}/${encodeURIComponent(instanceId)}/revoke`,
      body,
    );
  },
};
