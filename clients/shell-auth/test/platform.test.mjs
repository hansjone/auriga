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
  assert.match(seen[0][1].body, /client_id=auriga-dsh/);
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

test('HTTP is trusted only for the installer-confirmed private IP binding', () => {
  const ip = (platformOrigin, extra = {}) =>
    trustedConfig({
      platformOrigin,
      accessMode: 'ip',
      confirmedOrigin: platformOrigin,
      ...extra,
    });
  for (const origin of [
    'http://192.168.2.10:8080', // gitleaks:allow
    'http://10.1.2.3', // gitleaks:allow
    'http://172.16.0.9:8080', // gitleaks:allow
    'http://127.0.0.1:9000',
    'http://[::1]:8080',
    'http://[fd00::1]:8080',
  ]) {
    const config = ip(origin);
    assert.equal(config.platformOrigin, origin);
    assert.equal(config.issuer, origin);
  }
  for (const origin of [
    'http://example.com',
    'http://localhost:8080',
    'http://203.0.113.10:8080',
    'http://172.32.0.1:8080', // gitleaks:allow
    'http://3232236042:8080',
    'http://0x7f.0.0.1:8080',
    'http://[::ffff:192.168.2.10]:8080', // gitleaks:allow
    'http://192.168.2.10:8080/', // gitleaks:allow
    'http://192.168.2.10:8080/portal', // gitleaks:allow
    'http://user@192.168.2.10:8080', // gitleaks:allow
  ])
    assert.throws(() => ip(origin), /HTTPS/, origin);
  const lan = 'http://192.168.2.10:8080'; // gitleaks:allow
  assert.throws(() => trustedConfig({ platformOrigin: lan }), /HTTPS/);
  assert.throws(
    () => ip(lan, { accessMode: undefined }),
    /HTTPS/,
    'IP mode is required',
  );
  assert.throws(
    () => ip(lan, { confirmedOrigin: 'http://192.168.2.11:8080' }), // gitleaks:allow
    /HTTPS/,
    'confirmation must match exactly',
  );
  assert.throws(
    () => ip(lan, { issuer: 'http://192.168.2.11:8080' }), // gitleaks:allow
    /issuer/,
  );
});

test('K2 device projection uses authoritative member and source; AI claims keep the same source and restricted permissions', async () => {
  const config = trustedConfig({ platformOrigin: 'https://example.com' });
  const keys = generateKeyPairSync('ec', { namedCurve: 'prime256v1' });
  const jwk = {
    ...keys.publicKey.export({ format: 'jwk' }),
    kid: randomUUID(),
    alg: 'ES256',
    use: 'sig',
  };
  const source = {
    sourceSessionId: randomUUID(),
    providerId: randomUUID(),
    authGeneration: 2,
  };
  const now = Math.floor(Date.now() / 1000);
  const claims = {
    iss: config.issuer,
    sub: randomUUID(),
    tid: randomUUID(),
    did: randomUUID(),
    ptyp: 'device',
    ctx: 'device',
    scope: 'auriga.connect',
    iat: now,
    exp: now + 240,
    aud: 'auriga.ai-gateway',
    jti: randomUUID(),
    mtyp: 'member',
    perms: ['auriga.connect.models.invoke'],
    uacSessionBinding: source,
  };
  const projection = {
    accountId: claims.sub,
    tenantId: claims.tid,
    memberId: randomUUID(),
    deviceId: claims.did,
    providerId: source.providerId,
    clientId: config.clientId,
    authorizationMode: 'standard_device',
    expiresAt: new Date(Date.now() + 600000).toISOString(),
    uacSessionBinding: {
      authGeneration: source.authGeneration,
      providerId: source.providerId,
      sourceSessionId: source.sourceSessionId,
    },
  };
  const correlationId = randomUUID();
  const platform = createPlatform(config, async (url) => {
    if (url.endsWith('/oauth2/jwks')) return Response.json({ keys: [jwk] });
    assert.equal(url, 'https://example.com/api/iam/v1/me/device-session');
    return Response.json(projection, {
      headers: { 'X-Auriga-Correlation-Id': correlationId },
    });
  });
  const token = (value) => {
    const body = [
      Buffer.from(
        JSON.stringify({ alg: 'ES256', kid: jwk.kid, typ: 'at+jwt' }),
      ).toString('base64url'),
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
  const verified = await platform.verifyDevice(
    token(claims),
    claims,
    'auriga.ai-gateway',
  );
  assert.equal(
    verified.uacSessionBinding.sourceSessionId,
    source.sourceSessionId,
  );
  for (const invalid of [
    { ...claims, perms: ['iam.providers.write'] },
    { ...claims, exp: now + 600 },
    { ...claims, sid: randomUUID() },
    { ...claims, uacSessionBinding: { ...source, authGeneration: 3 } },
  ])
    await assert.rejects(
      platform.verifyDevice(token(invalid), claims, 'auriga.ai-gateway'),
    );
  const result = await platform.profile(randomUUID(), claims);
  assert.equal(result.identity.memberId, projection.memberId);
  assert.notEqual(result.identity.memberId, claims.sub);
  assert.equal(
    (await platform.request('/api/iam/v1/me/device-session')).correlationId,
    correlationId,
  );
  projection.uacSessionBinding.authGeneration++;
  await assert.rejects(
    platform.profile(randomUUID(), claims),
    /identity mismatch/,
  );
});
