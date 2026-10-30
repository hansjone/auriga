import test from 'node:test';
import assert from 'node:assert/strict';
import {
  publicLogin,
  desktopMemberRole,
  ensureDesktopMemberRole,
  validateMemberCredentialsPath,
  writeMemberCredentials,
  inviteDesktopMember,
} from './cloud-desk.http.mjs';
import {
  mkdtemp,
  chmod,
  mkdir,
  readFile,
  stat,
  symlink,
  rm,
} from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { execFileSync } from 'node:child_process';
import { privatePath } from '../../scripts/beta-ui/runner.mjs';

test('member credentials require a private parent, preserve exclusive writes, and reject unsafe existing files', async (t) => {
  const parent = await mkdtemp(join(tmpdir(), 'auriga-member-'));
  t.after(() => rm(parent, { recursive: true, force: true }));
  const file = join(parent, 'member.json');
  if (process.platform === 'win32') {
    privatePath(parent, true);
    // Deliberately expose only this temporary directory to test the ACL refusal.
    execFileSync('icacls.exe', [parent, '/grant', '*S-1-1-0:(RX)'], {
      windowsHide: true,
    });
  } else await chmod(parent, 0o755);
  await assert.rejects(validateMemberCredentialsPath(file));
  // Invalid credentials directory must fail before even reading admin inputs.
  await assert.rejects(
    inviteDesktopMember(
      {
        desktopMemberFile: file,
        adminCredentialsFile: join(parent, 'absent-admin.json'),
      },
      {},
      {},
    ),
    (error) => error.code !== 'ENOENT',
  );
  await assert.rejects(writeMemberCredentials(file, { password: 'test-only' }));
  await assert.rejects(stat(file), { code: 'ENOENT' });
  if (process.platform === 'win32') privatePath(parent, true);
  else await chmod(parent, 0o700);
  await writeMemberCredentials(file, { password: 'test-only' });
  await validateMemberCredentialsPath(file);
  if (process.platform === 'win32') privatePath(file);
  else assert.equal((await stat(file)).mode & 0o777, 0o600);
  const original = await readFile(file, 'utf8');
  await assert.rejects(
    writeMemberCredentials(file, { password: 'replacement' }),
    { code: 'EEXIST' },
  );
  assert.equal(await readFile(file, 'utf8'), original);
  if (process.platform === 'win32')
    execFileSync('icacls.exe', [file, '/grant', '*S-1-1-0:(R)'], {
      windowsHide: true,
    });
  else await chmod(file, 0o644);
  await assert.rejects(validateMemberCredentialsPath(file));
});

test('member credential paths reject directory and linked parents before writing', async (t) => {
  const parent = await mkdtemp(join(tmpdir(), 'auriga-member-path-'));
  t.after(() => rm(parent, { recursive: true, force: true }));
  if (process.platform === 'win32') privatePath(parent, true);
  await mkdir(join(parent, 'directory'));
  await assert.rejects(
    validateMemberCredentialsPath(join(parent, 'directory')),
  );
  await symlink(
    parent,
    join(parent, 'link'),
    process.platform === 'win32' ? 'junction' : 'dir',
  );
  await assert.rejects(
    writeMemberCredentials(join(parent, 'link', 'member.json'), {}),
  );
  await assert.rejects(stat(join(parent, 'member.json')), { code: 'ENOENT' });
});

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
