import test from 'node:test';
import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { mkdtemp, rm, writeFile, readFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { brokerFixture } from '../broker-fixture.js';
import { createManagedNetwork } from '../../src/network/managed-device.js';
import { setImmediate as nextTurn } from 'node:timers/promises';

async function fixture(t, configure) {
  const directory = await mkdtemp(join(tmpdir(), 'auriga-managed-shell-'));
  const connection = brokerFixture();
  const tenantId = connection.identity.tenantId,
    ownerId = connection.identity.memberId;
  configure?.(connection);
  const network = createManagedNetwork({
    directory,
    tenantId,
    ownerId,
    instanceId: randomUUID(),
    ctx: {},
    connection,
  });
  t.after(async () => {
    await network.close();
    await rm(directory, { recursive: true, force: true });
  });
  return { network, connection, directory, tenantId, ownerId };
}
const denied = [
  ...[
    'signed_out',
    'authenticating',
    'unreachable',
    'reauth_required',
    'revoked',
  ].map((state) => [state, (c) => c.transition(state)]),
  [
    'other_tenant',
    (c) => c.transition('authenticated', { tenantId: randomUUID() }),
  ],
  [
    'other_member',
    (c) => c.transition('authenticated', { memberId: randomUUID() }),
  ],
];
for (const [state, configure] of denied)
  for (const port of [
    'resource',
    'account',
    'models',
    'consent',
    'diagnostics',
  ])
    test(`managed ${state} cannot authorize ${port} through inherited shell identity`, async (t) => {
      const f = await fixture(t, configure);
      await f.network.start();
      assert.equal(f.network.sessionAvailable, false);
      if (port === 'resource')
        assert.equal(f.network.resourceSession, undefined);
      if (port === 'account') assert.equal(f.network.account, undefined);
      if (port === 'models') {
        let registrations = 0;
        const detach = await f.network.attachModels({
          registerAdapter() {
            registrations++;
            return () => {};
          },
        });
        assert.equal(registrations, 0);
        detach();
      }
      if (port === 'consent') {
        assert.equal(f.network.consentAvailable, false);
        assert.throws(() => f.network.consentUI({}));
        assert.throws(() => f.network.messagingUI({}));
      }
      if (port === 'diagnostics')
        assert.equal(f.network.baseTools.getState().loggedIn, false);
      assert.equal(f.network.status.connected, false);
      assert.equal(
        f.connection.calls.filter((call) => call.call).length,
        0,
        'foreign or unauthenticated identity never starts platform work',
      );
    });

test('managed admitted identity comes from broker and exposes no raw device material', async (t) => {
  const f = await fixture(t);
  assert.equal(f.network.sessionAvailable, false);
  await f.network.start();
  assert.equal(f.network.sessionAvailable, true);
  assert.equal(f.network.account.subject, f.ownerId);
  assert.equal(f.network.resourceSession.identity().tenantId, f.tenantId);
  assert.equal(f.network.resourceSession.getAccess, undefined);
  assert.equal(
    f.network.resourceSession.vault.snapshot().credential,
    undefined,
  );
  assert.equal(
    f.network.status.connected,
    false,
    'authentication alone is not message READY',
  );
});
test('managed source does not read legacy token files or use platform HTTP', async (t) => {
  const f = await fixture(t),
    path = join(f.directory, 'managed-device.json');
  await writeFile(path, 'invalid legacy material');
  t.mock.method(globalThis, 'fetch', () =>
    assert.fail('no plugin platform fetch'),
  );
  await f.network.start();
  assert.equal(f.network.sessionAvailable, true);
  assert.equal(await readFile(path, 'utf8'), 'invalid legacy material');
});
test('concurrent managed startup shares exactly one shell subscription', async (t) => {
  const f = await fixture(t),
    original = f.connection.ipc.subscribe.bind(f.connection.ipc);
  let subscriptions = 0;
  f.connection.ipc.subscribe = (...args) => {
    subscriptions++;
    return original(...args);
  };
  const first = f.network.start();
  assert.equal(f.network.start(), first);
  await first;
  assert.equal(subscriptions, 1);
});
test('managed close before startup cannot open or revive a broker', async (t) => {
  const f = await fixture(t);
  await f.network.close();
  await f.network.start();
  assert.equal(f.connection.calls.length, 0);
  assert.equal(f.network.sessionAvailable, false);
});
test('close fences a late initial snapshot and all concurrent callers await cleanup', async (t) => {
  const gate = Promise.withResolvers(),
    entered = Promise.withResolvers();
  const f = await fixture(t),
    original = f.connection.ipc.subscribe.bind(f.connection.ipc);
  f.connection.ipc.subscribe = async (...args) => {
    entered.resolve();
    await gate.promise;
    return original(...args);
  };
  const start = f.network.start();
  await entered.promise;
  const closes = [f.network.close(), f.network.close()];
  gate.resolve();
  await Promise.all([start, ...closes]);
  assert.equal(f.network.sessionAvailable, false);
  assert.equal(f.network.resourceSession, undefined);
});
test('same-generation shell refresh preserves the verified resource session', async (t) => {
  const f = await fixture(t);
  await f.network.start();
  const runtime = f.network.resourceSession;
  f.connection.refresh();
  await nextTurn();
  assert.equal(f.network.resourceSession, runtime);
  assert.equal(f.network.sessionAvailable, true);
});
test('expired shell freshness cannot keep authorizing a managed instance', async (t) => {
  t.mock.timers.enable({ apis: ['Date', 'setTimeout'], now: 2000000000000 });
  const f = await fixture(t);
  await f.network.start();
  assert.equal(f.network.sessionAvailable, true);
  t.mock.timers.tick(29001);
  assert.equal(f.network.sessionAvailable, false);
  assert.equal(f.network.resourceSession, undefined);
});
test('broker revocation removes models and managed instance admission immediately', async (t) => {
  const f = await fixture(t);
  await f.network.start();
  let registered = 0;
  const detach = await f.network.attachModels({
    registerAdapter() {
      registered++;
      return () => registered--;
    },
  });
  assert.equal(registered, 1);
  f.connection.transition('revoked');
  assert.equal(registered, 0);
  assert.equal(f.network.sessionAvailable, false);
  assert.equal(f.network.sessionRevoked, true);
  detach();
});
test('foreign identity push removes models and later matching shell login restores only authorized ports', async (t) => {
  const f = await fixture(t);
  await f.network.start();
  let registered = 0;
  const detach = await f.network.attachModels({
    registerAdapter() {
      registered++;
      return () => registered--;
    },
  });
  f.connection.transition('authenticated', { memberId: randomUUID() });
  await nextTurn();
  assert.equal(registered, 0);
  assert.equal(f.network.resourceSession, undefined);
  f.connection.transition('authenticated', { memberId: f.ownerId });
  await nextTurn();
  assert.equal(f.network.sessionAvailable, true);
  assert.equal(registered, 1);
  detach();
});
