import type { InjectionKey } from 'vue';
import type { WorkspaceComponents } from '@auriga/shared';
import { apiClient, readResource } from '../../api';
export type Placement = WorkspaceComponents['schemas']['DshPlacement'];
export type Switch = WorkspaceComponents['schemas']['DshPlacementSwitch'];
export type SwitchRequest =
  WorkspaceComponents['schemas']['DshPlacementSwitchRequest'];
type Schemas = WorkspaceComponents['schemas'];
export type Plan = Schemas['DshRebuildPlan'];
export type PlanRequest = Schemas['DshRebuildPlanRequest'];
export type PlanProgress = Schemas['DshRebuildProgress'];
export type Capacity = Schemas['DshCapacitySnapshot'];
export function intentKey() {
  return Array.from(
    globalThis.crypto.getRandomValues(new Uint8Array(16)),
    (byte) => byte.toString(16).padStart(2, '0'),
  ).join('');
}
export async function readTagged<T>(path: string, signal: AbortSignal) {
  signal.throwIfAborted();
  const value = await readResource<T>(path);
  signal.throwIfAborted();
  if (!value.etag || !/^"[^"]+"$/.test(value.etag))
    throw new Error('shared.missing_etag');
  return { data: value.data, etag: value.etag };
}
export interface MemberPlacementSnapshot {
  placement?: Placement;
  etag: string;
  dedicatedEntitled: boolean;
}
export interface PlacementPort {
  readMember(
    memberId: string,
    signal: AbortSignal,
  ): Promise<MemberPlacementSnapshot>;
  switchMember(
    memberId: string,
    body: SwitchRequest,
    etag: string,
    key: string,
    signal: AbortSignal,
  ): Promise<Switch>;
  progress(operationId: string, signal: AbortSignal): Promise<Switch>;
  entitlement(
    memberId: string,
    granted: boolean,
    reason: string,
    etag: string,
    key: string,
    signal: AbortSignal,
  ): Promise<unknown>;
  lookup(memberId: string, key: string, signal: AbortSignal): Promise<Switch>;
}
const base = '/api/workspace/v1/admin';
export const placementPortKey: InjectionKey<PlacementPort> =
  Symbol('placement-port');
export const placementPort: PlacementPort = {
  readMember: async (id, signal) => {
    const { data, etag } = await readTagged<Schemas['DshMemberPlacement']>(
      `${base}/members/${encodeURIComponent(id)}/placement`,
      signal,
    );
    if (
      data.memberId !== id ||
      data.entitlement.memberId !== id ||
      (data.placement && data.placement.memberId !== id)
    )
      throw new Error('shared.binding_mismatch');
    return {
      placement: data.placement,
      etag,
      dedicatedEntitled: data.entitlement.granted,
    };
  },
  entitlement: (id, granted, reason, etag, key, signal) =>
    apiClient.put(
      `${base}/members/${encodeURIComponent(id)}/dedicated-entitlement`,
      { granted, reason },
      { signal, headers: { 'If-Match': etag, 'Idempotency-Key': key } },
    ),
  lookup: (id, key, signal) =>
    apiClient.get(
      `${base}/members/${encodeURIComponent(id)}/placement-switches/lookup`,
      { signal, cache: 'no-store', headers: { 'Idempotency-Key': key } },
    ),
  switchMember: (id, body, etag, key, signal) =>
    apiClient.post(
      `${base}/members/${encodeURIComponent(id)}/placement-switches`,
      body,
      { signal, headers: { 'If-Match': etag, 'Idempotency-Key': key } },
    ),
  progress: (id, signal) =>
    apiClient.get(`${base}/placement-switches/${encodeURIComponent(id)}`, {
      signal,
      cache: 'no-store',
    }),
};
const plans = `${base}/placement-rebuild-plans`;
const headers = (key: string, etag?: string) => ({
  'Idempotency-Key': key,
  ...(etag ? { 'If-Match': etag } : {}),
});
export const placementAdminApi = {
  plan: (id: string, signal: AbortSignal) =>
    readTagged<Plan>(`${plans}/${encodeURIComponent(id)}`, signal),
  create: (body: PlanRequest, key: string, signal: AbortSignal) =>
    apiClient.post<Plan>(plans, body, { signal, headers: headers(key) }),
  update: (
    id: string,
    body: PlanRequest,
    etag: string,
    key: string,
    signal: AbortSignal,
  ) =>
    apiClient.put<Plan>(`${plans}/${encodeURIComponent(id)}`, body, {
      signal,
      headers: headers(key, etag),
    }),
  start: (id: string, etag: string, key: string, signal: AbortSignal) =>
    apiClient.post<PlanProgress>(
      `${plans}/${encodeURIComponent(id)}/start`,
      {
        exportConfirmed: true,
        sessionPolicy: 'discard',
        filePolicy: 'archive-and-reset',
      },
      { signal, headers: headers(key, etag) },
    ),
  progress: (id: string, signal: AbortSignal) =>
    apiClient.get<PlanProgress>(`${plans}/${encodeURIComponent(id)}/progress`, {
      signal,
      cache: 'no-store',
    }),
  capacity: (signal: AbortSignal) =>
    apiClient.get<Capacity>(`${base}/placement-capacity`, {
      signal,
      cache: 'no-store',
    }),
};
