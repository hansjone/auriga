// @ts-nocheck
import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  createResourceHandler,
  resourcePath,
} from '../../src/resources/channel.js';

const path = '/api/resource/v1/packages';
test('catalogue permits only fixed readers and bounded pagination', () => {
  for (const value of [
    path,
    path + '?limit=50&cursor=next',
    path + '/01900000-0000-7000-8000-000000000001/versions',
  ])
    assert.equal(resourcePath(value), value);
  for (const value of [
    '/api/iam/v1/me',
    '//example.com',
    path + '/..',
    path + '%2f',
    path + '?token=secret',
    path + '?limit=999',
    path + '?limit=1&limit=2',
    path + '#fragment',
  ])
    assert.throws(() => resourcePath(value));
});

function response() {
  return {
    status: 0,
    headers: {},
    body: '',
    writeHead(status, headers) {
      this.status = status;
      this.headers = headers;
    },
    end(body = '') {
      this.body = body;
    },
  };
}
test('local channel uses current runtime identity and never forwards browser credentials', async () => {
  let calls = 0;
  const identity = { subject: 'alice' };
  const runtime = {
    resolve: () => identity,
    getAccess: async () => 'private-token',
    config: { platformOrigin: 'https://platform.example.com' },
  };
  const handler = createResourceHandler({
    auth: { requestRejection: () => undefined },
    current: () => runtime,
    fetchImpl: async (url, init) => {
      calls++;
      assert.equal(String(url), 'https://platform.example.com' + path);
      assert.equal(init.headers.authorization, 'Bearer private-token');
      assert.equal(init.headers.cookie, undefined);
      assert.equal(init.redirect, 'error');
      return Response.json({ items: [] });
    },
  });
  const req = {
    method: 'GET',
    url: '/auriga/resources' + path,
    headers: { cookie: 'portal=secret', authorization: 'evil' },
  };
  const res = response();
  await handler(req, res);
  assert.equal(res.status, 200);
  assert.deepEqual(JSON.parse(res.body), { items: [] });
  assert.ok(!res.body.includes('private-token'));
  runtime.resolve = () => null;
  await handler(req, response());
  assert.equal(calls, 1);
});
test('channel rejects unauthenticated, non-reader and unconfigured requests before fetching', async () => {
  for (const [url, method, rejection, expected] of [
    ['/api/iam/v1/me', 'GET', undefined, 403],
    [path, 'POST', undefined, 405],
    [path, 'GET', 401, 401],
    [path, 'GET', undefined, 503],
  ]) {
    const handler = createResourceHandler({
      auth: { requestRejection: () => rejection },
      current: () => undefined,
      fetchImpl: () => assert.fail('must not fetch'),
    });
    const res = response();
    await handler({ url: '/auriga/resources' + url, method, headers: {} }, res);
    assert.equal(res.status, expected);
  }
});

test('logout during a catalogue read discards the response', async () => {
  const identity = { subject: 'alice' };
  let admitted = true;
  const runtime = {
    resolve: () => (admitted ? identity : null),
    getAccess: async () => 'secret',
    config: { platformOrigin: 'https://platform.example.com' },
  };
  const handler = createResourceHandler({
    auth: { requestRejection: () => undefined },
    current: () => runtime,
    fetchImpl: async () => {
      admitted = false;
      return Response.json({ items: [{ name: 'private-resource' }] });
    },
  });
  const res = response();
  await handler(
    { method: 'GET', url: '/auriga/resources' + path, headers: {} },
    res,
  );
  assert.equal(res.status, 401);
  assert.ok(!res.body.includes('private-resource'));
});
test('upstream errors expose only HTTP categories and oversized bodies are refused', async () => {
  const identity = {};
  const runtime = {
    resolve: () => identity,
    getAccess: async () => 'secret',
    config: { platformOrigin: 'https://platform.example.com' },
  };
  for (const [upstream, expected] of [
    [
      Response.json(
        { detail: 'private-token' },
        { status: 403, headers: { 'set-cookie': 'private-cookie' } },
      ),
      403,
    ],
    [Response.json({ data: 'x'.repeat(1048577) }), 502],
    [new Response('not json'), 502],
  ]) {
    const handler = createResourceHandler({
      auth: { requestRejection: () => undefined },
      current: () => runtime,
      fetchImpl: async () => upstream,
    });
    const res = response();
    await handler(
      { method: 'GET', url: '/auriga/resources' + path, headers: {} },
      res,
    );
    assert.equal(res.status, expected);
    assert.ok(!JSON.stringify(res).includes('private-'));
  }
});
