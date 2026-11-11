import { createApiClient, readAccessMode } from '@auriga/shared';
import { apiClient } from '../api';
import type { components } from './runtimeContract';
type S = components['schemas'];
export type OpsUpgrades = S['OpsUpgradeStatus'];
export type OpsBackups = S['OpsBackupStatus'];
export type OpsPlan = S['OpsBackupPlanInput'];
const base = '/api/workspace/v1/admin/ops';
export const opsRuntimeApi = {
  upgrades: () =>
    apiClient.get<OpsUpgrades>(`${base}/upgrades`, { cache: 'no-store' }),
  register: (targetId: string, key: string, signal: AbortSignal) =>
    apiClient.post(
      `${base}/upgrades`,
      { targetId },
      { signal, headers: { 'Idempotency-Key': key } },
    ),
  backups: () =>
    apiClient.get<OpsBackups>(`${base}/backups`, { cache: 'no-store' }),
  plan: (value: OpsPlan, signal: AbortSignal) =>
    apiClient.put(`${base}/backups/plan`, value, { signal }),
  trigger: (key: string, signal: AbortSignal) =>
    apiClient.post(`${base}/backups`, undefined, {
      signal,
      headers: { 'Idempotency-Key': key },
    }),
  async download(id: string, signal: AbortSignal) {
    const ticket = await apiClient.post<S['OpsBackupTicket']>(
      `${base}/backups/${encodeURIComponent(id)}/tickets`,
      undefined,
      { signal },
    );
    let archive: Blob | undefined;
    const reader = createApiClient({
      getAdminAccessMode: () => readAccessMode(),
      onUnauthenticated: () => apiClient.resetSession(),
      fetch: async (input, init) => {
        const response = await globalThis.fetch(input, init);
        if (
          response.ok &&
          response.headers
            .get('content-type')
            ?.includes('application/octet-stream')
        ) {
          archive = await response.blob();
          return new Response('null', {
            headers: { 'Content-Type': 'application/json' },
          });
        }
        return response;
      },
    });
    await reader.get(`${base}/backups/${encodeURIComponent(id)}/download`, {
      signal,
      cache: 'no-store',
      headers: { 'X-Auriga-Download': ticket.token },
    });
    if (!archive || signal.aborted) throw new Error('Archive unavailable');
    const url = URL.createObjectURL(archive);
    const link = document.createElement('a');
    link.href = url;
    link.download = `${id}.dump`;
    link.click();
    globalThis.setTimeout(() => URL.revokeObjectURL(url), 1000);
  },
};
