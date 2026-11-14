import test from 'node:test';
import assert from 'node:assert/strict';
import { Readable } from 'node:stream';
import { createUnconfiguredLoginRoutes } from '../src/local/routes.js';
import { shellRuntimePeer } from './shell-runtime-peer.js';

test('removed browser module graph never serves login HTML or assets', async (t) => {
  const f = await shellRuntimePeer(t),
    before = f.connection.calls.length;
  for (const path of [
    '/auriga/local-login',
    '/auriga/local-login?logout=1',
    '/auriga/shell/assets/index.js',
    '/auriga/shell/logout',
    '/auriga/shell',
  ])
    assert.equal((await f.http(path)).status, 404);
  assert.equal(f.connection.calls.length, before);
});
test('shell failure response contains only a safe error code and never provider detail', async (t) => {
  const f = await shellRuntimePeer(t);
  f.connection.ipc.call = async () => {
    throw Error('PRIVATE_PROVIDER_DETAIL');
  };
  const result = await f.http('/auriga/shell/command', {
    method: 'POST',
    body: { action: 'open_login' },
  });
  assert.equal(result.status, 503);
  assert.deepEqual(result.body, { code: 'common.unavailable' });
});
test('native admission and same origin guard status, commands, consent and messaging', async (t) => {
  const f = await shellRuntimePeer(t),
    before = f.connection.calls.length;
  for (const path of [
    '/auriga/shell/status',
    '/auriga/shell/command',
    '/auriga/shell/consent/snapshot',
    '/auriga/shell/messaging/snapshot',
  ]) {
    for (const method of ['GET', 'POST']) {
      assert.equal(
        (
          await f.http(path, {
            method,
            body: { action: 'logout' },
            admitted: false,
          })
        ).status,
        401,
      );
      assert.equal(
        (
          await f.http(path, {
            method,
            body: { action: 'logout' },
            sameOrigin: false,
          })
        ).status,
        403,
      );
    }
  }
  assert.equal(f.connection.calls.length, before);
  assert.ok(f.runtime.resolve(f.request));
});
test('only the three fixed shell commands pass the body and CSRF guards', async (t) => {
  const f = await shellRuntimePeer(t);
  for (const action of ['open_login', 'cancel_login', 'logout']) {
    const result = await f.http('/auriga/shell/command', {
      method: 'POST',
      body: { action },
    });
    assert.equal(result.status, 200);
    assert.equal(f.connection.calls.at(-1).action, action);
  }
});
test('form, poll, callback, credentials, targets and oversized bodies fail before broker work', async (t) => {
  const f = await shellRuntimePeer(t),
    before = f.connection.calls.length;
  for (const body of [
    null,
    [],
    {},
    { action: 'begin' },
    { action: 'open_login', input: { type: 'form' } },
    { action: 'logout', url: 'https://other.example.com' },
    '{',
    'x'.repeat(1025),
  ])
    assert.equal(
      (await f.http('/auriga/shell/command', { method: 'POST', body })).status,
      400,
    );
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
        body: { action: 'logout' },
        contentType: 'text/plain',
      })
    ).status,
    400,
  );
  assert.equal(f.connection.calls.length, before);
});
test('unconfigured local host exposes only admitted status and never old login or a write', async () => {
  const handler = createUnconfiguredLoginRoutes({
    requestRejection: () => undefined,
  });
  const call = async (path, method = 'GET') => {
    const req = Readable.from([]);
    Object.assign(req, { url: path, method, headers: {} });
    let status, body;
    await handler(req, {
      writeHead(code) {
        status = code;
      },
      end(value) {
        body = JSON.parse(value);
      },
    });
    return { status, body };
  };
  assert.equal((await call('/auriga/local-login')).status, 404);
  const result = await call('/auriga/shell/status');
  assert.equal(result.status, 200);
  assert.equal(result.body.configured, false);
  assert.equal(result.body.loggedIn, false);
  assert.equal((await call('/auriga/shell/command', 'POST')).status, 403);
});
