import test from 'node:test';
import assert from 'node:assert/strict';
import { createNativeBrowser } from '../../src/shared-browser/native.js';
import { memberWorkspace } from '../../src/shared/member-files.js';

const principal = {
  kind: 'auriga-shared-member',
  tenantId: '00000000-0000-0000-0000-000000000001',
  memberId: '01a1920c-e09c-7115-a780-dd61a2bfcb8e',
  placementId: '01a1fc08-dbb7-7507-888b-6e4308caec90',
  physicalInstanceId: '01a1fc08-dc04-7402-b04e-e64382a4e320',
  epoch: 1,
};
const request = (endpoint, args) => ({
  channel: '/api',
  endpoint,
  payload: { args },
});

function fixture(ids = []) {
  const state = { ids, created: [] };
  const host = {
    refresh: async () => [...state.ids],
    authorize: async () => {},
    create: async (_peer, input) => {
      state.created.push(input);
      return { sessionId: 'b0000000-0000-4000-8000-000000000001' };
    },
  };
  const native = createNativeBrowser({
    host,
    gateway: {
      dispatchRpc: () => assert.fail('fixed workspace must not reach native'),
    },
    remoteAccess: { invalidate() {} },
    workspaceRecheckMs: 5,
  });
  return { native, state, peer: { principal } };
}

test('a shared member gets exactly one fixed workspace at their own directory', async () => {
  const f = fixture(['a0000000-0000-4000-8000-000000000001']);
  const result = await f.native.call(
    f.peer,
    request('workspace/initializeDefault', {}),
    new AbortController().signal,
  );
  assert.equal(result.ok, true);
  assert.equal(result.value.workspaceId, `auriga-member-${principal.memberId}`);
  assert.equal(result.value.path, memberWorkspace(principal));
  assert.deepEqual(result.value.sessionIds, [
    'a0000000-0000-4000-8000-000000000001',
  ]);
});

test('workspace follow streams the fixed baseline then replaces on session change', async () => {
  const f = fixture([]);
  const abort = new AbortController();
  const { source } = await f.native.open(
    f.peer,
    request('workspace/follow', {}),
    abort.signal,
  );
  const first = await source.next();
  assert.equal(first.value.type, 'baseline');
  assert.equal(first.value.value.items.length, 1);
  assert.deepEqual(first.value.value.items[0].sessionIds, []);
  f.state.ids = ['a0000000-0000-4000-8000-000000000002'];
  const second = await source.next();
  assert.equal(second.value.type, 'replace');
  assert.deepEqual(second.value.value.items[0].sessionIds, f.state.ids);
  abort.abort();
});

test('session create accepts only the member fixed workspace and never a cwd', async () => {
  const f = fixture([]);
  const signal = new AbortController().signal;
  const ok = await f.native.call(
    f.peer,
    request('session/create', {
      request: { workspaceId: `auriga-member-${principal.memberId}` },
    }),
    signal,
  );
  assert.equal(ok.ok, true);
  await assert.rejects(
    f.native.call(
      f.peer,
      request('session/create', { request: { workspaceId: 'other' } }),
      signal,
    ),
    (error) => error.code === 'shared.binding_mismatch',
  );
  await assert.rejects(
    f.native.call(
      f.peer,
      request('session/create', { request: { cwd: '/etc' } }),
      signal,
    ),
    (error) => error.code === 'common.validation_failed',
  );
  for (const endpoint of [
    'workspace/create',
    'workspace/rename',
    'workspace/delete',
  ])
    await assert.rejects(
      f.native.call(f.peer, request(endpoint, { request: {} }), signal),
      (error) => error.code === 'shared.revoked',
    );
});
