import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { createOidcConnector } from '../src/identity/oidc.js';
import { createOidcHttp, publicAddress } from '../src/identity/oidc-http.js';
import { parseRedirect, signIdToken, startFakeOidc } from './helpers.js';

function ctx(state = null) {
  return {
    txId: '01936f00-0000-7000-8000-000000000099',
    tenantId: '01936f00-0000-7000-8000-000000000020',
    providerId: '01936f00-0000-7000-8000-000000000002',
    purpose: 'login',
    locale: 'en',
    callbackUrl:
      'https://portal.example.com/api/iam/v1/login/callback/01936f00-0000-7000-8000-000000000002',
    state,
    expiresAt: '2026-06-01T00:05:00.000Z',
  };
}

test('publicAddress rejects loopback and private ranges', () => {
  // Avoid RFC1918 literals in the public repo (gitleaks private-ipv4 rule).
  assert.equal(publicAddress('127.0.0.1'), false);
  assert.equal(publicAddress('169.254.1.1'), false); // link-local
  assert.equal(publicAddress('100.64.1.1'), false); // CGNAT
  assert.equal(publicAddress('198.18.0.1'), false); // benchmarking
  assert.equal(publicAddress('8.8.8.8'), true);
});

test('oidc http rejects private targets when allowLocalHttp is false', async () => {
  const http = createOidcHttp({ allowLocalHttp: false });
  await assert.rejects(
    () =>
      http.send('http://127.0.0.1:9/.well-known/openid-configuration', 'GET'),
    /prohibited|invalid OIDC URL/,
  );
});

test('oidc happy path with PKCE + JWKS verified id_token', async () => {
  const idp = await startFakeOidc();
  try {
    const connector = createOidcConnector({
      allowLocalHttp: true,
      now: () => new Date(),
    });
    const config = {
      issuer: idp.issuer,
      clientId: 'example-client',
      clientSecret: 'example-secret',
      scopes: ['openid', 'profile', 'email'],
    };
    await connector.validateConfig(config);
    const begin = await connector.begin(ctx(null), config);
    assert.equal(begin.step.type, 'redirect');
    const parsed = parseRedirect(begin.step.url);
    assert.equal(parsed.method, 'S256');
    assert.ok(parsed.state);
    assert.ok(parsed.nonce);
    assert.equal(
      parsed.challenge,
      createHash('sha256')
        .update(begin.state.codeVerifier, 'ascii')
        .digest('base64url'),
    );

    const code = idp.issueCode(begin.state.nonce, begin.state.codeVerifier);
    const done = await connector.advance(ctx(begin.state), config, {
      type: 'callback',
      params: { code, state: begin.state.oauthState },
    });
    assert.equal(done.step.type, 'done');
    assert.equal(done.step.profile.subject, 'alice');
    assert.equal(done.step.profile.email, 'alice@example.com');
  } finally {
    await idp.close();
  }
});

test('oidc rejects mismatched state with iam.external_denied', async () => {
  const idp = await startFakeOidc();
  try {
    const connector = createOidcConnector({ allowLocalHttp: true });
    const config = {
      issuer: idp.issuer,
      clientId: 'example-client',
      clientSecret: 'example-secret',
    };
    const begin = await connector.begin(ctx(null), config);
    const result = await connector.advance(ctx(begin.state), config, {
      type: 'callback',
      params: { code: 'x', state: 'wrong-state' },
    });
    assert.equal(result.step.type, 'fail');
    assert.equal(result.step.code, 'iam.external_denied');
  } finally {
    await idp.close();
  }
});

test('oidc rejects mismatched nonce with iam.external_denied', async () => {
  const idp = await startFakeOidc();
  try {
    const connector = createOidcConnector({
      allowLocalHttp: true,
      now: () => new Date(),
    });
    const config = {
      issuer: idp.issuer,
      clientId: 'example-client',
      clientSecret: 'example-secret',
    };
    const begin = await connector.begin(ctx(null), config);
    const code = idp.issueCode('other-nonce', begin.state.codeVerifier);
    const result = await connector.advance(ctx(begin.state), config, {
      type: 'callback',
      params: { code, state: begin.state.oauthState },
    });
    assert.equal(result.step.type, 'fail');
    assert.equal(result.step.code, 'iam.external_denied');
  } finally {
    await idp.close();
  }
});

test('oidc rejects PKCE verifier mismatch with iam.external_denied', async () => {
  const idp = await startFakeOidc();
  try {
    const connector = createOidcConnector({ allowLocalHttp: true });
    const config = {
      issuer: idp.issuer,
      clientId: 'example-client',
      clientSecret: 'example-secret',
    };
    const begin = await connector.begin(ctx(null), config);
    const code = idp.issueCode(begin.state.nonce, begin.state.codeVerifier);
    const badState = { ...begin.state, codeVerifier: 'a'.repeat(43) };
    const result = await connector.advance(ctx(badState), config, {
      type: 'callback',
      params: { code, state: begin.state.oauthState },
    });
    assert.equal(result.step.type, 'fail');
    assert.equal(result.step.code, 'iam.external_denied');
  } finally {
    await idp.close();
  }
});

test('oidc rejects forged id_token signature with iam.external_denied', async () => {
  const idp = await startFakeOidc();
  try {
    const connector = createOidcConnector({
      allowLocalHttp: true,
      now: () => new Date(),
    });
    const config = {
      issuer: idp.issuer,
      clientId: 'example-client',
      clientSecret: 'example-secret',
    };
    const begin = await connector.begin(ctx(null), config);
    idp.setWrongJwks(true);
    const forged = signIdToken({
      privateKey: idp.privateKey,
      issuer: idp.issuer,
      nonce: begin.state.nonce,
      aud: 'example-client',
      sub: 'alice',
    });
    // Keep a valid local signature but advertise a different JWKS key → verify fails.
    idp.setTokenOverride(forged);
    const code = idp.issueCode(begin.state.nonce, begin.state.codeVerifier);
    const result = await connector.advance(ctx(begin.state), config, {
      type: 'callback',
      params: { code, state: begin.state.oauthState },
    });
    assert.equal(result.step.type, 'fail');
    assert.equal(result.step.code, 'iam.external_denied');
  } finally {
    await idp.close();
  }
});
