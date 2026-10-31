import test from 'node:test';
import assert from 'node:assert/strict';
import { generateKeyPairSync, sign } from 'node:crypto';
import { createPlatform, platformConfig } from '../src/local/platform.js';

test('device and AI verification apply the same bounded clock tolerance to iat and nbf', async (t) => {
  const config = platformConfig({
    platformOrigin: 'https://portal.example.com',
    issuer: 'https://portal.example.com',
    clientId: 'auriga-dsh',
    providerId: 'team',
  });
  const platform = createPlatform(config);
  const { privateKey, publicKey } = generateKeyPairSync('ec', {
    namedCurve: 'P-256',
  });
  const now = 1_800_000_000;
  t.mock.method(Date, 'now', () => now * 1000);
  t.mock.method(globalThis, 'fetch', async (url) => {
    assert.equal(String(url), 'https://portal.example.com/oauth2/jwks');
    return Response.json({
      keys: [{ ...publicKey.export({ format: 'jwk' }), kid: 'test-clock-key' }],
    });
  });
  for (const ai of [false, true]) {
    const claims = {
      iss: config.issuer,
      sub: 'owner',
      tid: 'tenant',
      did: 'device',
      aud: ai ? 'auriga.ai-gateway' : 'auriga',
      ptyp: 'device',
      ctx: 'device',
      scope: 'auriga.connect',
      ...(ai ? { perms: ['auriga.connect.models.invoke'] } : {}),
      iat: now,
      exp: now + 120,
    };
    const token = (patch) => {
      const body = [
        { alg: 'ES256', kid: 'test-clock-key', typ: 'at+jwt' },
        { ...claims, ...patch },
      ]
        .map((value) =>
          Buffer.from(JSON.stringify(value)).toString('base64url'),
        )
        .join('.');
      return (
        body +
        '.' +
        sign('sha256', Buffer.from(body), {
          key: privateKey,
          dsaEncoding: 'ieee-p1363',
        }).toString('base64url')
      );
    };
    const verify = (patch) =>
      ai
        ? platform.verifyAi(token(patch), claims)
        : platform.verifyDevice(token(patch), claims);
    for (const offset of [0, 2, 5, 6, 59, 60]) {
      const patch = { iat: now + offset, nbf: now + offset };
      assert.deepEqual(await verify(patch), { ...claims, ...patch });
    }
    for (const patch of [
      { nbf: now + 61 },
      { nbf: now + 3600 },
      { nbf: now + 0.5 },
      { nbf: String(now) },
      { nbf: null },
      { iat: now + 61, nbf: now + 2 },
      { exp: now, nbf: now + 2 },
      { exp: now - 1 },
    ])
      await assert.rejects(verify(patch), /invalid device/);
  }
});

test('clock tolerance is configurable without waiting or weakening signature and identity checks', async (t) => {
  const config = platformConfig({
    platformOrigin: 'https://portal.example.com',
    issuer: 'https://portal.example.com',
    clientId: 'auriga-dsh',
    providerId: 'team',
  });
  const { privateKey, publicKey } = generateKeyPairSync('ec', {
    namedCurve: 'P-256',
  });
  const now = 1800000000;
  t.mock.method(Date, 'now', () => now * 1000);
  t.mock.method(globalThis, 'fetch', async () =>
    Response.json({
      keys: [{ ...publicKey.export({ format: 'jwk' }), kid: 'clock' }],
    }),
  );
  const token = (patch = {}, key = privateKey) => {
    const claims = {
      iss: config.issuer,
      aud: 'auriga',
      sub: 'owner',
      tid: 'tenant',
      did: 'device',
      ptyp: 'device',
      ctx: 'device',
      scope: 'auriga.connect',
      iat: now + 6,
      nbf: now + 6,
      exp: now + 120,
      ...patch,
    };
    const body = [{ alg: 'ES256', kid: 'clock', typ: 'at+jwt' }, claims]
      .map((v) => Buffer.from(JSON.stringify(v)).toString('base64url'))
      .join('.');
    return (
      body +
      '.' +
      sign('sha256', Buffer.from(body), {
        key,
        dsaEncoding: 'ieee-p1363',
      }).toString('base64url')
    );
  };
  const platform = createPlatform(config, { clockToleranceSeconds: 60 });
  assert.equal((await platform.verifyDevice(token())).iat, now + 6);
  assert.equal(
    Date.now(),
    now * 1000,
    'tolerance must not sleep or change real time',
  );
  await assert.rejects(
    createPlatform(config, { clockToleranceSeconds: 0 }).verifyDevice(token()),
    /invalid device/,
  );
  await assert.rejects(
    platform.verifyDevice(
      token({}, generateKeyPairSync('ec', { namedCurve: 'P-256' }).privateKey),
    ),
    /signature/,
  );
  for (const patch of [
    { iat: now + 61 },
    { nbf: now + 61 },
    { iss: 'https://other.example.com' },
    { exp: now },
    { nbf: String(now) },
  ])
    await assert.rejects(platform.verifyDevice(token(patch)), /invalid device/);
  await assert.rejects(
    platform.verifyDevice(token(), {
      tid: 'other',
      sub: 'owner',
      did: 'device',
    }),
    /identity changed/,
  );
  for (const value of [-1, 121, NaN, 0.5, '60'])
    assert.throws(
      () => createPlatform(config, { clockToleranceSeconds: value }),
      /clock/,
    );
});
