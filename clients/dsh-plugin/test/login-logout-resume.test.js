import test from 'node:test';
import assert from 'node:assert/strict';
import { shellRuntimePeer } from './shell-runtime-peer.js';

test('shell logout uses fresh local CSRF and invalidates all admitted requests', async (t) => {
  const f = await shellRuntimePeer(t),
    other = f.runtime.nativeRequest(f.peer);
  const result = await f.http('/auriga/shell/command', {
    method: 'POST',
    body: { action: 'logout' },
  });
  assert.equal(result.status, 200);
  assert.equal(result.body.loggedIn, false);
  assert.equal(f.runtime.resolve(f.request), undefined);
  assert.equal(f.runtime.resolve(other), undefined);
});
test('anonymous request cannot resume another native owners identity', async (t) => {
  const f = await shellRuntimePeer(t),
    before = f.connection.calls.length;
  assert.equal(
    (
      await f.http('/auriga/shell/command', {
        method: 'POST',
        body: { action: 'logout' },
        admitted: false,
      })
    ).status,
    401,
  );
  assert.equal(f.connection.calls.length, before);
  assert.ok(f.runtime.resolve(f.request));
});
test('remote snapshot revocation immediately invalidates admitted owner requests', async (t) => {
  const f = await shellRuntimePeer(t);
  f.connection.logout();
  assert.equal(f.runtime.resolve(f.request), undefined);
  assert.equal(f.runtime.status(f.request).loggedIn, false);
  assert.equal(f.runtime.status(f.request).available, true);
});
test('repeated signed out status never starts a legacy credential resume', async (t) => {
  const f = await shellRuntimePeer(t);
  await f.rpc('aurigaAuth/logout');
  const count = f.connection.calls.length;
  for (let i = 0; i < 3; i++)
    assert.equal((await f.rpc('aurigaAuth/status')).value.loggedIn, false);
  assert.equal(f.connection.calls.length, count);
  assert.equal(f.runtime.login, undefined);
});
test('invalid CSRF and fake command actions cannot claim device logout', async (t) => {
  const f = await shellRuntimePeer(t),
    count = f.connection.calls.length;
  assert.equal(
    (
      await f.http('/auriga/shell/command', {
        method: 'POST',
        body: { action: 'logout' },
        csrf: false,
      })
    ).status,
    403,
  );
  assert.equal(
    (
      await f.http('/auriga/shell/command', {
        method: 'POST',
        body: { action: 'resume' },
      })
    ).status,
    400,
  );
  assert.equal(f.connection.calls.length, count);
  assert.ok(f.runtime.resolve(f.request));
});
