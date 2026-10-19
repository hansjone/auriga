import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  createOidcHttp,
  assertAddressesAllowed,
} from '../src/identity/oidc-http.js';
import { createMemoryStore } from '../src/identity/store.js';
import {
  hashPassword,
  parseAndValidatePasswordHash,
  ARGON2_LIMITS,
} from '../src/identity/crypto.js';
import { createEmergencyConnector } from '../src/identity/emergency.js';
import { createStandalone } from '../src/modes/standalone.js';
import { otpFor, testEmergency, TEST_ARGON2 } from './helpers.js';

const fixedNow = () => new Date('2026-06-01T00:00:00.000Z');

test('DNS lookup that returns private addresses is rejected (rebinding)', async () => {
  let calls = 0;
  const http = createOidcHttp({
    allowLocalHttp: false,
    lookupAll: async () => {
      calls += 1;
      // Simulate rebinding: every resolve attempt returns a non-public IP
      // (link-local; avoid RFC1918 literals — gitleaks private-ipv4).
      return [{ address: '169.254.1.1', family: 4 }];
    },
  });
  await assert.rejects(
    () =>
      http.send(
        'https://idp.example.com/.well-known/openid-configuration',
        'GET',
      ),
    (err) => {
      assert.match(String(err?.message ?? err), /prohibited/i);
      return true;
    },
  );
  assert.ok(calls >= 1);
});

test('assertAddressesAllowed rejects when second address is private', () => {
  assert.throws(
    () =>
      assertAddressesAllowed(
        'idp.example.com',
        ['8.8.8.8', '169.254.1.1'],
        false,
      ),
    /prohibited/,
  );
});

test('claimOwner is compare-and-set: only first caller wins', () => {
  const store = createMemoryStore();
  const a = store.claimOwner({ providerId: 'emergency', subject: 'alice' });
  const b = store.claimOwner({ providerId: 'oidc', subject: 'bob' });
  assert.equal(a, true);
  assert.equal(b, false);
  assert.equal(store.getOwner()?.subject, 'alice');
});

test('concurrent first logins: only one becomes owner', async () => {
  const account = testEmergency();
  const runtime = await createStandalone({
    now: fixedNow,
    config: {
      secure: false,
      connectors: [{ type: 'emergency', id: 'emergency', config: account }],
    },
  });
  const beginA = await runtime.beginLogin('emergency', {
    clientIp: '192.0.2.10',
  });
  const beginB = await runtime.beginLogin('emergency', {
    clientIp: '192.0.2.11',
  });
  const input = {
    type: 'form',
    formId: 'emergency',
    values: {
      username: account.username,
      password: account.password,
      otp: otpFor(account.totpSecret, fixedNow()),
    },
  };
  const [r1, r2] = await Promise.all([
    runtime.advanceLogin(beginA.txId, input, { clientIp: '192.0.2.10' }),
    runtime.advanceLogin(beginB.txId, input, { clientIp: '192.0.2.11' }),
  ]);
  const outcomes = [r1.step.type, r2.step.type].sort();
  // One complete; the other may complete (same subject identity) or fail if TOTP counter reused.
  assert.ok(outcomes.includes('complete'));
  assert.equal(runtime.getOwner()?.subject, 'alice');
});

test('weak argon2 hash is rejected at validateConfig', () => {
  const connector = createEmergencyConnector();
  assert.throws(
    () => hashPassword('x', { t: 1, m: 8, p: 1, saltLen: 8, dkLen: 16 }),
    (e) => {
      assert.equal(
        /** @type {{ code?: string }} */ (e).code,
        'common.validation_failed',
      );
      return true;
    },
  );
  assert.throws(
    () =>
      parseAndValidatePasswordHash(
        '$argon2id$v=19$m=8,t=1,p=1$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA',
      ),
    (e) => {
      assert.equal(
        /** @type {{ code?: string }} */ (e).code,
        'common.validation_failed',
      );
      return true;
    },
  );
  assert.throws(
    () =>
      connector.validateConfig({
        username: 'alice',
        passwordHash:
          '$argon2id$v=19$m=8,t=1,p=1$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA',
        totpSecret: 'MFRGGZDFMZTWQ2LK',
      }),
    (e) => {
      assert.equal(
        /** @type {{ code?: string }} */ (e).code,
        'common.validation_failed',
      );
      return true;
    },
  );
  assert.ok(ARGON2_LIMITS.mMin >= 19_456);
});

test('owner policy blocks first login that does not match', async () => {
  const account = testEmergency();
  const runtime = await createStandalone({
    now: fixedNow,
    config: {
      secure: false,
      owner: { providers: ['oidc'], subject: 'only-this' },
      connectors: [{ type: 'emergency', id: 'emergency', config: account }],
    },
  });
  const begin = await runtime.beginLogin('emergency', {
    clientIp: '192.0.2.10',
  });
  const denied = await runtime.advanceLogin(
    begin.txId,
    {
      type: 'form',
      formId: 'emergency',
      values: {
        username: account.username,
        password: account.password,
        otp: otpFor(account.totpSecret, fixedNow()),
      },
    },
    { clientIp: '192.0.2.10' },
  );
  assert.equal(denied.step.type, 'fail');
  assert.equal(denied.step.code, 'iam.admission_denied');
  assert.equal(runtime.getOwner(), null);
});

test('linkIdentity requires owner session with recent MFA', async () => {
  const account = testEmergency();
  const runtime = await createStandalone({
    now: fixedNow,
    config: {
      secure: false,
      connectors: [{ type: 'emergency', id: 'emergency', config: account }],
    },
  });
  const begin = await runtime.beginLogin('emergency', {
    clientIp: '192.0.2.10',
  });
  const done = await runtime.advanceLogin(
    begin.txId,
    {
      type: 'form',
      formId: 'emergency',
      values: {
        username: account.username,
        password: account.password,
        otp: otpFor(account.totpSecret, fixedNow()),
      },
    },
    { clientIp: '192.0.2.10' },
  );
  assert.equal(done.step.type, 'complete');
  const cookies = Object.fromEntries(
    done.setCookie.map((c) => {
      const [pair] = c.split(';');
      const i = pair.indexOf('=');
      return [pair.slice(0, i), pair.slice(i + 1)];
    }),
  );

  const noAuth = runtime.linkIdentity('oidc', 'alice-sso', { cookies: {} });
  assert.equal(noAuth.ok, false);
  assert.equal(noAuth.code, 'common.unauthenticated');

  const ok = runtime.linkIdentity('oidc', 'alice-sso', {
    cookies,
    headers: { 'x-auriga-csrf': done.csrf },
  });
  assert.equal(ok.ok, true);
  assert.ok(runtime.store.hasIdentity('oidc', 'alice-sso'));
});

test('TEST_ARGON2 meets configured floors', () => {
  assert.ok(TEST_ARGON2.m >= ARGON2_LIMITS.mMin);
  assert.ok(TEST_ARGON2.t >= ARGON2_LIMITS.tMin);
  assert.ok(TEST_ARGON2.saltLen >= ARGON2_LIMITS.saltMin);
});
