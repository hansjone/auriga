import type { WorkspacePaths, WorkspaceComponents } from '@auriga/shared';
import { apiClient } from '../api';

export type JoinToken = WorkspaceComponents['schemas']['JoinToken'];
type JoinPath = WorkspacePaths['/admin/join-tokens'];
export type JoinInput = NonNullable<
  JoinPath['post']['requestBody']
>['content']['application/json'];
export type JoinMaterial =
  JoinPath['post']['responses']['201']['content']['application/json'];
const base = '/api/workspace/v1/admin';
export const nodesApi = {
  tokens: () =>
    apiClient.get<
      JoinPath['get']['responses']['200']['content']['application/json']
    >(`${base}/join-tokens`, { cache: 'no-store' }),
  create: (body: JoinInput) =>
    apiClient.post<JoinMaterial>(`${base}/join-tokens`, body),
  revokeToken: (id: string) =>
    apiClient.delete<void>(`${base}/join-tokens/${encodeURIComponent(id)}`),
  retire: (id: string) =>
    apiClient.delete<void>(`${base}/nodes/${encodeURIComponent(id)}`),
};
