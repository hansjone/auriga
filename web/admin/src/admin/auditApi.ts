import { apiClient } from '../api';
import type { components, paths } from './audit.generated';
export type AuditEvent = components['schemas']['AuditEvent'];
export type AuditFilters = {
  from: string;
  to: string;
  actor: string;
  action: string;
  result: string;
  q: string;
};
const root = '/api/audit/v1/admin/events';
function query(filters: AuditFilters, cursor?: string) {
  const values = new URLSearchParams();
  for (const [key, value] of Object.entries(filters))
    if (value) values.set(key, value);
  if (cursor) values.set('cursor', cursor);
  return values.toString();
}
export const auditApi = {
  list: (filters: AuditFilters, cursor?: string) =>
    apiClient.get<components['schemas']['AuditPage']>(
      `${root}?${query(filters, cursor)}`,
    ),
  detail: (id: string) =>
    apiClient.get<AuditEvent>(`${root}/${encodeURIComponent(id)}`),
  export: (filters: AuditFilters) =>
    apiClient.get<
      paths['/admin/events/export']['get']['responses'][200]['content']['application/json']
    >(`${root}/export?${query(filters)}`),
};
