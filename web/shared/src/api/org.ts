import type { components, paths } from '../generated/org';
import type { ApiClient } from './client';

export type Member = components['schemas']['Member'];
export type MemberStatus = components['schemas']['MemberStatus'];
export type MemberType = components['schemas']['MemberType'];

type MembersResponse =
  paths['/admin/members']['get']['responses']['200']['content']['application/json'];

type InvitationPreview =
  paths['/invitations/preview/{code}']['get']['responses']['200']['content']['application/json'];

const ORG = '/api/org/v1';

export type ListMembersQuery = {
  q?: string;
  status?: MemberStatus;
  memberType?: MemberType;
  groupId?: string;
  limit?: number;
  cursor?: string;
};

export function createOrgApi(client: ApiClient) {
  return {
    listMembers(query: ListMembersQuery = {}) {
      const params = new URLSearchParams();
      if (query.q) params.set('q', query.q);
      if (query.status) params.set('status', query.status);
      if (query.memberType) params.set('memberType', query.memberType);
      if (query.groupId) params.set('groupId', query.groupId);
      if (query.limit !== undefined) params.set('limit', String(query.limit));
      if (query.cursor) params.set('cursor', query.cursor);
      const qs = params.toString();
      return client.get<MembersResponse>(
        `${ORG}/admin/members${qs ? `?${qs}` : ''}`,
      );
    },
    previewInvitation(code: string) {
      return client.get<InvitationPreview>(
        `${ORG}/invitations/preview/${encodeURIComponent(code)}`,
      );
    },
  };
}

export type OrgApi = ReturnType<typeof createOrgApi>;
