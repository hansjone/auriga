import { apiClient } from '../api';
import type { components } from './opsSchema';
export type OpsAlert = components['schemas']['OpsAlert'];
export type OpsChannel = components['schemas']['OpsChannel'];
export type OpsChannelInput = components['schemas']['OpsChannelInput'];
const root = '/api/ops/v1/admin';
const id = encodeURIComponent;
export const opsApi = {
  alerts: (signal?: AbortSignal) =>
    apiClient.get<{ items: OpsAlert[]; limit: number }>(`${root}/alerts`, {
      signal,
    }),
  action: (
    key: string,
    action: 'acknowledge' | 'silence' | 'unsilence',
    signal?: AbortSignal,
  ) =>
    apiClient.post<void>(`${root}/alerts/${id(key)}/${action}`, undefined, {
      signal,
    }),
  channels: (signal?: AbortSignal) =>
    apiClient.get<{ items: OpsChannel[] }>(`${root}/notification-channels`, {
      signal,
    }),
  create: (body: OpsChannelInput, signal?: AbortSignal) =>
    apiClient.post<{ id: string }>(`${root}/notification-channels`, body, {
      signal,
    }),
  save: (key: string, body: OpsChannelInput, signal?: AbortSignal) =>
    apiClient.request<void>(`${root}/notification-channels/${id(key)}`, {
      method: 'PUT',
      body: JSON.stringify(body),
      headers: { 'Content-Type': 'application/json' },
      signal,
    }),
  remove: (key: string, signal?: AbortSignal) =>
    apiClient.request<void>(`${root}/notification-channels/${id(key)}`, {
      method: 'DELETE',
      signal,
    }),
  test: (key: string, signal?: AbortSignal) =>
    apiClient.post<void>(
      `${root}/notification-channels/${id(key)}/test`,
      undefined,
      { signal },
    ),
};
