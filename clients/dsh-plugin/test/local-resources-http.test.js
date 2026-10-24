import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Readable } from 'node:stream';
import { createLocalResourceHandler } from '../src/local-resources/http.js';
import { installLocalResources } from '../src/local-resources/index.js';

function request(url, body, headers = {}) {
  return Object.assign(
    Readable.from(
      body === undefined
        ? []
        : [Buffer.from(typeof body === 'string' ? body : JSON.stringify(body))],
    ),
    {
      url,
      method: body === undefined ? 'GET' : 'POST',
      headers: {
        host: '127.0.0.1:8080',
        origin: 'http://127.0.0.1:8080',
        'x-auriga-local-resources': '1',
        'content-type': 'application/json',
        ...headers,
      },
    },
  );
}
function response() {
  return {
    status: 0,
    body: '',
    destroyed: false,
    writeHead(status) {
      this.status = status;
    },
    end(body) {
      this.body = body;
    },
  };
}
test('HTTP exact route/header/origin/schema fences execute before any local service operation', async () => {
  let calls = 0;
  const handler = createLocalResourceHandler({
    auth: { requestRejection() {} },
    service: {
      async list() {
        calls++;
        return {};
      },
      async detail() {
        calls++;
      },
      async change() {
        calls++;
      },
    },
  });
  for (const [req, status] of [
    [
      request('/auriga/local-resources', undefined, {
        'x-auriga-local-resources': undefined,
      }),
      403,
    ],
    [request('/auriga/local-resources/%64etail', {}), 404],
    [request('/auriga/local-resources/detail?url=example', {}), 404],
    [
      request(
        '/auriga/local-resources/enabled',
        { id: 'x' },
        { origin: 'null' },
      ),
      403,
    ],
    [
      request('/auriga/local-resources/detail', {
        id: 'x',
        url: 'https://example.com',
      }),
      400,
    ],
    [request('/auriga/local-resources/detail', '{'), 400],
    [request('/auriga/local-resources/detail', 'x'.repeat(16385)), 400],
  ]) {
    const res = response();
    await handler(req, res);
    assert.equal(res.status, status);
  }
  assert.equal(calls, 0);
});
test('native 401/403 and stopped/revoked requests fail closed without leaking host error detail', async () => {
  for (const status of [401, 403]) {
    const res = response();
    await createLocalResourceHandler({
      auth: { requestRejection: () => status },
      service: {
        list() {
          throw new Error('secret path');
        },
      },
    })(request('/auriga/local-resources'), res);
    assert.equal(res.status, status);
    assert.doesNotMatch(res.body, /secret path/);
  }
  let authorized = true;
  const handler = createLocalResourceHandler({
    auth: { requestRejection: () => (authorized ? undefined : 401) },
    service: {
      async list() {
        authorized = false;
        return { mustNotLeak: true };
      },
    },
  });
  const res = response();
  await handler(request('/auriga/local-resources'), res);
  assert.equal(res.status, 401);
  assert.doesNotMatch(res.body, /mustNotLeak/);
  const stopped = response();
  await createLocalResourceHandler({
    alive: () => false,
    auth: {},
    service: {},
  })(request('/auriga/local-resources'), stopped);
  assert.equal(stopped.status, 503);
});
test('Cordis route injection is local-only and teardown removes the route and seals late handlers', async () => {
  const effects = [];
  let route,
    registered = false;
  const ctx = {
    connection: { requestRejection() {} },
    webServer: {
      register(value) {
        route = value;
        registered = true;
        return () => {
          registered = false;
        };
      },
    },
    effect(start) {
      effects.push(start());
    },
    inject(keys, apply) {
      assert.deepEqual(keys, ['pluginManager']);
      apply({
        pluginManager: {
          async listPlugins() {
            return [];
          },
        },
        effect(start) {
          effects.push(start());
        },
      });
    },
  };
  installLocalResources(ctx, 'managed');
  assert.equal(route.path, '/auriga/local-resources');
  assert.equal(registered, true);
  const res = response();
  await route.handler(request(route.path), res);
  assert.equal(res.status, 200);
  for (const effect of effects.reverse()) effect?.();
  assert.equal(registered, false);
  const late = response();
  await route.handler(request(route.path), late);
  assert.equal(late.status, 503);
});
