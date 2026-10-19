import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createPublicKey } from 'node:crypto';
import {
  verifyIdentityHeader,
  createReplayCache,
} from '../src/managed/door.js';
import {
  makeIdentityKeys,
  signIdentity,
  INSTANCE_ID,
  OWNER_ID,
  OTHER_OWNER,
} from './managed-helpers.js';

function setup() {
  const keys = makeIdentityKeys();
  const replay = createReplayCache();
  const getPublicKey = async (kid) => {
    const jwk = keys.keySet.keys.find((k) => k.kid === kid);
    if (!jwk) return null;
    return createPublicKey({
      key: { kty: 'OKP', crv: 'Ed25519', x: jwk.x },
      format: 'jwk',
    });
  };
  return { keys, replay, getPublicKey };
}

test('door rejects missing header with common.unauthenticated', async () => {
  const { getPublicKey, replay } = setup();
  const result = await verifyIdentityHeader({
    token: undefined,
    method: 'GET',
    path: '/app',
    instanceId: INSTANCE_ID,
    ownerId: OWNER_ID,
    getPublicKey,
    replay,
  });
  assert.equal(result.ok, false);
  assert.equal(result.code, 'common.unauthenticated');
  assert.equal(result.reason, 'missing');
});

test('door rejects bad signature with common.unauthenticated', async () => {
  const { keys, getPublicKey, replay } = setup();
  const { token } = signIdentity(keys.privateKey, keys.kid, {
    htm: 'GET',
    htu: '/app',
  });
  const tampered = token.slice(0, -4) + 'AAAA';
  const result = await verifyIdentityHeader({
    token: tampered,
    method: 'GET',
    path: '/app',
    instanceId: INSTANCE_ID,
    ownerId: OWNER_ID,
    getPublicKey,
    replay,
  });
  assert.equal(result.ok, false);
  assert.equal(result.code, 'common.unauthenticated');
  assert.equal(result.reason, 'signature');
});

test('door rejects unknown kid with common.unauthenticated', async () => {
  const { keys, replay } = setup();
  const { token } = signIdentity(keys.privateKey, 'unknown-kid', {
    htm: 'GET',
    htu: '/app',
  });
  const result = await verifyIdentityHeader({
    token,
    method: 'GET',
    path: '/app',
    instanceId: INSTANCE_ID,
    ownerId: OWNER_ID,
    getPublicKey: async () => null,
    replay,
  });
  assert.equal(result.ok, false);
  assert.equal(result.code, 'common.unauthenticated');
  assert.equal(result.reason, 'kid_unknown');
});

test('door rejects expired token with common.unauthenticated', async () => {
  const { keys, getPublicKey, replay } = setup();
  const now = Math.floor(Date.now() / 1000);
  const { token } = signIdentity(keys.privateKey, keys.kid, {
    htm: 'GET',
    htu: '/app',
    iat: now - 120,
    exp: now - 60,
  });
  const result = await verifyIdentityHeader({
    token,
    method: 'GET',
    path: '/app',
    instanceId: INSTANCE_ID,
    ownerId: OWNER_ID,
    getPublicKey,
    replay,
    now: () => now,
  });
  assert.equal(result.ok, false);
  assert.equal(result.code, 'common.unauthenticated');
  assert.equal(result.reason, 'expired');
});

test('door rejects replayed jti with common.unauthenticated', async () => {
  const { keys, getPublicKey, replay } = setup();
  const { token, claims } = signIdentity(keys.privateKey, keys.kid, {
    htm: 'GET',
    htu: '/app',
    jti: 'fixed-jti-replay-1',
  });
  const first = await verifyIdentityHeader({
    token,
    method: 'GET',
    path: '/app',
    instanceId: INSTANCE_ID,
    ownerId: OWNER_ID,
    getPublicKey,
    replay,
  });
  assert.equal(first.ok, true);
  assert.equal(first.claims.jti, claims.jti);

  const second = await verifyIdentityHeader({
    token,
    method: 'GET',
    path: '/app',
    instanceId: INSTANCE_ID,
    ownerId: OWNER_ID,
    getPublicKey,
    replay,
  });
  assert.equal(second.ok, false);
  assert.equal(second.code, 'common.unauthenticated');
  assert.equal(second.reason, 'replay');
});

test('door rejects non-owner subject with common.unauthenticated', async () => {
  const { keys, getPublicKey, replay } = setup();
  const { token } = signIdentity(keys.privateKey, keys.kid, {
    htm: 'GET',
    htu: '/app',
    sub: OTHER_OWNER,
  });
  const result = await verifyIdentityHeader({
    token,
    method: 'GET',
    path: '/app',
    instanceId: INSTANCE_ID,
    ownerId: OWNER_ID,
    getPublicKey,
    replay,
  });
  assert.equal(result.ok, false);
  assert.equal(result.code, 'common.unauthenticated');
  assert.equal(result.reason, 'subject');
});

test('door accepts valid identity header', async () => {
  const { keys, getPublicKey, replay } = setup();
  const { token } = signIdentity(keys.privateKey, keys.kid, {
    htm: 'POST',
    htu: '/rpc',
  });
  const result = await verifyIdentityHeader({
    token,
    method: 'POST',
    path: '/rpc',
    instanceId: INSTANCE_ID,
    ownerId: OWNER_ID,
    getPublicKey,
    replay,
  });
  assert.equal(result.ok, true);
  assert.equal(result.claims.sub, OWNER_ID);
  assert.equal(result.claims.aud, INSTANCE_ID);
});
