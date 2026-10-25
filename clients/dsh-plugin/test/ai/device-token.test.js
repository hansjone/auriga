import test from 'node:test';
import assert from 'node:assert/strict';
import { generateKeyPairSync, sign } from 'node:crypto';
import { createPlatform, platformConfig } from '../../src/local/platform.js';

test('AI device JWT: actual ES256 signature, fixed target, owner, tenant, device and bounded lifetime', async (t) => {
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
  t.mock.method(globalThis, 'fetch', async (url, init) => {
    assert.equal(String(url), 'https://portal.example.com/oauth2/jwks');
    assert.equal(init.redirect, 'error');
    return Response.json({
      keys: [{ ...publicKey.export({ format: 'jwk' }), kid: 'fake-key' }],
    });
  });
  const now = Math.floor(Date.now() / 1000);
  t.mock.method(Date, 'now', () => now * 1000);
  const claims = {
    iss: config.issuer,
    sub: 'owner',
    tid: 'tenant',
    did: 'device',
    aud: 'auriga.ai-gateway',
    ptyp: 'device',
    ctx: 'device',
    scope: 'auriga.connect',
    perms: ['auriga.connect.models.invoke'],
    iat: now,
    exp: now + 120,
  };
  const token = (patch = {}, headers = {}) => {
    const body = [
      { alg: 'ES256', kid: 'fake-key', typ: 'at+jwt', ...headers },
      { ...claims, ...patch },
    ]
      .map((v) => Buffer.from(JSON.stringify(v)).toString('base64url'))
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
  assert.deepEqual(await platform.verifyAi(token(), claims), claims);
  for (const patch of [{ iat: now + 5 }, { exp: now + 300 }])
    assert.deepEqual(
      await platform.verifyAi(token(patch), claims),
      { ...claims, ...patch },
      JSON.stringify(patch),
    );
  for (const patch of [
    { aud: 'auriga' },
    { aud: ['auriga.ai-gateway'] },
    { tid: 'other' },
    { sub: 'other' },
    { did: 'other' },
    { iss: 'https://other.example.com' },
    { ctx: 'instance' },
    { sid: 'session' },
    { instanceId: 'instance' },
    { exp: now + 301 },
    { exp: now },
    { iat: now + 6 },
    { perms: ['auriga.connect.collab'] },
    { perms: ['auriga.connect.models.invoke', 'admin'] },
    { perms: [] },
  ])
    await assert.rejects(
      platform.verifyAi(token(patch), claims),
      /invalid device|identity changed/,
      JSON.stringify(patch),
    );
  await assert.rejects(
    platform.verifyAi(token({}, { typ: 'JWT' }), claims),
    /invalid device/,
  );
  await assert.rejects(
    platform.verifyDevice(token(), claims),
    /invalid device/,
  );
});
