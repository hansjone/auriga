import test from 'node:test';
import assert from 'node:assert/strict';
import { generateKeyPairSync, sign } from 'node:crypto';
import { setImmediate } from 'node:timers/promises';
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
    for (const offset of [0, 2, 5]) {
      const patch = { iat: now + offset, nbf: now + offset };
      assert.deepEqual(await verify(patch), { ...claims, ...patch });
    }
    for (const patch of [
      { nbf: now + 6 },
      { nbf: now + 3600 },
      { nbf: now + 0.5 },
      { nbf: String(now) },
      { nbf: null },
      { iat: now + 6, nbf: now + 2 },
      { exp: now, nbf: now + 2 },
      { exp: now - 1 },
    ])
      await assert.rejects(verify(patch), /invalid device/);
  }
});

test('native login waits for a signed near-future token, then applies the original clock checks', async (t) => {
  const config = platformConfig({
    platformOrigin: 'https://portal.example.com',
    issuer: 'https://portal.example.com',
    clientId: 'auriga-dsh',
    providerId: 'team',
  });
  const { privateKey, publicKey } = generateKeyPairSync('ec', {
    namedCurve: 'P-256',
  });
  let now = 1_800_000_000;
  t.mock.timers.enable({ apis: ['Date', 'setTimeout'], now: now * 1000 });
  let keyReads = 0;
  t.mock.method(globalThis, 'fetch', async () => {
    keyReads++;
    return Response.json({
      keys: [{ ...publicKey.export({ format: 'jwk' }), kid: 'clock-wait' }],
    });
  });
  const platform = createPlatform(config, { waitForValidity: true });
  const token = (ai, patch = {}, key = privateKey) => {
    const claims = {
      iss: config.issuer,
      aud: ai ? 'auriga.ai-gateway' : 'auriga',
      sub: 'owner',
      tid: 'tenant',
      did: 'device',
      ptyp: 'device',
      ctx: 'device',
      scope: 'auriga.connect',
      iat: now + 6,
      nbf: now + 6,
      exp: now + 306,
      ...(ai ? { perms: ['auriga.connect.models.invoke'] } : {}),
      ...patch,
    };
    const body = [{ alg: 'ES256', kid: 'clock-wait', typ: 'at+jwt' }, claims]
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
  for (const ai of [false, true]) {
    now = Math.floor(Date.now() / 1000);
    const before = keyReads;
    let done = false;
    const pending = (
      ai ? platform.verifyAi(token(ai)) : platform.verifyDevice(token(ai))
    ).then((v) => {
      done = true;
      return v;
    });
    for (let i = 0; i < 20 && keyReads === before; i++) await setImmediate();
    await setImmediate();
    assert.ok(
      keyReads > before,
      'independent JWKS signature validation precedes waiting',
    );
    assert.equal(done, false);
    t.mock.timers.tick(5999);
    await setImmediate();
    assert.equal(
      done,
      false,
      'future credentials cannot be returned before they become current',
    );
    t.mock.timers.tick(1);
    const claims = await pending;
    assert.equal(claims.iat, Math.floor(Date.now() / 1000));
  }
  now = Math.floor(Date.now() / 1000);
  const other = generateKeyPairSync('ec', { namedCurve: 'P-256' }).privateKey;
  await assert.rejects(
    platform.verifyDevice(token(false, {}, other)),
    /invalid device signature/,
  );
  for (const patch of [
    { iat: now + 31, nbf: now + 31 },
    { iss: 'https://other.example.com' },
    { exp: now },
    { nbf: String(now + 6) },
  ])
    await assert.rejects(
      platform.verifyDevice(token(false, patch)),
      /invalid device/,
    );
  await assert.rejects(
    platform.verifyDevice(token(false), {
      tid: 'other',
      sub: 'owner',
      did: 'device',
    }),
    /identity changed/,
  );
  const expiring = platform.verifyDevice(token(false, { exp: now + 3 }));
  const rejected = assert.rejects(expiring, /invalid device credential/);
  for (let i = 0; i < 5; i++) await setImmediate();
  t.mock.timers.tick(6000);
  await rejected;
});
