import { test } from 'node:test';
import assert from 'node:assert/strict';
import { installLocalAdmission } from '../src/local/host.js';
import { runtimePeer } from './local-runtime-peer.js';
test('R4 platform logout leaves the native DSH admission and transport owner intact', async (t) => {
  const { runtime, request } = await runtimePeer(t);
  const peer = { principal: { kind: 'owner' } };
  const connection = {
    admit: () => ({ peer }),
    authorizeIndex: () => true,
    isTrusted: () => true,
  };
  const admission = installLocalAdmission(connection, runtime, {
    protocol: 'http:',
  });
  try {
    await runtime.logout(request);
    assert.equal(runtime.status(request).loggedIn, false);
    assert.equal(
      connection.admit({ headers: {}, url: '/api/session/create' }).peer,
      peer,
    );
  } finally {
    admission.dispose();
  }
});
test('R4 native DSH rejection and non-owner rejection survive removed transport tracking', () => {
  const connection = {
    admit: (req) =>
      req.headers.cookie
        ? { peer: { principal: { kind: 'guest' } } }
        : { rejection: 401 },
    authorizeIndex: () => false,
    isTrusted: () => true,
  };
  const original = connection.admit;
  const admission = installLocalAdmission(
    connection,
    {},
    { protocol: 'http:' },
  );
  assert.equal(
    connection.admit({ headers: { 'x-auriga-native': 'true' } }).rejection,
    401,
  );
  assert.equal(
    connection.admit({ headers: { cookie: 'guest' } }).rejection,
    403,
  );
  admission.dispose();
  assert.equal(connection.admit, original);
});
test('R4 runtime has no browser bearer or local transport-lifetime hooks', async (t) => {
  const { runtime } = await runtimePeer(t);
  for (const name of [
    'bindHttpResponse',
    'bindSocket',
    'installHttpTracking',
    'resolveNative',
  ])
    assert.equal(runtime[name], undefined);
  assert.equal(
    runtime.resolve({ headers: { cookie: 'auriga_local_session=forged' } }),
    null,
  );
  assert.throws(() => runtime.prepare({ headers: {} }), /owner admission/);
});
