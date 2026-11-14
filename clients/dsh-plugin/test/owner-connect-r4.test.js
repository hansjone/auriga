import test from 'node:test';
import assert from 'node:assert/strict';
import { setImmediate as nextTurn } from 'node:timers/promises';
import { randomBytes } from 'node:crypto';
import { shellRuntimePeer } from './shell-runtime-peer.js';
import { teamRuntimePeer } from './team-runtime-peer.js';

test('R4 expired snapshots cannot offer a saved credential resume path', async (t) => {
  t.mock.timers.enable({ apis: ['Date'], now: Date.now() });
  const f = await shellRuntimePeer(t);
  t.mock.timers.tick(30001);
  const status = f.runtime.status(f.request);
  assert.equal(status.loggedIn, false);
  assert.equal(status.platform, 'unreachable');
  assert.equal(status.account, null);
  assert.equal(status.resumeAvailable, undefined);
  assert.equal(f.runtime.getAccess, undefined);
  assert.equal(f.runtime.vault.snapshot().credential, undefined);
});
for (const kind of ['busy', 'configuration', 'platform_http', 'dns'])
  test(
    'R4 native RPC never leaks broker exception details: ' + kind,
    async (t) => {
      const f = await shellRuntimePeer(t),
        secret = randomBytes(32).toString('base64url');
      f.connection.ipc.call = async () => {
        throw Object.assign(new Error(secret), { code: kind, status: 503 });
      };
      const result = await f.rpc('aurigaAuth/login');
      assert.equal(result.ok, false);
      assert.equal(JSON.stringify(result).includes(secret), false);
      assert.equal(result.error.code, 'common.unavailable');
    },
  );
test('R4 unreachable shell can recover by an authenticated push without restarting local DSH', async (t) => {
  let stopped = 0;
  const f = await shellRuntimePeer(t, {
    ctx: {
      sessionController: {
        stop() {
          stopped++;
        },
      },
    },
  });
  f.connection.transition('unreachable');
  assert.equal(f.runtime.resolve(f.request), undefined);
  await f.rpc('aurigaAuth/cancel');
  assert.equal(f.runtime.status(f.request).loggedIn, false);
  f.connection.transition('authenticated');
  await nextTurn();
  assert.equal(f.runtime.status(f.request).loggedIn, true);
  assert.equal(stopped, 0);
  assert.equal(
    f.connection.calls.some((c) => /oauth2/.test(c.path ?? '')),
    false,
  );
});
test('R4 closing a native RPC discards the result without issuing a shared cancel or logout', async (t) => {
  const f = await shellRuntimePeer(t),
    entered = Promise.withResolvers(),
    release = Promise.withResolvers();
  const call = f.connection.ipc.call;
  f.connection.ipc.call = async (message) => {
    if (message.action === 'open_login') {
      entered.resolve();
      await release.promise;
    }
    return call(message);
  };
  const abort = new AbortController();
  const result = f.rpc('aurigaAuth/login', { args: {} }, f.peer, abort.signal);
  await entered.promise;
  abort.abort();
  release.resolve();
  assert.equal((await result).ok, false);
  assert.equal(f.runtime.status(f.request).loggedIn, true);
  assert.equal(
    f.connection.calls.some((c) =>
      ['cancel_login', 'logout'].includes(c.action),
    ),
    false,
  );
});
for (const name of [undefined, 'Verified Alice'])
  test(
    'R4 display name is projected from the member-bound broker profile: ' +
      name,
    async (t) => {
      const f = await shellRuntimePeer(t, {
        configure(connection) {
          const call = connection.ipc.call;
          connection.ipc.call = async (message) =>
            message.call?.operation === 'profile_read'
              ? {
                  requestId: message.requestId,
                  operation: 'profile_read',
                  output: {
                    statusCode: 200,
                    body: {
                      memberId: connection.identity.memberId,
                      displayName: name ?? '',
                    },
                  },
                }
              : call(message);
        },
      });
      await nextTurn();
      assert.equal(f.runtime.status(f.request).account.displayName, name);
    },
  );
test('R4 valid same-generation snapshots do not reconnect Team or refresh platform tokens', async (t) => {
  const f = await teamRuntimePeer(t);
  await f.settled();
  for (let i = 0; i < 30; i++) f.connection.refresh();
  await nextTurn();
  assert.equal(
    f.connection.calls.filter((c) => c.call?.operation === 'team_initialize')
      .length,
    1,
  );
  assert.equal(f.tools.size, 4);
  assert.equal(f.runtime.status(f.request).loggedIn, true);
  assert.equal(
    f.connection.calls.some((c) => /oauth2/.test(c.path ?? '')),
    false,
  );
});
