import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createStandalone } from '../src/modes/standalone.js';
import { loadConnectors } from '../src/identity/registry.js';
import {
  otpFor,
  parseRedirect,
  startFakeOidc,
  testEmergency,
} from './helpers.js';

const fixedNow = () => new Date('2026-06-01T00:00:00.000Z');

test('loads enterprise private connector module by package name', async () => {
  const fake = {
    descriptor() {
      return {
        type: 'example-sso',
        spiVersion: '1',
        displayName: { en: 'Example SSO' },
        steps: ['form'],
        configSchema: { type: 'object' },
      };
    },
    validateConfig() {},
    begin() {
      return {
        step: {
          type: 'form',
          formId: 'x',
          fields: [{ name: 'u', kind: 'text' }],
        },
      };
    },
    advance() {
      return { step: { type: 'done', profile: { subject: 'bob' } } };
    },
  };
  const registry = await loadConnectors(
    {
      connectors: [
        { type: 'example-sso', module: '@example/auriga-sso', config: {} },
      ],
    },
    {
      importModule: async (specifier) => {
        assert.equal(specifier, '@example/auriga-sso');
        return { default: fake };
      },
    },
  );
  assert.equal(registry.list().length, 1);
  assert.equal(registry.list()[0].type, 'example-sso');
});

test('expired login transaction cannot be advanced (iam.login_tx_expired)', async () => {
  let nowMs = Date.parse('2026-06-01T00:00:00.000Z');
  const account = testEmergency();
  const runtime = await createStandalone({
    now: () => new Date(nowMs),
    config: {
      secure: false,
      connectors: [{ type: 'emergency', id: 'emergency', config: account }],
    },
  });
  const begin = await runtime.beginLogin('emergency', {
    clientIp: '192.0.2.10',
  });
  nowMs += 6 * 60 * 1000;
  const result = await runtime.advanceLogin(begin.txId, {
    type: 'form',
    formId: 'emergency',
    values: {
      username: account.username,
      password: account.password,
      otp: otpFor(account.totpSecret, new Date(nowMs)),
    },
  });
  assert.equal(result.step.type, 'fail');
  assert.equal(result.step.code, 'iam.login_tx_expired');
});

test('finished transaction replay returns iam.login_tx_finished', async () => {
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
  const input = {
    type: 'form',
    formId: 'emergency',
    values: {
      username: account.username,
      password: account.password,
      otp: otpFor(account.totpSecret, fixedNow()),
    },
  };
  const first = await runtime.advanceLogin(begin.txId, input, {
    clientIp: '192.0.2.10',
  });
  assert.equal(first.step.type, 'complete');
  const replay = await runtime.advanceLogin(begin.txId, input, {
    clientIp: '192.0.2.10',
  });
  assert.equal(replay.step.type, 'fail');
  assert.equal(replay.step.code, 'iam.login_tx_finished');
});

test('non-owner identity is denied with iam.admission_denied', async () => {
  const owner = testEmergency('alice', 'correct-horse-battery');
  const runtime = await createStandalone({
    now: fixedNow,
    config: {
      secure: false,
      connectors: [
        { type: 'emergency', id: 'emergency', config: owner },
        {
          type: 'example-sso',
          id: 'sso',
          module: '@example/auriga-sso',
          config: {},
        },
      ],
    },
    importModule: async () => ({
      default: {
        descriptor: () => ({
          type: 'example-sso',
          spiVersion: '1',
          displayName: { en: 'Example SSO' },
          steps: ['form'],
          configSchema: { type: 'object' },
        }),
        validateConfig() {},
        begin() {
          return {
            step: {
              type: 'form',
              formId: 'sso',
              fields: [{ name: 'unused', kind: 'hidden', value: '1' }],
            },
          };
        },
        advance() {
          return { step: { type: 'done', profile: { subject: 'intruder' } } };
        },
      },
    }),
  });

  const beginOwner = await runtime.beginLogin('emergency', {
    clientIp: '192.0.2.10',
  });
  const owned = await runtime.advanceLogin(
    beginOwner.txId,
    {
      type: 'form',
      formId: 'emergency',
      values: {
        username: owner.username,
        password: owner.password,
        otp: otpFor(owner.totpSecret, fixedNow()),
      },
    },
    { clientIp: '192.0.2.10' },
  );
  assert.equal(owned.step.type, 'complete');
  assert.equal(runtime.getOwner()?.subject, 'alice');

  const beginSso = await runtime.beginLogin('sso', { clientIp: '192.0.2.11' });
  const denied = await runtime.advanceLogin(
    beginSso.txId,
    { type: 'form', formId: 'sso', values: { unused: '1' } },
    { clientIp: '192.0.2.11' },
  );
  assert.equal(denied.step.type, 'fail');
  assert.equal(denied.step.code, 'iam.admission_denied');
});

