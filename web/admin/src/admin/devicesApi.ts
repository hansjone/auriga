import { isApiError, type IAMComponents, type IAMPaths } from '@auriga/shared';
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
  async revoke(id: string, previous?: AdminDevice): Promise<AdminDevice> {
    type PartialResult =
      IAMPaths['/admin/devices/{deviceId}']['delete']['responses']['202']['content']['application/json'];
    let result: PartialResult | undefined;
    let acknowledged = false;
    try {
      result = await apiClient.delete<PartialResult | undefined>(
        `${base}/${encodeURIComponent(id)}`,
      );
      acknowledged = true;
    } catch (error) {
      if (!isApiError(error) || error.status < 500) throw error;
    }
    let device: AdminDevice;
    try {
      device = await this.get(id);
    } catch (error) {
      if (
        !acknowledged ||
        !previous ||
        (isApiError(error) && [401, 403].includes(error.status))
      )
        throw error;
      return {
        ...previous,
        status: 'revoked',
        online: 'unknown',
        disconnectStatus: 'pending',
      };
    }
    if (device.status !== 'revoked')
      throw new Error('Revocation not confirmed');
    return {
      ...device,
      disconnectStatus:
        result?.disconnectStatus === 'pending' ||
        device.disconnectStatus === 'pending' ||
        device.online !== 'offline'
          ? 'pending'
          : 'confirmed',
    };
  },
};
