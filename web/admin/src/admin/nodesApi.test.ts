import { expect, it, vi } from 'vitest';
import { nodesApi } from './nodesApi';
import { apiClient } from '../api';
vi.mock('../api', () => ({
  apiClient: { get: vi.fn(), post: vi.fn(), delete: vi.fn() },
}));
it('uses contract endpoints, encodes IDs and never requests forced retirement', async () => {
  await nodesApi.tokens();
  expect(apiClient.get).toHaveBeenCalledWith(
    '/api/workspace/v1/admin/join-tokens',
    { cache: 'no-store' },
  );
  await nodesApi.create({ ttlMinutes: 60 });
  expect(apiClient.post).toHaveBeenCalledWith(
    '/api/workspace/v1/admin/join-tokens',
    { ttlMinutes: 60 },
  );
  await nodesApi.revokeToken('a/b?c');
  expect(apiClient.delete).toHaveBeenCalledWith(
    '/api/workspace/v1/admin/join-tokens/a%2Fb%3Fc',
  );
  await nodesApi.retire('a/b?c');
  expect(apiClient.delete).toHaveBeenLastCalledWith(
    '/api/workspace/v1/admin/nodes/a%2Fb%3Fc',
  );
});