test('linked identity under owner can sign in', async () => {
  const owner = testEmergency('alice', 'correct-horse-battery');
  const runtime = await createStandalone({
    now: fixedNow,
    config: {
      secure: false,
      connectors: [
        { type: 'emergency', id: 'emergency', config: owner },
        {
          type: 'example-sso',
          id: 'sso',
          module: '@example/auriga-sso',
          config: {},
        },
      ],
    },
    importModule: async () => ({
      default: {
        descriptor: () => ({
          type: 'example-sso',
          spiVersion: '1',
          displayName: { en: 'Example SSO' },
          steps: ['form'],
          configSchema: { type: 'object' },
        }),
        validateConfig() {},
        begin() {
          return {
            step: {
              type: 'form',
              formId: 'sso',
              fields: [{ name: 'unused', kind: 'hidden', value: '1' }],
            },
          };
        },
        advance() {
          return { step: { type: 'done', profile: { subject: 'alice-sso' } } };
        },
      },
    }),
  });

  const beginOwner = await runtime.beginLogin('emergency', {
    clientIp: '192.0.2.10',
  });
  const owned = await runtime.advanceLogin(
    beginOwner.txId,
    {
      type: 'form',
      formId: 'emergency',
      values: {
        username: owner.username,
        password: owner.password,
        otp: otpFor(owner.totpSecret, fixedNow()),
      },
    },
    { clientIp: '192.0.2.10' },
  );
  assert.equal(owned.step.type, 'complete');
  const cookies = Object.fromEntries(
    owned.setCookie.map((c) => {
      const [pair] = c.split(';');
      const i = pair.indexOf('=');
      return [pair.slice(0, i), pair.slice(i + 1)];
    }),
  );
  const linked = runtime.linkIdentity('sso', 'alice-sso', {
    cookies,
    headers: { 'x-auriga-csrf': owned.csrf },
  });
  assert.equal(linked.ok, true);

  const beginSso = await runtime.beginLogin('sso', { clientIp: '192.0.2.11' });
  const ok = await runtime.advanceLogin(
    beginSso.txId,
    { type: 'form', formId: 'sso', values: { unused: '1' } },
    { clientIp: '192.0.2.11' },
  );
  assert.equal(ok.step.type, 'complete');
});

test('standalone oidc end-to-end binds first login as owner', async () => {
  const idp = await startFakeOidc();
  try {
    const runtime = await createStandalone({
      allowLocalHttp: true,
      now: () => new Date(),
      config: {
        secure: false,
        callbackBaseUrl: 'https://portal.example.com/callback',
        connectors: [
          {
            type: 'oidc',
            id: 'oidc',
            config: {
              issuer: idp.issuer,
              clientId: 'example-client',
              clientSecret: 'example-secret',
            },
          },
        ],
      },
    });
    const begin = await runtime.beginLogin('oidc', { clientIp: '192.0.2.10' });
    assert.equal(begin.step.type, 'redirect');
    const parsed = parseRedirect(begin.step.url);
    assert.equal(parsed.method, 'S256');
    const tx = runtime.getLoginTransaction(begin.txId);
    assert.ok(tx?.state?.codeVerifier);
    const code = idp.issueCode(
      String(tx.state.nonce),
      String(tx.state.codeVerifier),
    );
    const done = await runtime.advanceLogin(
      begin.txId,
      {
        type: 'callback',
        params: { code, state: String(tx.state.oauthState) },
      },
      { clientIp: '192.0.2.10' },
    );
    assert.equal(done.step.type, 'complete');
    assert.equal(runtime.getOwner()?.subject, 'alice');
    assert.equal(runtime.getOwner()?.providerId, 'oidc');
  } finally {
    await idp.close();
  }
});
