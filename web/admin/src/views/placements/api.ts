import type { InjectionKey } from 'vue';
import type { WorkspaceComponents } from '@auriga/shared';
import { apiClient } from '../../api';
export type Placement = WorkspaceComponents['schemas']['DshPlacement'];
export type Switch = WorkspaceComponents['schemas']['DshPlacementSwitch'];
export type SwitchRequest =
  WorkspaceComponents['schemas']['DshPlacementSwitchRequest'];
// Local integration port. K/W must supply an authoritative admin read contract;
// never synthesize an ETag from revision or infer entitlement from member type.
export interface MemberPlacementSnapshot {
  placement: Placement;
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
}
const base = '/api/workspace/v1/admin';
export const placementPortKey: InjectionKey<PlacementPort> =
  Symbol('placement-port');
export const placementPort: PlacementPort = {
  readMember: () =>
    Promise.reject(new Error('placement.admin_read_unavailable')),
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
