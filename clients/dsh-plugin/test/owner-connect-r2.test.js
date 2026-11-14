import { test } from 'node:test';
import assert from 'node:assert/strict';
import { installLocalAdmission } from '../src/local/host.js';
import { shellRuntimePeer } from './shell-runtime-peer.js';

for (const kind of ['browser', 'api'])
  test(`R2 ${kind} admitted owner can use local DSH before platform login and after expiry`, () => {
    const peer = { principal: { kind: 'owner' } };
    const connection = {
      admit: (req) =>
        req.headers.cookie === 'dsh=valid' ? { peer } : { rejection: 401 },
      authorizeIndex: () => kind === 'browser',
      isTrusted: () => true,
    };
    const gate = installLocalAdmission(
      connection,
      {
        isConfigured: () => true,
        resolve: () => null,
        installHttpTracking: () => () => {},
      },
      { protocol: 'http:' },
    );
    try {
      for (const upgrade of [undefined, 'websocket']) {
        const request = {
          method: 'POST',
          url: '/api/session/create',
          headers: { cookie: 'dsh=valid', upgrade },
        };
        assert.equal(connection.admit(request).peer, peer);
        assert.equal(
          connection.admit({
            ...request,
            headers: { 'x-auriga-native': 'true' },
          }).rejection,
          401,
        );
      }
    } finally {
      gate.dispose();
    }
  });

test('R2 non-owner admission does not acquire local owner authority', () => {
  const connection = {
    admit: () => ({ peer: { principal: { kind: 'guest' } } }),
    authorizeIndex: () => false,
    isTrusted: () => true,
  };
  const gate = installLocalAdmission(
    connection,
    {
      isConfigured: () => false,
      installHttpTracking: () => () => {},
    },
    { protocol: 'http:' },
  );
  try {
    assert.equal(connection.admit({ headers: {} }).rejection, 403);
  } finally {
    gate.dispose();
  }
});

test('R2 platform identity is shared by all DSH owners and logout leaves no platform identity', async (t) => {
  const { runtime } = await shellRuntimePeer(t);
  const first = runtime.nativeRequest({ principal: { kind: 'owner' } });
  const second = runtime.nativeRequest({ principal: { kind: 'owner' } });
  assert.equal(runtime.status(first).loggedIn, true);
  assert.equal(runtime.status(second).loggedIn, true);
  await runtime.command(second, 'logout');
  assert.equal(runtime.status(first).loggedIn, false);
  assert.equal(runtime.resetEmergency, undefined);
});

test('R2 the plugin can only open the shell and cannot select an identity provider or form', async (t) => {
  const { rpc, connection, runtime } = await shellRuntimePeer(t);
  connection.logout();
  const result = await rpc('aurigaAuth/login');
  assert.equal(result.ok, true);
  assert.equal(connection.calls.at(-1).action, 'open_login');
  assert.equal(result.value.loggedIn, false);
  for (const name of [
    'login',
    'cancelAttempt',
    'attemptToken',
    'resetEmergency',
  ])
    assert.equal(runtime[name], undefined);
  const before = connection.calls.length;
  assert.equal(
    (await rpc('aurigaAuth/login', { args: { providerId: 'team' } })).ok,
    false,
  );
  assert.equal(connection.calls.length, before);
});
test('R3 a cancelled RPC discards its late result and cannot clear a newer shell identity', async (t) => {
  const { rpc, connection, runtime, request, peer } = await shellRuntimePeer(t);
  const call = connection.ipc.call,
    entered = Promise.withResolvers(),
    release = Promise.withResolvers();
  connection.ipc.call = async (message) => {
    if (message.action === 'cancel_login') {
      entered.resolve();
      await release.promise;
    }
    return call(message);
  };
  const abort = new AbortController();
  const old = rpc('aurigaAuth/cancel', { args: {} }, peer, abort.signal);
  await entered.promise;
  abort.abort();
  connection.transition('authenticated');
  release.resolve();
  assert.equal((await old).ok, false);
  assert.equal(runtime.status(request).loggedIn, true);
  assert.equal(connection.calls.filter((m) => m.action === 'logout').length, 0);
});
