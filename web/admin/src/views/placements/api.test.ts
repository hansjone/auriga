import { beforeEach, expect, it, vi } from 'vitest';
import { placementPort, placementAdminApi as api } from './api';
const client = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  put: vi.fn(),
  readResource: vi.fn(),
}));
vi.mock('../../api', () => ({
  apiClient: client,
  readResource: client.readResource,
}));
const signal = new AbortController().signal;
const memberId = '11111111-1111-4111-8111-111111111111';
const planId = '22222222-2222-4222-8222-222222222222';
const base = '/api/workspace/v1/admin';
beforeEach(() => {
  vi.clearAllMocks();
});
it('reads the composite ETag without deriving it, including unassigned members', async () => {
  client.readResource.mockResolvedValue({
    data: { memberId, entitlement: { memberId, granted: true, revision: 4 } },
    etag: '"composite-unrelated-to-revision"',
  });
  expect(await placementPort.readMember(memberId, signal)).toEqual({
    placement: undefined,
    etag: '"composite-unrelated-to-revision"',
    dedicatedEntitled: true,
  });
  expect(client.readResource).toHaveBeenCalledWith(
    base + '/members/' + memberId + '/placement',
  );
  client.readResource.mockResolvedValue({
    data: { memberId, entitlement: { memberId: 'other', granted: true } },
    etag: '"tag"',
  });
  await expect(placementPort.readMember(memberId, signal)).rejects.toThrow(
    'binding',
  );
  client.readResource.mockResolvedValue({
    data: { memberId, entitlement: { memberId, granted: true } },
    etag: null,
  });
  await expect(placementPort.readMember(memberId, signal)).rejects.toThrow(
    'etag',
  );
});
it('uses header-only lookup and entitlement CAS', async () => {
  await placementPort.entitlement(
    memberId,
    true,
    'reason',
    '"etag"',
    'intent',
    signal,
  );
  expect(client.put).toHaveBeenCalledWith(
    base + '/members/' + memberId + '/dedicated-entitlement',
    { granted: true, reason: 'reason' },
    { signal, headers: { 'If-Match': '"etag"', 'Idempotency-Key': 'intent' } },
  );
  await placementPort.lookup(memberId, 'intent', signal);
  expect(client.get).toHaveBeenCalledWith(
    base + '/members/' + memberId + '/placement-switches/lookup',
    { signal, cache: 'no-store', headers: { 'Idempotency-Key': 'intent' } },
  );
});
it('wires plan creation, read, replacement, start, progress and capacity to frozen paths', async () => {
  const body = {
    reason: 'reason',
    members: [
      { memberId, legacyInstanceId: planId, targetMode: 'dedicated' as const },
    ],
  };
  await api.create(body, 'key', signal);
  expect(client.post).toHaveBeenCalledWith(
    base + '/placement-rebuild-plans',
    body,
    { signal, headers: { 'Idempotency-Key': 'key' } },
  );
  client.readResource.mockResolvedValue({ data: { planId }, etag: '"tag"' });
  await api.plan(planId, signal);
  await api.update(planId, body, '"tag"', 'key', signal);
  expect(client.put).toHaveBeenCalledWith(
    base + '/placement-rebuild-plans/' + planId,
    body,
    { signal, headers: { 'Idempotency-Key': 'key', 'If-Match': '"tag"' } },
  );
  await api.start(planId, '"tag"', 'key', signal);
  expect(client.post).toHaveBeenLastCalledWith(
    base + '/placement-rebuild-plans/' + planId + '/start',
    {
      exportConfirmed: true,
      sessionPolicy: 'discard',
      filePolicy: 'archive-and-reset',
    },
    { signal, headers: { 'Idempotency-Key': 'key', 'If-Match': '"tag"' } },
  );
  await api.progress(planId, signal);
  await api.capacity(signal);
  expect(client.get).toHaveBeenCalledWith(
    base + '/placement-rebuild-plans/' + planId + '/progress',
    { signal, cache: 'no-store' },
  );
  expect(client.get).toHaveBeenCalledWith(base + '/placement-capacity', {
    signal,
    cache: 'no-store',
  });
});
