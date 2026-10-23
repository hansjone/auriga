import type { OrgComponents, OrgPaths } from '@auriga/shared';
import { apiClient } from '../api';

export type Group = OrgComponents['schemas']['Group'];
export type GroupInput = OrgComponents['schemas']['GroupInput'];
export type Invitation = OrgComponents['schemas']['Invitation'];
export type InvitationInput = OrgComponents['schemas']['InvitationInput'];
export type InvitationStatus = Invitation['status'];
type GroupList =
  OrgPaths['/admin/groups']['get']['responses']['200']['content']['application/json'];
type MemberList =
  OrgPaths['/admin/groups/{groupId}/members']['get']['responses']['200']['content']['application/json'];
type InvitationList =
  OrgPaths['/admin/invitations']['get']['responses']['200']['content']['application/json'];
type CreatedInvitation =
  OrgPaths['/admin/invitations']['post']['responses']['201']['content']['application/json'];
const base = '/api/org/v1/admin';
const id = encodeURIComponent;
function page(cursor?: string) {
  const query = new URLSearchParams({ limit: '50' });
  if (cursor) query.set('cursor', cursor);
  return query;
}
export const orgAdminApi = {
  invitations(status?: InvitationStatus, cursor?: string) {
    const query = page(cursor);
    if (status) query.set('status', status);
    return apiClient.get<InvitationList>(`${base}/invitations?${query}`);
  },
  createInvitation: (body: InvitationInput) =>
    apiClient.post<CreatedInvitation>(`${base}/invitations`, body),
  revokeInvitation: (invitationId: string) =>
    apiClient.delete<void>(`${base}/invitations/${id(invitationId)}`),
  groups: () => apiClient.get<GroupList>(`${base}/groups`),
  group: (groupId: string) =>
    apiClient.get<Group>(`${base}/groups/${id(groupId)}`),
  createGroup: (body: GroupInput) =>
    apiClient.post<Group>(`${base}/groups`, body),
  updateGroup: (groupId: string, body: GroupInput) =>
    apiClient.patch<Group>(`${base}/groups/${id(groupId)}`, body, {
      headers: { 'Content-Type': 'application/merge-patch+json' },
    }),
  deleteGroup: (groupId: string) =>
    apiClient.delete<void>(`${base}/groups/${id(groupId)}`),
  groupMembers: (groupId: string, cursor?: string) =>
    apiClient.get<MemberList>(
      `${base}/groups/${id(groupId)}/members?${page(cursor)}`,
    ),
  addMember: (groupId: string, accountId: string) =>
    apiClient.put<void>(
      `${base}/groups/${id(groupId)}/members/${id(accountId)}`,
    ),
  removeMember: (groupId: string, accountId: string) =>
    apiClient.delete<void>(
      `${base}/groups/${id(groupId)}/members/${id(accountId)}`,
    ),
};
