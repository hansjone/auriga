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
