import test from 'node:test';
import assert from 'node:assert/strict';
import {
  publicLogin,
  desktopMemberRole,
  ensureDesktopMemberRole,
} from './cloud-desk.http.mjs';

test('desktop invitations use the discovered built-in member role; missing permission fails closed', async () => {
  const role = {
    id: '019f0000-0000-7000-8000-000000000002',
    key: 'member',
    builtin: true,
    permissions: ['auriga.connect.collab.use'],
  };
  assert.equal(
    desktopMemberRole({ items: [{ ...role, key: 'admin' }, role] }),
    role.id,
  );
  assert.throws(() =>
    desktopMemberRole({ items: [{ ...role, key: 'admin' }] }),
  );
  assert.throws(() =>
    desktopMemberRole({ items: [{ ...role, builtin: false }] }),
  );
  assert.throws(() =>
    desktopMemberRole({ items: [{ ...role, permissions: [] }] }),
  );
  const accountId = '019f0000-0000-7000-8000-000000000001';
  const calls = [];
  let bound = false;
  const admin = {
    request: async (path, body) => {
      calls.push({ path, body });
      if (path.endsWith('/role-bindings') && body) {
        bound = true;
        return { id: 'actual-binding' };
      }
      return { items: bound ? [{ roleId: role.id }] : [] };
    },
  };
  await ensureDesktopMemberRole(admin, accountId, role.id);
  await ensureDesktopMemberRole(admin, accountId, role.id);
  assert.equal(calls.filter((c) => c.body).length, 1);
  assert.deepEqual(calls.find((c) => c.body).body, {
    roleId: role.id,
    subject: { type: 'member', id: accountId },
  });
});

test('public IAM login requires a completed transaction and the actual authenticated account', async () => {
  const calls = [];
  const accountId = '019f0000-0000-7000-8000-000000000001';
  const responses = [
    {
      id: 'actual-transaction',
      step: { type: 'form', formId: 'actual-form', fields: [] },
    },
    { status: 'completed' },
    { account: { id: accountId } },
  ];
  const transport = {
    request: async (path, body) => {
      calls.push({ path, body });
      return responses.shift();
    },
  };
  assert.equal(
    await publicLogin(transport, {
      providerId: 'discovered-provider',
      username: 'alice',
      password: 'test-only',
    }),
    accountId,
  );
  assert.equal(
    calls[1].path,
    '/api/iam/v1/login/transactions/actual-transaction/advance',
  );
  assert.equal(calls[1].body.formId, 'actual-form');
  assert.equal(calls[2].path, '/api/iam/v1/me');
  await assert.rejects(
    publicLogin(
      {
        request: async () => ({
          step: { type: 'form', fields: [] },
          status: 'pending',
        }),
      },
      { providerId: 'provider' },
    ),
  );
});
