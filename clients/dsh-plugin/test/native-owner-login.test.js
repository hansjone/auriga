import { test } from 'node:test';
import assert from 'node:assert/strict';
import { runtimePeer } from './local-runtime-peer.js';
import { installLocalAdmission } from '../src/local/host.js';

test('admitted owner resumes without plugin cookies; logout and expiry fence native business access', async (t) => {
  const { runtime, credential } = await runtimePeer(t);
  const peer = { principal: { kind: 'owner' } };
  const request = runtime.nativeRequest(peer);
  assert.equal(request.headers.cookie, undefined);
  const prepared = runtime.prepare(request);
  assert.equal(prepared.setCookie, undefined);
  assert.equal(prepared.body.loggedIn, false);
  const loggedIn = await runtime.login(request, { action: 'resume' });
  assert.equal(loggedIn.setCookie, undefined);
  assert.equal(runtime.resolveNative(peer).subject, credential.claims.sub);
  const business = { headers: {} };
  assert.ok(runtime.bindNativeRequest(peer, business));
  assert.equal(runtime.status(business).loggedIn, true);
  const other = { principal: { kind: 'owner' } };
  assert.equal(runtime.resolveNative(other), null);
  t.mock.timers.enable({ apis: ['Date'], now: Date.now() + 1800001 });
  assert.equal(runtime.resolveNative(peer), null);
  assert.equal(runtime.bindNativeRequest(peer, { headers: {} }), null);
  await runtime.logout(request);
  assert.equal(runtime.resolveNative(peer), null);
  assert.equal(runtime.vault.snapshot().credential, null);
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
