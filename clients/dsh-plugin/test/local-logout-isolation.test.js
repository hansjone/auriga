import { ownerRequest } from './owner-request.js';
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import { Readable } from 'node:stream';
import { createConnectRoutes } from '../src/local/routes.js';
import { runtimePeer } from './local-runtime-peer.js';

test('R4 another admitted owner cancels a form without revoking shared device or Team', async (t) => {
  const { runtime, request: a, registered, requests } = await runtimePeer(t);
  const before = runtime.vault.snapshot();
  const status = runtime.status(a);
  const routes = createConnectRoutes({
    runtime,
    admission: {
      nativeAuth: {
        requestRejection: (req) => {
          ownerRequest(runtime, req);
          return undefined;
        },
      },
      sameOrigin: () => true,
    },
  });
  const req = Readable.from([Buffer.from('{}')]);
  Object.assign(req, {
    method: 'POST',
    url: '/auriga/local-login/cancel',
    headers: {
      'x-auriga-csrf': status.csrf,
      'content-type': 'application/json',
    },
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
  assert.equal(res.body.cancelled, true);
  assert.deepEqual(runtime.vault.snapshot(), before);
  assert.deepEqual(runtime.status(a), status);
  assert.equal(runtime.isReady(), true);
  assert.equal(registered.size, 4);
  assert.equal(requests.includes('/oauth2/revoke'), false);
  await runtime.logout(a);
  assert.equal(runtime.resolve(a), null);
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
