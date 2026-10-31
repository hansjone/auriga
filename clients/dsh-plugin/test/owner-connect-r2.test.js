import { test } from 'node:test';
import assert from 'node:assert/strict';
import { installLocalAdmission } from '../src/local/host.js';
import { runtimePeer } from './local-runtime-peer.js';

for (const kind of ['browser', 'api'])
  test(`R2 ${kind} admitted owner can use local DSH before platform login and after expiry`, () => {
    const peer = { principal: { kind: 'owner' } };
    const connection = {
      admit: (req) =>
        req.headers.cookie === 'dsh=valid' ? { peer } : { rejection: 401 },
      authorizeIndex: () => kind === 'browser',
      isTrusted: () => true,
    };
    const gate = installLocalAdmission(
      connection,
      {
        isConfigured: () => true,
        resolve: () => null,
        installHttpTracking: () => () => {},
      },
      { protocol: 'http:' },
    );
    try {
      for (const upgrade of [undefined, 'websocket']) {
        const request = {
          method: 'POST',
          url: '/api/session/create',
          headers: { cookie: 'dsh=valid', upgrade },
        };
        assert.equal(connection.admit(request).peer, peer);
        assert.equal(
          connection.admit({
            ...request,
            headers: { 'x-auriga-native': 'true' },
          }).rejection,
          401,
        );
      }
    } finally {
      gate.dispose();
    }
  });

test('R2 non-owner admission does not acquire local owner authority', () => {
  const connection = {
    admit: () => ({ peer: { principal: { kind: 'guest' } } }),
    authorizeIndex: () => false,
    isTrusted: () => true,
  };
  const gate = installLocalAdmission(
    connection,
    {
      isConfigured: () => false,
      installHttpTracking: () => () => {},
    },
    { protocol: 'http:' },
  );
  try {
    assert.equal(connection.admit({ headers: {} }).rejection, 403);
  } finally {
    gate.dispose();
  }
});

test('R2 platform identity is shared by all DSH owners and logout leaves no platform identity', async (t) => {
  const { runtime } = await runtimePeer(t);
  const first = runtime.nativeRequest({ principal: { kind: 'owner' } });
  const second = runtime.nativeRequest({ principal: { kind: 'owner' } });
  assert.equal(runtime.status(first).loggedIn, true);
  assert.equal(runtime.status(second).loggedIn, true);
  await runtime.logout(second);
  assert.equal(runtime.status(first).loggedIn, false);
  assert.equal(runtime.resetEmergency, undefined);
});

test('R2 discovery accepts only the team-account form and never offers emergency login', async (t) => {
  const { runtime } = await runtimePeer(t);
  const request = runtime.nativeRequest({ principal: { kind: 'owner' } });
  await runtime.logout(request);
  runtime.prepare(request);
  t.mock.method(globalThis, 'fetch', async (url, init) => {
    if (new URL(url).pathname.endsWith('/providers'))
      return Response.json({
        items: [
          { id: 'emergency', connectorType: 'emergency', primaryStep: 'form' },
          { id: 'team', connectorType: 'team-account', primaryStep: 'form' },
        ],
      });
    assert.equal(JSON.parse(init.body).providerId, 'team');
    return Response.json({
      id: 'team-tx',
      nativeDevice: {
        deviceCode: 'synthetic',
        expiresAt: new Date(Date.now() + 60000).toISOString(),
        interval: 1,
      },
      step: { type: 'form', formId: 'team', fields: [] },
    });
  });
  const otherOwnerRequest = runtime.nativeRequest({
    principal: { kind: 'owner' },
  });
  const result = await runtime.login(otherOwnerRequest, { action: 'begin' });
  assert.equal(result.step.formId, 'team');
  assert.equal(result.step.fields.length, 0);
  // Leaving an optional form does not log out the shared owner. Reopening it
  // must continue the same transaction rather than reporting an outage.
  const reopened = await runtime.login(request, { action: 'begin' });
  assert.deepEqual(reopened.step, result.step);
});

test('R3 cancellation finishing late cannot erase a new owner login attempt', async (t) => {
  const { runtime } = await runtimePeer(t);
  const request = runtime.nativeRequest({ principal: { kind: 'owner' } });
  await runtime.logout(request);
  runtime.prepare(request);
  let release;
  const pending = new Promise((resolve) => {
    release = resolve;
  });
  t.mock.method(globalThis, 'fetch', () => pending);
  const old = runtime.login(request, { action: 'begin' });
  const rejected = assert.rejects(old, /superseded/);
  await runtime.cancelAttempt(request);
  runtime.prepare(request);
  const fresh = runtime.attemptToken(request);
  release(Response.json({ items: [] }));
  await rejected;
  assert.equal(runtime.attemptToken(request), fresh);
});
