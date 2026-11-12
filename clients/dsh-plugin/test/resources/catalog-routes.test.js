import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Readable } from 'node:stream';
import { catalogRoutes } from '../../src/resources/catalog-routes.js';
import { createResourceHandler } from '../../src/resources/channel.js';
import { catalogFixture } from './catalog-fixture.js';
function response() {
  return {
    status: 0,
    body: '',
    writeHead(status) {
      this.status = status;
    },
    end(body) {
      this.body = body;
    },
  };
}
function request(body, headers = {}) {
  const req = Readable.from([Buffer.from(JSON.stringify(body))]);
  req.method = 'POST';
  req.url = '/auriga/resources/catalog/actions';
  req.headers = {
    host: 'localhost:7777',
    'content-type': 'application/json',
    'x-auriga-catalog': '1',
    origin: 'http://localhost:7777',
    ...headers,
  };
  return req;
}
test('catalog mutations require native admission, runtime admission, same-origin JSON and a strict input without renderer-selected roots or identity', async (t) => {
  const f = catalogFixture(t);
  let calls = 0;
  const input = {
    packageId: f.packageId,
    versionId: f.versionId,
    action: 'install',
    revision: 0,
    key: 'route-test',
  };
  const route = catalogRoutes({
    ...f,
    service: () => ({
      apply: async (value) => {
        calls++;
        assert.deepEqual(JSON.parse(JSON.stringify(value)), input);
        return { accepted: true };
      },
    }),
  });
  for (const [extra, headers, status] of [
    [{ root: 'C:/evil' }, {}, 400],
    [{ clientId: f.who.hostId }, {}, 400],
    [{ reportSource: 'node' }, {}, 400],
    [{}, { origin: 'https://evil.test' }, 403],
    [{}, { 'x-auriga-catalog': '' }, 403],
    [{}, { 'content-type': 'text/plain' }, 403],
    [{}, { 'sec-fetch-site': 'cross-site' }, 403],
  ]) {
    const res = response();
    await route(request({ ...input, ...extra }, headers), res);
    assert.equal(res.status, status);
  }
  assert.equal(calls, 0);
  const res = response();
  await route(request(input), res);
  assert.equal(res.status, 202);
  assert.equal(calls, 1);
  f.runtime.resolve = () => null;
  const denied = response();
  await route(request(input), denied);
  assert.equal(denied.status, 401);
  assert.equal(calls, 1);
  const native = createResourceHandler({
    auth: { requestRejection: () => 401 },
    current: f.current,
    fetchImpl: () => assert.fail('native gate must precede network'),
  });
  const rejected = response();
  await native(request(input), rejected);
  assert.equal(rejected.status, 401);
});
test('catalog readers pin skill kind and native access token, rejecting arbitrary query and download paths', async (t) => {
  const f = catalogFixture(t);
  let calls = 0;
  const route = catalogRoutes({
    ...f,
    fetchImpl: async (url, init) => {
      calls++;
      assert.equal(url.searchParams.get('kind'), 'skill');
      assert.equal(init.headers.authorization, 'Bearer fixture-token');
      assert.equal(init.headers.cookie, undefined);
      return Response.json({ items: [] });
    },
  });
  for (const path of [
    'packages?kind=plugin',
    'packages?root=C:/evil',
    'packages?limit=201',
    'packages?cursor=x&cursor=y',
    'packages%2f..',
    'member/downloads',
  ]) {
    const res = response();
    await route(
      { method: 'GET', url: '/auriga/resources/catalog/' + path, headers: {} },
      res,
    );
    assert.ok([400, 404].includes(res.status), path + ':' + res.status);
  }
  assert.equal(calls, 0);
  const res = response();
  await route(
    {
      method: 'GET',
      url: '/auriga/resources/catalog/packages?limit=50',
      headers: { cookie: 'browser=secret' },
    },
    res,
  );
  assert.equal(res.status, 200);
  assert.equal(calls, 1);
});
