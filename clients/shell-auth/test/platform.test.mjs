import { test } from 'node:test';
import assert from 'node:assert/strict';
import { generateKeyPairSync, sign, randomUUID } from 'node:crypto';
import { createPlatform, trustedConfig } from '../platform.mjs';
test('device credentials require real signature, exact issuer/audience and device-only identity', async () => {
  const keys = generateKeyPairSync('ec', { namedCurve: 'prime256v1' }),
    config = trustedConfig({ platformOrigin: 'https://example.com' });
  const jwk = {
    ...keys.publicKey.export({ format: 'jwk' }),
    kid: 'test',
    alg: 'ES256',
    use: 'sig',
  };
  const claims = {
    iss: config.issuer,
    aud: 'auriga',
    ptyp: 'device',
    ctx: 'device',
    scope: 'auriga.connect',
    tid: randomUUID(),
    sub: randomUUID(),
    did: randomUUID(),
    iat: Math.floor(Date.now() / 1000),
    exp: Math.floor(Date.now() / 1000) + 300,
  };
  const token = (value) => {
    const body = [
      Buffer.from(JSON.stringify({ alg: 'ES256', kid: 'test' })).toString(
        'base64url',
      ),
      Buffer.from(JSON.stringify(value)).toString('base64url'),
    ].join('.');
    return (
      body +
      '.' +
      sign('sha256', Buffer.from(body), {
        key: keys.privateKey,
        dsaEncoding: 'ieee-p1363',
      }).toString('base64url')
    );
  };
  const platform = createPlatform(config, async (url, options) => {
    assert.equal(url, 'https://example.com/oauth2/jwks');
    assert.equal(options.redirect, 'error');
    return new Response(JSON.stringify({ keys: [jwk] }));
  });
  assert.deepEqual(await platform.verifyDevice(token(claims)), claims);
  for (const invalid of [
    { ...claims, aud: 'auriga.ai-gateway' },
    { ...claims, iss: 'https://other.example.com' },
    { ...claims, ctx: 'admin' },
    { ...claims, exp: 1 },
  ])
    await assert.rejects(
      platform.verifyDevice(token(invalid)),
      /invalid credential/,
    );
  const altered = token(claims).split('.');
  altered[2] = Buffer.alloc(64).toString('base64url');
  await assert.rejects(platform.verifyDevice(altered.join('.')), /signature/);
  await assert.rejects(
    platform.verifyDevice(token(claims), { ...claims, did: randomUUID() }),
    /identity changed/,
  );
});
test('OAuth requests never forward browser state; redirect and arbitrary route are rejected', async () => {
  const config = trustedConfig({ platformOrigin: 'https://example.com' }),
    seen = [];
  const platform = createPlatform(config, async (url, options) => {
    seen.push([url, options]);
    return new Response('{}');
  });
  await platform.begin();
  assert.match(seen[0][1].body, /client_id=auriga-desktop/);
  assert.equal(seen[0][1].headers.Cookie, undefined);
  assert.equal(seen[0][1].credentials, 'omit');
  await assert.rejects(
    platform.request('https://elsewhere.example.com/'),
    /route/,
  );
  assert.throws(
    () => trustedConfig({ platformOrigin: 'http://example.com' }),
    /HTTPS/,
  );
});
