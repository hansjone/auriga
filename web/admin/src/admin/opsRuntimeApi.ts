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
    if (signal.aborted || !ticket.csrf)
      throw new Error('Download authorization unavailable');
    const frame = document.createElement('iframe');
    frame.hidden = true;
    frame.name = 'ops-download-' + globalThis.crypto.randomUUID();
    frame.title = 'Database archive download';
    document.body.append(frame);
    const form = document.createElement('form');
    form.method = 'POST';
    form.action = `${base}/backups/${encodeURIComponent(id)}/download`;
    form.target = frame.name;
    form.hidden = true;
    for (const [name, value] of Object.entries({
      ticket: ticket.token,
      csrf: ticket.csrf,
    })) {
      const field = document.createElement('input');
      field.type = 'hidden';
      field.name = name;
      field.value = value;
      form.append(field);
    }
    document.body.append(form);
    try {
      form.submit();
    } finally {
      form.remove();
    }
    // Attachments are owned by the browser's download manager. Keep its frame
    // for this page's lifetime; removing it early can abort slow downloads.
  },
  remove: (id: string, signal: AbortSignal) =>
    apiClient.delete(`${base}/backups/${encodeURIComponent(id)}`, { signal }),
};
