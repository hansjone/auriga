import test from 'node:test';
import { randomUUID, createHash } from 'node:crypto';
import assert from 'node:assert/strict';
import { mkdtemp, rm, mkdir, writeFile, readFile } from 'node:fs/promises';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import { createConnectRuntime } from '../src/local/runtime.js';
import { resourceRequest } from '../src/resources/broker-transport.js';
import { createVault } from '../src/local/vault.js';
import { brokerFixture } from './broker-fixture.js';
import { openLedger } from '../src/network/host/ledger.js';
import { createFileLedgerStore } from '../src/network/host/store.js';

async function fixture(t, legacy = false, platformOrigin = '') {
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
    config: { platformOrigin },
    directory,
    ctx,
    connection,
  });
  t.after(() => runtime.close());
  const req = runtime.nativeRequest({ principal: { kind: 'owner' } });
  return { runtime, connection, req, directory };
}

test('required Desktop updates block broker business and credentials while shell status and cleanup remain available', async (t) => {
  const root = await mkdtemp(join(tmpdir(), 'auriga-required-update-'));
  const savedRoot = process.env.AURIGA_DESKTOP_ROOT;
  const savedRuntime = process.env.AURIGA_DESKTOP_RUNTIME;
  t.after(async () => {
    if (savedRoot === undefined) delete process.env.AURIGA_DESKTOP_ROOT;
    else process.env.AURIGA_DESKTOP_ROOT = savedRoot;
    if (savedRuntime === undefined) delete process.env.AURIGA_DESKTOP_RUNTIME;
    else process.env.AURIGA_DESKTOP_RUNTIME = savedRuntime;
    await rm(root, { recursive: true, force: true });
  });
  const folder = join(root, 'versions', 'v1', 'auriga', 'runtime');
  await mkdir(folder, { recursive: true });
  await writeFile(
    join(root, 'install.json'),
    JSON.stringify({ current: 'v1' }),
  );
  const modulePath = join(folder, 'update.mjs');
  await writeFile(
    modulePath,
    `export function platformUrl() {}
     export function createUpdater() {
       let required = false;
       return {status: () => ({required}), configure(value) { required = value; }, start: () => () => {}};
     }`,
  );
  process.env.AURIGA_DESKTOP_ROOT = root;
  process.env.AURIGA_DESKTOP_RUNTIME = modulePath;
  const { runtime, connection, req } = await fixture(
    t,
    false,
    'https://example.com',
  );
  const context = {
    kind: 'session',
    hostSessionId: 'session',
    expiresAt: new Date(Date.now() + 600000).toISOString(),
  };
  const registered = await runtime.broker.register(context);
  await runtime.broker.call('UnifiedListResourcePackages', {
    limit: 10,
    kind: 'skill',
  });
  runtime.desktop.configure(true);
  const before = connection.calls.length;
  await assert.rejects(
    runtime.broker.call('UnifiedListResourcePackages', {
      limit: 10,
      kind: 'skill',
    }),
    {
      code: 'desktop.update_required',
    },
  );
  await assert.rejects(runtime.broker.register(context), {
    code: 'desktop.update_required',
  });
  await assert.rejects(
    runtime.broker.credentials(registered.executionContextId, 'raw'),
    { code: 'desktop.update_required' },
  );
  await assert.rejects(
    runtime.broker.streamCommand({
      streamId: randomUUID(),
      action: 'credit',
      creditBytes: 1,
    }),
    { code: 'desktop.update_required' },
  );
  await assert.rejects(
    runtime.broker.subscribeStream(randomUUID(), () => {}),
    { code: 'desktop.update_required' },
  );
  assert.equal(connection.calls.length, before);
  assert.equal(runtime.status(req).loggedIn, true);
  assert.equal(runtime.isReady(), false);
  await runtime.broker.revoke(registered.executionContextId);
  const cancelled = [];
  connection.ipc.streamCommand = async (command) => {
    cancelled.push(command);
  };
  await runtime.broker.streamCommand({
    streamId: randomUUID(),
    action: 'cancel',
  });
  assert.equal(cancelled.length, 1);
  assert.equal(connection.contexts.size, 0);
  runtime.desktop.configure(false);
  await runtime.broker.call('UnifiedListResourcePackages', {
    limit: 10,
    kind: 'skill',
  });
});

