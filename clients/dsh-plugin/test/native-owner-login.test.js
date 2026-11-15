import { test } from 'node:test';
import assert from 'node:assert/strict';
import { shellRuntimePeer } from './shell-runtime-peer.js';
import { installLocalAdmission } from '../src/local/host.js';

test('admitted owner resumes without plugin cookies; logout and expiry fence native business access', async (t) => {
  t.mock.timers.enable({ apis: ['Date'], now: Date.now() });
  const { runtime, connection } = await shellRuntimePeer(t);
  const peer = { principal: { kind: 'owner' } };
  const request = runtime.nativeRequest(peer);
  assert.equal(request.headers.cookie, undefined);
  const prepared = runtime.prepare(request);
  assert.equal(prepared.setCookie, undefined);
  assert.equal(prepared.body.loggedIn, true);

  assert.equal(
    runtime.resolve(runtime.nativeRequest(peer)).ownerId,
    connection.identity.memberId,
  );
  const business = { headers: {} };
  assert.ok(runtime.bindNativeRequest(peer, business));
  assert.equal(runtime.status(business).loggedIn, true);
  const other = { principal: { kind: 'owner' } };
  assert.equal(
    runtime.resolve(runtime.nativeRequest(other)).ownerId,
    connection.identity.memberId,
  );
  t.mock.timers.tick(30001);
  assert.equal(runtime.resolve(runtime.nativeRequest(peer)), undefined);
  assert.equal(runtime.bindNativeRequest(peer, { headers: {} }), undefined);
  await assert.rejects(runtime.command(request, 'logout'), {
    code: 'common.unavailable',
  });
  connection.transition('authenticated');
  await runtime.command(request, 'logout');
  assert.equal(runtime.resolve(runtime.nativeRequest(peer)), undefined);
  assert.equal(runtime.vault.snapshot().credential, undefined);
});

test('native RPC bootstrap needs a real admitted Peer; browser native-looking headers cannot select it', () => {
  const peer = { principal: { kind: 'owner' } };
  let signedIn = false;
  const connection = {
    admit: (req) =>
      req.headers.cookie === 'native=valid' ? { peer } : { rejection: 401 },
    authorizeIndex: () => false,
    isTrusted: () => true,
  };
  const runtime = {
    isConfigured: () => true,
    installHttpTracking: () => () => {},
    resolve: () => null,
    resolveNative: (p) =>
      signedIn && p === peer ? { key: 'native-owner' } : null,
    bindNativeRequest: (p) => signedIn && p === peer,
    bindSocket() {},
  };
  const admission = installLocalAdmission(connection, runtime, {
    protocol: 'http:',
  });
  const request = (cookie, path) => ({
    url: path,
    method: 'POST',
    headers: {
      host: 'localhost:3000',
      cookie,
      'content-type': 'application/json',
      'x-auriga-native': 'true',
    },
    rawHeaders: ['Host', 'localhost:3000'],
  });
  try {
    const nativePath = '/auriga-native/aurigaAuth/status';
    assert.equal(connection.admit(request('', nativePath)).rejection, 401);
    assert.equal(
      connection.admit(request('native=valid', nativePath)).peer,
      peer,
    );
    assert.equal(
      connection.admit(request('native=valid', '/api/session/create')).peer,
      peer,
    );
    signedIn = true;
    assert.equal(
      connection.admit(request('native=valid', '/api/session/create')).peer,
      peer,
    );
    signedIn = false;
    assert.equal(
      connection.admit(request('native=valid', '/api/session/create')).peer,
      peer,
    );
  } finally {
    admission.dispose();
  }
});
