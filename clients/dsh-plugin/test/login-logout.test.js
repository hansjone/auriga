import test from 'node:test';
import assert from 'node:assert/strict';
import { shellRuntimePeer } from './shell-runtime-peer.js';

test('status GET is read only and never signs out', async (t) => {
  const f = await shellRuntimePeer(t);
  const before = f.connection.calls.length;
  assert.equal((await f.http('/auriga/shell/status')).body.loggedIn, true);
  assert.equal(f.connection.calls.length, before);
  assert.ok(f.runtime.resolve(f.request));
});
test('native cancel delegates cancel_login without revoking the current identity', async (t) => {
  const f = await shellRuntimePeer(t);
  assert.equal((await f.rpc('aurigaAuth/cancel')).ok, true);
  assert.equal(f.connection.calls.at(-1).action, 'cancel_login');
  assert.ok(f.runtime.resolve(f.request));
});
test('explicit native logout invalidates existing owner identity without stopping local host', async (t) => {
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
  assert.equal((await f.rpc('aurigaAuth/logout')).value.loggedIn, false);
  assert.equal(f.runtime.resolve(f.request), undefined);
  assert.equal(f.runtime.status(f.request).available, true);
  assert.equal(stopped, 0);
});
test('broker failure never exposes an exception or falsely confirms sign out', async (t) => {
  const f = await shellRuntimePeer(t);
  f.connection.ipc.call = async () => {
    throw Error('PRIVATE_PROVIDER_DETAIL');
  };
  const result = await f.rpc('aurigaAuth/logout');
  assert.equal(result.ok, false);
  assert.doesNotMatch(JSON.stringify(result), /PRIVATE_PROVIDER_DETAIL/);
});
test('cancelled native logout response is suppressed even when broker completes late', async (t) => {
  const f = await shellRuntimePeer(t),
    gate = Promise.withResolvers(),
    entered = Promise.withResolvers(),
    abort = new AbortController();
  const original = f.connection.ipc.call.bind(f.connection.ipc);
  f.connection.ipc.call = async (value) => {
    entered.resolve();
    await gate.promise;
    return original(value);
  };
  const pending = f.rpc(
    'aurigaAuth/logout',
    { args: {} },
    f.peer,
    abort.signal,
  );
  await entered.promise;
  abort.abort();
  gate.resolve();
  assert.equal((await pending).ok, false);
  assert.equal(f.runtime.resolve(f.request), undefined);
});