test('actual connect runtime inherits shell identity without platform credentials', async (t) => {
  const { runtime, connection, req } = await fixture(t);
  assert.equal(runtime.status(req).loggedIn, true);
  assert.equal(runtime.identity().ownerId, connection.identity.memberId);
  assert.equal(runtime.vault.snapshot().credential, undefined);
  assert.equal(runtime.getAccess, undefined);
  assert.equal(runtime.status({ headers: {} }).loggedIn, false);
});
test('shell identity reads the existing device ledger without changing its writer or retaining access after logout', async (t) => {
  const { runtime, connection, directory, req } = await fixture(t);
  const key = createHash('sha256')
    .update(
      JSON.stringify([
        '',
        connection.identity.tenantId,
        connection.identity.deviceId,
      ]),
    )
    .digest('hex');
  const dir = join(directory, 'network', 'devices', key, 'host');
  const ledger = await openLedger({
    identity: {
      tenant: connection.identity.tenantId,
      realm: '2',
      node: '64',
      nodeIncarnation: 'inc-1',
    },
    store: createFileLedgerStore({ dir }),
    initialize: true,
  });
  t.after(() => ledger.close());
  const before = await readFile(join(dir, 'ledger.json'), 'utf8');
  const lock = await readFile(join(dir, 'ledger.lock'), 'utf8');
  assert.deepEqual(
    (await runtime.baseTools.readLocal()).identity,
    ledger.peek().identity,
  );
  assert.equal(await readFile(join(dir, 'ledger.json'), 'utf8'), before);
  assert.equal(await readFile(join(dir, 'ledger.lock'), 'utf8'), lock);
  assert.deepEqual(runtime.baseTools.getState().network, runtime.networkStatus);
  assert.equal(
    runtime.status(req).capabilities.consent,
    runtime.consentAvailable,
  );
  connection.logout();
  assert.equal(await runtime.baseTools.readLocal(), null);
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
    '/api/resource/v1/../packages',
  ])
    await assert.rejects(resourceRequest(runtime, path));
  // The real schema emits a coded validation error, mapped to an HTTP response
  // by the resource adapter before any IPC business request is sent.
  assert.equal(
    (
      await resourceRequest(
        runtime,
        '/api/resource/v1/catalog/packages?url=https://other.example.com',
      )
    ).status,
    400,
  );
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
test('a pre-invalidated shell cannot start a platform call', async (t) => {
  const { runtime, connection } = await fixture(t);
  const before = connection.calls.length;
  connection.logout();
  await assert.rejects(runtime.broker.call('listAiModels', {}), {
    code: 'common.unauthenticated',
  });
  assert.equal(connection.calls.length, before);
});

test('broker.call waits for transient unreachable state to recover to authenticated', async (t) => {
  const { runtime, connection } = await fixture(t);
  connection.transition('unreachable');
  const pending = runtime.broker.call('UnifiedListResourcePackages', {
    limit: 10,
    kind: 'skill',
  });
  await new Promise((r) => setTimeout(r, 20));
  connection.transition('authenticated');
  const output = await pending;
  assert.equal(output.statusCode, 200);
});

test('broker.call returns common.unavailable when unreachable does not recover before timeout', async (t) => {
  const { createBroker } = await import('../src/local/broker.js');
  const connection = brokerFixture();
  const broker = await createBroker({
    ipc: connection.ipc,
    validate: connection.validate,
    authWaitMs: 60,
  });
  t.after(() => broker.close());
  connection.transition('unreachable');
  await assert.rejects(
    broker.call('UnifiedListResourcePackages', { limit: 10, kind: 'skill' }),
    {
      code: 'common.unavailable',
    },
  );
});

test('broker.call retries once when same-identity revision advances and causes common.conflict', async (t) => {
  const { runtime, connection } = await fixture(t);
  const origCall = connection.ipc.call;
  let attempts = 0;
  connection.ipc.call = async (message) => {
    if (message.call?.operation === 'UnifiedListResourcePackages') {
      attempts++;
      if (attempts === 1) {
        connection.transition('authenticated');
        return {
          requestId: message.requestId,
          operation: 'UnifiedListResourcePackages',
          error: {
            code: 'common.conflict',
            retryable: true,
          },
        };
      }
    }
    return origCall(message);
  };
  const output = await runtime.broker.call('UnifiedListResourcePackages', {
    limit: 10,
    kind: 'skill',
  });
  assert.equal(output.statusCode, 200);
  assert.equal(attempts, 2);
});

test('broker.call rejects queued call when account switches during unreachable recovery', async (t) => {
  const { runtime, connection } = await fixture(t);
  connection.transition('unreachable');
  const pending = runtime.broker.call('UnifiedListResourcePackages', {
    limit: 10,
    kind: 'skill',
  });
  await new Promise((r) => setTimeout(r, 20));
  connection.transition('authenticated', { memberId: randomUUID() });
  await assert.rejects(pending, { code: 'common.unauthenticated' });
});

test('broker.call never retries upstream HTTP 409 business conflict', async (t) => {
  const { runtime, connection } = await fixture(t);
  const origCall = connection.ipc.call;
  let attempts = 0;
  connection.ipc.call = async (message) => {
    if (message.call?.operation === 'UnifiedListResourcePackages') {
      attempts++;
      return {
        requestId: message.requestId,
        operation: 'UnifiedListResourcePackages',
        error: {
          code: 'common.conflict',
          retryable: false,
          upstreamStatus: 409,
        },
      };
    }
    return origCall(message);
  };
  await assert.rejects(
    runtime.broker.call('UnifiedListResourcePackages', {
      limit: 10,
      kind: 'skill',
    }),
    { code: 'common.conflict' },
  );
  assert.equal(attempts, 1);
});
