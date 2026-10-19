import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createEmergencyConnector } from '../src/identity/emergency.js';
import { createStandalone } from '../src/modes/standalone.js';
import { otpFor, testEmergency } from './helpers.js';

const fixedNow = () => new Date('2026-06-01T00:00:00.000Z');

test('emergency happy path returns done with mfaPerformed', async () => {
  const account = testEmergency();
  const connector = createEmergencyConnector({ now: fixedNow });
  const begin = await connector.begin(
    {
      txId: 'tx',
      tenantId: 't',
      providerId: 'emergency',
      purpose: 'login',
      locale: 'en',
      callbackUrl: 'http://127.0.0.1/cb/emergency',
      state: null,
      expiresAt: '2026-06-01T00:05:00.000Z',
    },
    account,
  );
  assert.equal(begin.step.type, 'form');
  assert.equal(begin.step.formId, 'emergency');

  const result = await connector.advance(
    {
      txId: 'tx',
      tenantId: 't',
      providerId: 'emergency',
      purpose: 'login',
      locale: 'en',
      callbackUrl: 'http://127.0.0.1/cb/emergency',
      state: null,
      expiresAt: '2026-06-01T00:05:00.000Z',
    },
    account,
    {
      type: 'form',
      formId: 'emergency',
      values: {
        username: account.username,
        password: account.password,
        otp: otpFor(account.totpSecret, fixedNow()),
      },
    },
  );
  assert.equal(result.step.type, 'done');
  assert.equal(result.step.profile.subject, 'alice');
  assert.equal(result.step.profile.mfaPerformed, true);
});

test('emergency rejects wrong password with iam.invalid_credentials', async () => {
  const account = testEmergency();
  const connector = createEmergencyConnector({ now: fixedNow });
  const result = await connector.advance(
    {
      txId: 'tx',
      tenantId: 't',
      providerId: 'emergency',
      purpose: 'login',
      locale: 'en',
      callbackUrl: 'http://127.0.0.1/cb/emergency',
      state: null,
      expiresAt: '2026-06-01T00:05:00.000Z',
    },
    account,
    {
      type: 'form',
      formId: 'emergency',
      values: {
        username: account.username,
        password: 'wrong-password',
        otp: otpFor(account.totpSecret, fixedNow()),
      },
    },
  );
  assert.equal(result.step.type, 'fail');
  assert.equal(result.step.code, 'iam.invalid_credentials');
});

test('emergency rejects wrong TOTP with iam.invalid_credentials', async () => {
  const account = testEmergency();
  const connector = createEmergencyConnector({ now: fixedNow });
  const result = await connector.advance(
    {
      txId: 'tx',
      tenantId: 't',
      providerId: 'emergency',
      purpose: 'login',
      locale: 'en',
      callbackUrl: 'http://127.0.0.1/cb/emergency',
      state: null,
      expiresAt: '2026-06-01T00:05:00.000Z',
    },
    account,
    {
      type: 'form',
      formId: 'emergency',
      values: {
        username: account.username,
        password: account.password,
        otp: '000000',
      },
    },
  );
  assert.equal(result.step.type, 'fail');
  assert.equal(result.step.code, 'iam.invalid_credentials');
});

test('standalone emergency login issues session cookies and CSRF', async () => {
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
  assert.equal(begin.step.type, 'form');
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
  assert.ok(done.setCookie?.some((c) => c.includes('auriga_session=')));
  assert.ok(done.setCookie?.some((c) => c.includes('HttpOnly')));
  assert.ok(done.setCookie?.some((c) => c.includes('SameSite=')));
  assert.ok(done.csrf);

  const cookies = Object.fromEntries(
    done.setCookie.map((c) => {
      const [pair] = c.split(';');
      const i = pair.indexOf('=');
      return [pair.slice(0, i), pair.slice(i + 1)];
    }),
  );
  const ok = runtime.sessions.checkCsrf('POST', cookies, {
    'x-auriga-csrf': done.csrf,
  });
  assert.equal(ok.ok, true);
  const bad = runtime.sessions.checkCsrf('POST', cookies, {
    'x-auriga-csrf': 'forged',
  });
  assert.equal(bad.ok, false);
  assert.equal(bad.code, 'common.forbidden');
});
