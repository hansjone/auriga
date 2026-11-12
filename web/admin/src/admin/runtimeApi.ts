import type { WorkspacePaths } from '@auriga/shared';
import { apiClient } from '../api';
export type RuntimeCatalog =
  WorkspacePaths['/admin/runtimes']['get']['responses']['200']['content']['application/json'];
export const runtimeApi = {
  runtimes: () =>
    apiClient.get<RuntimeCatalog>('/api/workspace/v1/admin/runtimes', {
      cache: 'no-store',
    }),
};
