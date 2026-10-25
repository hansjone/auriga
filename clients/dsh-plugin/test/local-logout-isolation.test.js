import { test } from 'node:test';
import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import { Readable } from 'node:stream';
import { createConnectRoutes } from '../src/local/routes.js';
import { runtimePeer } from './local-runtime-peer.js';

test('anonymous B cancels with its own CSRF while authenticated A keeps device, Team, HTTP and WS without revoke', async (t) => {
  const { runtime, request: a, registered, requests } = await runtimePeer(t);
  const stream = new EventEmitter(),
    socket = new EventEmitter();
  let httpClosed = 0,
    wsClosed = 0;
  stream.destroy = () => {
    httpClosed++;
    stream.emit('close');
  };
  socket.destroy = () => {
    wsClosed++;
    socket.emit('close');
  };
  runtime.bindHttpResponse(a, stream);
  runtime.bindSocket({
    ...a,
    headers: { ...a.headers, upgrade: 'websocket' },
    socket,
  });
  await runtime.vault.update((s) => {
    s.threads.existing = { sessionId: 'source-A', bindingId: 'binding-A' };
    s.deliveries.existing = {
      sessionId: 'source-A',
      messageId: 'message-A',
      accepted: true,
    };
  });
  const before = runtime.vault.snapshot(),
    status = runtime.status(a);
  const prepared = runtime.prepare({ headers: {} });
  const bCookie = prepared.setCookie.split(';')[0];
  const b = {
    headers: { cookie: bCookie, 'x-auriga-csrf': prepared.body.csrf },
  };
  assert.equal(runtime.resolve(b), null);
  assert.equal(runtime.checkCsrf(b), true);
  const routes = createConnectRoutes({
    runtime,
    admission: {
      nativeAuth: { requestRejection: () => undefined },
      sameOrigin: () => true,
    },
  });
  const req = Readable.from([Buffer.from('{}')]);
  Object.assign(req, {
    method: 'POST',
    url: '/auriga/local-login/logout',
    headers: { ...b.headers, 'content-type': 'application/json' },
  });
  const res = new EventEmitter();
  res.writeHead = (code) => {
    res.status = code;
  };
  res.end = (body) => {
    res.body = JSON.parse(body);
    res.writableEnded = true;
  };
  await routes(req, res);
  assert.equal(res.status, 200);
  assert.equal(res.body.loggedIn, false);
  assert.deepEqual(runtime.vault.snapshot(), before);
  assert.deepEqual(runtime.status(a), status);
  assert.equal(runtime.isReady(), true);
  assert.equal(registered.size, 4);
  assert.equal(httpClosed, 0);
  assert.equal(wsClosed, 0);
  assert.equal(requests.includes('/oauth2/revoke'), false);
  assert.equal(runtime.checkCsrf(b), false);
  assert.notEqual(runtime.prepare(b).body.csrf, prepared.body.csrf);
  await runtime.logout(a);
  assert.equal(runtime.resolve(a), null);
  assert.equal(httpClosed, 1);
  assert.equal(wsClosed, 1);
  assert.equal(registered.size, 0);
  assert.equal(requests.filter((path) => path === '/oauth2/revoke').length, 1);
});

test('a browser session for another device cannot tombstone the current device', async (t) => {
  const { runtime, request, registered, requests } = await runtimePeer(t);
  await runtime.vault.update((s) => {
    s.credential.claims.did = 'replacement-device';
  });
  const before = runtime.vault.snapshot();
  await assert.rejects(runtime.logout(request), /device ownership/);
  assert.deepEqual(runtime.vault.snapshot(), before);
  assert.equal(registered.size, 4);
  assert.equal(requests.includes('/oauth2/revoke'), false);
});

test('only authenticated status exposes unsupported delivery and unresolved source counts', async (t) => {
  const { runtime, request } = await runtimePeer(t);
  await runtime.vault.update((s) => {
    s.deliveries.failed = {
      accepted: false,
      status: 'unsupported',
      reason: 'subagent_origin',
    };
    s.pendingOpens = { source: { sessionId: 'trusted-source' } };
    s.openOutcomes = { expired: { status: 'result_unknown' } };
  });
  const connection = runtime.status(request).connection;
  assert.equal(connection.unsupported, 1);
  assert.equal(connection.pendingSourceOpens, 1);
  assert.equal(connection.unknownSourceOpens, 1);
  assert.equal(connection.received, 0);
  assert.equal(runtime.status({ headers: {} }).connection, null);
});
