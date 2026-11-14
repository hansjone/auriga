import test from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { once } from 'node:events';
import { randomBytes } from 'node:crypto';
import { createConnectRoutes } from '../src/local/routes.js';

async function fixture(t) {
  const csrf = randomBytes(32).toString('base64url'),
    commands = [];
  const handler = createConnectRoutes({
    admission: {
      nativeAuth: {
        requestRejection: (req) =>
          req.headers['x-test-owner'] === 'owner' ? undefined : 401,
      },
      sameOrigin: (req, write) =>
        !write || req.headers.origin === `http://${req.headers.host}`,
    },
    runtime: {
      prepare: () => ({ body: { loggedIn: false, csrf } }),
      checkCsrf: (req) => req.headers['x-auriga-csrf'] === csrf,
      async command(_req, action) {
        commands.push(action);
        return { loggedIn: false };
      },
    },
  });
  const server = createServer((req, res) => {
    void handler(req, res);
  });
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  t.after(async () => {
    server.closeAllConnections();
    await new Promise((r) => server.close(r));
  });
  const base = `http://127.0.0.1:${server.address().port}`;
  const post = (body, headers = {}) =>
    fetch(base + '/auriga/shell/command', {
      method: 'POST',
      headers: {
        'x-test-owner': 'owner',
        'x-auriga-csrf': csrf,
        origin: base,
        'content-type': 'application/json',
        ...headers,
      },
      body: JSON.stringify(body),
    });
  return { base, post, commands };
}
test('normal shell route exposes state and fixed commands, never a login page', async (t) => {
  const f = await fixture(t);
  const status = await fetch(f.base + '/auriga/shell/status', {
    headers: { 'x-test-owner': 'owner' },
  });
  assert.equal(status.status, 200);
  assert.equal(status.headers.get('cache-control'), 'no-store');
  for (const path of [
    '/auriga/local-login',
    '/auriga/local-login/assets/adapter.js',
    '/auriga/shell',
  ])
    assert.equal(
      (await fetch(f.base + path, { headers: { 'x-test-owner': 'owner' } }))
        .status,
      404,
    );
  assert.equal((await f.post({ action: 'open_login' })).status, 200);
  assert.deepEqual(f.commands, ['open_login']);
});
test('shell command rejects missing owner, wrong Origin/CSRF and old form input', async (t) => {
  const f = await fixture(t);
  assert.equal(
    (await f.post({ action: 'open_login' }, { 'x-test-owner': '' })).status,
    401,
  );
  assert.equal(
    (
      await f.post(
        { action: 'open_login' },
        { origin: 'https://other.example.com' },
      )
    ).status,
    403,
  );
  assert.equal(
    (await f.post({ action: 'logout' }, { 'x-auriga-csrf': '' })).status,
    403,
  );
  assert.equal(
    (await f.post({ action: 'advance', input: { password: 'ignored' } }))
      .status,
    400,
  );
  assert.deepEqual(f.commands, []);
});
