import type { IAMComponents } from '@auriga/shared';
import { apiClient } from '../api';
export type AdminDevice = IAMComponents['schemas']['AdminDevicesDevice'];
const base = '/api/iam/v1/admin/devices';
export const devicesApi = {
  list(member?: string, online?: string) {
    const q = new URLSearchParams();
    if (member) q.set('member', member);
    if (online) q.set('online', online);
    return apiClient.get<{ items: AdminDevice[] }>(`${base}?${q}`, {
      cache: 'no-store',
    });
  },
  get(id: string) {
    return apiClient.get<AdminDevice>(`${base}/${encodeURIComponent(id)}`, {
      cache: 'no-store',
    });
  },
  async revoke(id: string) {
    await apiClient.delete<void>(`${base}/${encodeURIComponent(id)}`);
    const device = await this.get(id);
    if (device.status !== 'revoked')
      throw new Error('Revocation not confirmed');
    return device;
  },
};
