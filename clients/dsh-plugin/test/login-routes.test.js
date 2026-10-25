import { test } from 'node:test';
import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import { Readable } from 'node:stream';
import {
  createConnectRoutes,
  createUnconfiguredLoginRoutes,
} from '../src/local/routes.js';
import { PlatformError } from '../src/local/platform.js';

function fixture(options = {}) {
  let writes = 0;
  const handler = createConnectRoutes({
    runtime: {
      prepare: () => ({ body: {} }),
      checkCsrf: () => true,
      login: async () => {
        writes++;
        throw options.error ?? new Error('private SECRET');
      },
      logout: async () => {
        writes++;
        return { body: { loggedIn: false } };
      },
      cancelAttempt: async () => {},
    },
    admission: {
      nativeAuth: { requestRejection: () => options.rejection },
      sameOrigin: () => options.origin !== false,
    },
  });
  return { handler, writes: () => writes };
}
async function request(handler, path, body) {
  const req = Readable.from(
    body === undefined ? [] : [Buffer.from(JSON.stringify(body))],
  );
  Object.assign(req, {
    method: body === undefined ? 'GET' : 'POST',
    url: '/auriga/local-login' + path,
    headers: { 'content-type': 'application/json' },
  });
  const res = new EventEmitter();
  res.writeHead = (status, headers) => {
    res.status = status;
    res.headers = headers;
  };
  res.end = (body) => {
    res.body = String(body ?? '');
    res.writableEnded = true;
  };
  await handler(req, res);
  return res;
}
test('fixed browser module graph is served with CSP; logout GET/query never writes', async () => {
  const { handler, writes } = fixture();
  const modules = [
    'adapter',
    'index',
    'styles',
    'errors',
    'local-controller',
    'logout',
    'page',
  ];
  for (const name of modules) {
    const res = await request(handler, '/assets/' + name + '.js');
    assert.equal(res.status, 200);
    assert.match(res.headers['content-type'], /javascript/);
    assert.equal(res.headers['x-content-type-options'], 'nosniff');
    for (const match of res.body.matchAll(/from ['"]\.\/([^'"]+)['"]/g)) {
      assert.equal((await request(handler, '/assets/' + match[1])).status, 200);
    }
  }
  const page = await request(handler, '?logout=1');
  assert.equal(page.status, 200);
  assert.match(page.headers['content-security-policy'], /form-action 'none'/);
  assert.equal((await request(handler, '/logout')).status, 404);
  assert.equal((await request(handler, '/assets/../runtime.js')).status, 404);
  assert.equal(writes(), 0);
});
test('route preserves expired, unsupported and network categories without internal detail', async () => {
  for (const [error, status, code] of [
    [new Error('unsupported native login step'), 501, 'common.unavailable'],
    [new Error('login attempt expired'), 401, 'common.unauthenticated'],
    [new Error('invalid device credential'), 401, 'common.unauthenticated'],
    [
      new PlatformError(400, 'iam.login_tx_expired'),
      400,
      'iam.login_tx_expired',
    ],
    [new PlatformError(400, 'invalid_grant'), 401, 'common.unauthenticated'],
    [new Error('private SECRET'), 503, 'common.unavailable'],
  ]) {
    const { handler } = fixture({ error });
    const res = await request(handler, '/login', { action: 'begin' });
    assert.equal(res.status, status);
    assert.equal(JSON.parse(res.body).code, code);
    assert.doesNotMatch(res.body, /SECRET|credential|unsupported native/);
  }
});
test('callback, event, poll and webauthn cannot bypass native FormInput; auth and origin remain ahead of writes', async () => {
  const { handler, writes } = fixture();
  for (const input of [
    { type: 'poll' },
    { type: 'webauthn', credential: {} },
    { type: 'callback', params: {} },
    { type: 'event', name: 'qr_confirmed' },
  ]) {
    const res = await request(handler, '/login', { action: 'advance', input });
    assert.equal(res.status, 400);
    assert.equal(JSON.parse(res.body).code, 'common.validation_failed');
  }
  for (const options of [{ rejection: 401 }, { origin: false }]) {
    const f = fixture(options);
    assert.equal(
      (await request(f.handler, '/logout', {})).status,
      options.rejection ?? 403,
    );
    assert.equal(f.writes(), 0);
  }
  assert.equal(writes(), 0);
});
test('unconfigured host still serves login and recovery UI behind native admission; never accepts writes', async () => {
  const handler = createUnconfiguredLoginRoutes({
    requestRejection: () => undefined,
  });
  assert.equal((await request(handler, '')).status, 200);
  assert.equal((await request(handler, '/assets/page.js')).status, 200);
  assert.equal((await request(handler, '/status')).status, 404);
  assert.equal(
    (await request(handler, '/login', { action: 'begin' })).status,
    403,
  );
  const rejected = createUnconfiguredLoginRoutes({
    requestRejection: () => 401,
  });
  assert.equal((await request(rejected, '')).status, 401);
});
