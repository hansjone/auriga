import type { OrgComponents, OrgPaths } from '@auriga/shared';
import { apiClient } from '../api';
export type Role = OrgComponents['schemas']['Role'];
export type RoleInput = OrgComponents['schemas']['RoleInput'];
export type Permission = OrgComponents['schemas']['Permission'];
export type RoleBinding = OrgComponents['schemas']['RoleBinding'];
export type BindingSubject = OrgComponents['schemas']['BindingSubject'];
type RoleList =
  OrgPaths['/admin/roles']['get']['responses']['200']['content']['application/json'];
type PermissionList =
  OrgPaths['/admin/permissions']['get']['responses']['200']['content']['application/json'];
type BindingList =
  OrgPaths['/admin/role-bindings']['get']['responses']['200']['content']['application/json'];
type BindingInput =
  OrgPaths['/admin/role-bindings']['post']['requestBody']['content']['application/json'];
const base = '/api/org/v1/admin';
const id = encodeURIComponent;
export const rolesApi = {
  list: () => apiClient.get<RoleList>(`${base}/roles`),
  permissions: () => apiClient.get<PermissionList>(`${base}/permissions`),
  get: (roleId: string) => apiClient.get<Role>(`${base}/roles/${id(roleId)}`),
  create: (body: RoleInput) => apiClient.post<Role>(`${base}/roles`, body),
  update: (roleId: string, body: RoleInput) =>
    apiClient.patch<Role>(`${base}/roles/${id(roleId)}`, body, {
      headers: { 'Content-Type': 'application/merge-patch+json' },
    }),
  delete: (roleId: string) =>
    apiClient.delete<void>(`${base}/roles/${id(roleId)}`),
  bindings(roleId: string, cursor?: string) {
    const query = new URLSearchParams({ roleId, limit: '50' });
    if (cursor) query.set('cursor', cursor);
    return apiClient.get<BindingList>(`${base}/role-bindings?${query}`);
  },
  grant: (body: BindingInput) =>
    apiClient.post<RoleBinding>(`${base}/role-bindings`, body),
  revoke: (bindingId: string) =>
    apiClient.delete<void>(`${base}/role-bindings/${id(bindingId)}`),
};
