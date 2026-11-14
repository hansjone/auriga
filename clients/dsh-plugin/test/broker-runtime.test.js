import test from 'node:test';
import { randomUUID } from 'node:crypto';
import assert from 'node:assert/strict';
import { mkdtemp, rm, writeFile, readFile } from 'node:fs/promises';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import { createConnectRuntime } from '../src/local/runtime.js';
import { resourceRequest } from '../src/resources/broker-transport.js';
import { createVault } from '../src/local/vault.js';
import { brokerFixture } from './broker-fixture.js';

async function fixture(t, legacy = false) {
  const directory = await mkdtemp(join(tmpdir(), 'auriga-shell-runtime-'));
  t.after(() => rm(directory, { recursive: true, force: true }));
  await writeFile(join(directory, 'private-content.txt'), 'private content');
  if (legacy) {
    const vault = await createVault(directory);
    await vault.update((s) => {
      if (legacy === 'revocation')
        s.revocations = [{ refreshToken: randomUUID() }];
      else s.credential = { accessToken: randomUUID(), claims: {} };
    });
    vault.close();
  }
  const connection = brokerFixture(),
    ctx = { sessions: { get: () => ({ id: 'session' }) } };
  const runtime = await createConnectRuntime({
    config: { platformOrigin: '' },
    directory,
    ctx,
    connection,
  });
  t.after(() => runtime.close());
  const req = runtime.nativeRequest({ principal: { kind: 'owner' } });
  return { runtime, connection, req, directory };
}

test('actual connect runtime inherits shell identity without platform credentials', async (t) => {
  const { runtime, connection, req } = await fixture(t);
  assert.equal(runtime.status(req).loggedIn, true);
  assert.equal(runtime.identity().ownerId, connection.identity.memberId);
  assert.equal(runtime.vault.snapshot().credential, undefined);
  assert.equal(runtime.getAccess, undefined);
  assert.equal(runtime.status({ headers: {} }).loggedIn, false);
});
test('host contexts and latest credential version use the K2 IPC envelopes', async (t) => {
  const { runtime, connection } = await fixture(t);
  const context = await runtime.broker.register({
    kind: 'session',
    hostSessionId: 'session',
    expiresAt: new Date(Date.now() + 600000).toISOString(),
  });
  const first = await runtime.broker.credentials(
    context.executionContextId,
    'raw',
  );
  assert.equal(first.token, connection.token());
  connection.rotate();
  const next = await runtime.broker.credentials(
    context.executionContextId,
    'outbound',
  );
  assert.notEqual(next.token, first.token);
  assert.equal(next.credentialVersion, first.credentialVersion + 1);
  await runtime.broker.revoke(context.executionContextId);
  assert.equal(connection.contexts.size, 0);
});
test('logout push invalidates the same admitted native owner immediately', async (t) => {
  const { runtime, connection, req } = await fixture(t);
  assert.ok(runtime.resolve(req));
  connection.logout();
  assert.equal(runtime.resolve(req), undefined);
  assert.equal(runtime.status(req).loggedIn, false);
});
test('legacy credential removal preserves private files and requires shell authority', async (t) => {
  const { runtime, directory } = await fixture(t, true);
  assert.equal(runtime.vault.snapshot().credential, undefined);
  assert.equal(
    await readFile(join(directory, 'private-content.txt'), 'utf8'),
    'private content',
  );
});
test('migration also removes queued legacy revocation credentials after local logout', async (t) => {
  const { runtime, directory } = await fixture(t, 'revocation');
  runtime.close();
  const stored = await createVault(directory);
  t.after(() => stored.close());
  assert.equal(stored.snapshot().credential, null);
  assert.deepEqual(stored.snapshot().revocations, []);
  assert.equal(stored.snapshot().endpoint, null);
  assert.equal(
    await readFile(join(directory, 'private-content.txt'), 'utf8'),
    'private content',
  );
});
test('resource transport selects a fixed operation and never calls fetch aliases', async (t) => {
  const { runtime, connection } = await fixture(t);
  const original = globalThis.fetch;
  let direct = 0;
  globalThis.fetch = async () => {
    direct++;
    throw new Error('unexpected direct network');
  };
  t.after(() => {
    globalThis.fetch = original;
  });
  const alias = globalThis.fetch;
  const response = await resourceRequest(
    runtime,
    '/api/resource/v1/catalog/packages?limit=10&kind=skill',
    { fetch: alias, headers: { authorization: 'ignored' } },
  );
  assert.equal(response.status, 200);
  assert.equal(
    connection.calls.at(-1).call.operation,
    'UnifiedListResourcePackages',
  );
  assert.equal(connection.calls.at(-1).call.input.limit, 10);
  assert.equal(connection.calls.at(-1).call.input.headers, undefined);
  assert.equal(direct, 0);
});
test('arbitrary fetch-style platform targets and unknown operations fail before transport', async (t) => {
  const { runtime, connection } = await fixture(t);
  const before = connection.calls.length;
  for (const path of [
    'https://portal.example.com/api/resource/v1/catalog/packages',
    '//portal.example.com/api/resource/v1/catalog/packages',
    '/api/iam/v1/me',
    '/api/resource/v1/new',
    '/api/resource/v1/catalog/packages?url=https://other.example.com',
    '/api/resource/v1/../packages',
  ])
    await assert.rejects(resourceRequest(runtime, path));
  assert.equal(connection.calls.length, before);
  await assert.rejects(runtime.broker.call('unknown', {}));
});

test('an IPC business call that never settles rejects immediately when the shell is invalidated', async (t) => {
  const { runtime, connection } = await fixture(t);
  const entered = Promise.withResolvers();
  const call = connection.ipc.call;
  connection.ipc.call = (message) => {
    if (message.call?.operation === 'listAiModels') {
      entered.resolve();
      return new Promise(() => {});
    }
    return call(message);
  };
  const pending = runtime.broker.call('listAiModels', {});
  const denied = assert.rejects(pending, { code: 'common.unauthenticated' });
  await entered.promise;
  connection.logout();
  await denied;
});
test('revocation before the IPC microtask prevents a queued platform call from starting', async (t) => {
  const { runtime, connection } = await fixture(t);
  const before = connection.calls.length;
  const pending = runtime.broker.call('listAiModels', {});
  const denied = assert.rejects(pending, { code: 'common.unauthenticated' });
  connection.logout();
  await denied;
  assert.equal(connection.calls.length, before);
});
