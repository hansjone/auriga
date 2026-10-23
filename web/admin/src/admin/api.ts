import type {
  OrgPaths,
  WorkspacePaths,
  WorkspaceComponents,
  Member,
} from '@auriga/shared';
import { apiClient } from '../api';

export type Node = WorkspaceComponents['schemas']['Node'];
export type Instance = WorkspaceComponents['schemas']['Instance'];
export type NodePatch =
  WorkspacePaths['/admin/nodes/{nodeId}']['patch']['requestBody']['content']['application/merge-patch+json'];
export type InstancePatch =
  WorkspacePaths['/admin/instances/{instanceId}']['patch']['requestBody']['content']['application/merge-patch+json'];
type Membership =
  OrgPaths['/me']['get']['responses']['200']['content']['application/json'];
type InstanceList =
  WorkspacePaths['/admin/instances']['get']['responses']['200']['content']['application/json'];
type NodeList =
  WorkspacePaths['/admin/nodes']['get']['responses']['200']['content']['application/json'];
const workspace = '/api/workspace/v1';
const org = '/api/org/v1';
const id = encodeURIComponent;
const mergePatch = { 'Content-Type': 'application/merge-patch+json' };

export const adminApi = {
  membership: () => apiClient.get<Membership>(`${org}/me`),
  nodes: () => apiClient.get<NodeList>(`${workspace}/admin/nodes`),
  node: (nodeId: string) =>
    apiClient.get<Node>(`${workspace}/admin/nodes/${id(nodeId)}`),
  updateNode: (nodeId: string, body: NodePatch) =>
    apiClient.patch<Node>(`${workspace}/admin/nodes/${id(nodeId)}`, body, {
      headers: mergePatch,
    }),
  drainNode: (nodeId: string) =>
    apiClient.post<Node>(`${workspace}/admin/nodes/${id(nodeId)}/drain`),
  instances: (cursor?: string) => {
    const query = new URLSearchParams({ limit: '50' });
    if (cursor) query.set('cursor', cursor);
    return apiClient.get<InstanceList>(`${workspace}/admin/instances?${query}`);
  },
  instance: (instanceId: string) =>
    apiClient.get<Instance>(`${workspace}/instances/${id(instanceId)}`),
  operateInstance: (instanceId: string, action: 'start' | 'stop' | 'restart') =>
    apiClient.post<Instance>(
      `${workspace}/instances/${id(instanceId)}/${action}`,
    ),
  updateInstance: (instance: Instance, body: InstancePatch) =>
    apiClient.patch<Instance>(
      `${workspace}/admin/instances/${id(instance.id)}`,
      body,
      {
        headers: { ...mergePatch, 'If-Match': `"${instance.generation}"` },
      },
    ),
  member: (accountId: string) =>
    apiClient.get<Member>(`${org}/admin/members/${id(accountId)}`),
  setMemberStatus: (accountId: string, action: 'enable' | 'disable') =>
    apiClient.post<Member>(`${org}/admin/members/${id(accountId)}/${action}`),
};
