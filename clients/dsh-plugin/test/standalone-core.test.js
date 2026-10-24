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

async function emergencyRuntime(commitAdmission) {
  const account = testEmergency();
  const runtime = await createStandalone({
    now: fixedNow,
    commitAdmission,
    config: {
      secure: false,
      owner: { providers: ['emergency'], subject: account.username },
      connectors: [{ type: 'emergency', id: 'emergency', config: account }],
    },
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
  return { runtime, input };
}

test('different concurrent transactions cannot consume the same emergency OTP', async () => {
  const { runtime, input } = await emergencyRuntime();
  const first = await runtime.beginLogin('emergency');
  const second = await runtime.beginLogin('emergency');
  const results = await Promise.all([
    runtime.advanceLogin(first.txId, input),
    runtime.advanceLogin(second.txId, input),
  ]);
  assert.equal(results.filter((r) => r.step.type === 'complete').length, 1);
  const denied = results.find((r) => r.step.type === 'fail');
  assert.equal(denied.step.code, 'iam.invalid_credentials');
  assert.equal(denied.setCookie, undefined);
});

test('admission commit finishes before owner, identity or session is published', async () => {
  const entered = Promise.withResolvers();
  const release = Promise.withResolvers();
  let received;
  const { runtime, input } = await emergencyRuntime(async (value) => {
    received = value;
    entered.resolve();
    await release.promise;
    return true;
  });
  let issued = 0;
  const putSession = runtime.store.putSession;
  runtime.store.putSession = (...args) => {
    issued += 1;
    putSession(...args);
  };
  const begin = await runtime.beginLogin('emergency');
  const pending = runtime.advanceLogin(begin.txId, input);
  // One turn also makes a missing hook fail promptly instead of hanging.
  await Promise.race([entered.promise, pending]);
  assert.ok(received);
  assert.equal(issued, 0);
  assert.equal(runtime.getOwner(), null);
  assert.deepEqual(runtime.store.listIdentities(), []);
  assert.deepEqual(received, {
    providerId: 'emergency',
    profile: {
      subject: 'alice',
      displayName: 'alice',
      mfaPerformed: true,
      authTime: fixedNow().toISOString(),
    },
    emergencyCounter: Math.floor(fixedNow().getTime() / 30_000),
  });
  release.resolve();
  const result = await pending;
  assert.equal(result.step.type, 'complete');
  assert.equal(issued, 1);
  assert.ok(result.setCookie);
});

for (const failure of ['veto', 'throw', 'committed-then-throw']) {
  test(`admission ${failure} emits no cookie and leaves no first-owner claim`, async () => {
    let committed = false;
    const { runtime, input } = await emergencyRuntime(async () => {
      if (failure === 'veto') return false;
      committed = failure === 'committed-then-throw';
      throw new Error('private storage detail');
    });
    let issued = 0;
    runtime.store.putSession = () => issued++;
    const begin = await runtime.beginLogin('emergency');
    const result = await runtime.advanceLogin(begin.txId, input);
    assert.equal(
      result.step.code,
      failure === 'veto' ? 'iam.admission_denied' : 'iam.connector_unavailable',
    );
    assert.equal(result.setCookie, undefined);
    assert.equal(result.csrf, undefined);
    assert.equal(result.session, undefined);
    assert.equal(issued, 0);
    assert.equal(runtime.getOwner(), null);
    assert.deepEqual(runtime.store.listIdentities(), []);
    assert.equal(runtime.getLoginTransaction(begin.txId).state, null);
    assert.equal(runtime.getLoginTransaction(begin.txId).finished, true);
    assert.equal(committed, failure === 'committed-then-throw');
    const retry = await runtime.beginLogin('emergency');
    assert.equal(
      (await runtime.advanceLogin(retry.txId, input)).step.code,
      'iam.invalid_credentials',
    );
  });
}

test('fixed owner policy rejects another identity before calling persistent commit', async () => {
  let calls = 0;
  const { runtime, input } = await emergencyRuntime(async () => {
    calls++;
    return true;
  });
  runtime.registry.get('emergency').connector.advance = async () => ({
    step: { type: 'done', profile: { subject: 'mallory', mfaPerformed: true } },
    state: { lastCounter: 10 },
  });
  const begin = await runtime.beginLogin('emergency');
  const result = await runtime.advanceLogin(begin.txId, input);
  assert.equal(result.step.code, 'iam.admission_denied');
  assert.equal(calls, 0);
  assert.equal(runtime.getOwner(), null);
  assert.equal(runtime.store.getEmergencyLastCounter(), -1);
});

test('overlapping commits receive each transaction own counter and detached whitelisted profile', async () => {
  const entered = Promise.withResolvers();
  const release = Promise.withResolvers();
  const received = [];
  const { runtime, input } = await emergencyRuntime(async (value) => {
    received.push(JSON.parse(JSON.stringify(value)));
    value.profile.subject = 'mallory';
    if (value.emergencyCounter === 10) {
      entered.resolve();
      await release.promise;
    }
    return true;
  });
  let counter = 10;
  runtime.registry.get('emergency').connector.advance = async () => ({
    step: {
      type: 'done',
      profile: {
        subject: 'alice',
        mfaPerformed: true,
        secret: 'never-forward',
      },
    },
    state: { lastCounter: counter++, secret: 'never-forward' },
  });
  const first = await runtime.beginLogin('emergency');
  const second = await runtime.beginLogin('emergency');
  const pending = runtime.advanceLogin(first.txId, input);
  await entered.promise;
  const completed = await runtime.advanceLogin(second.txId, input);
  assert.equal(completed.step.type, 'complete');
  assert.equal(completed.session.subject, 'alice');
  assert.equal(runtime.store.getEmergencyLastCounter(), 11);
  release.resolve();
  assert.equal((await pending).session.subject, 'alice');
  assert.deepEqual(
    received,
    [10, 11].map((emergencyCounter) => ({
      providerId: 'emergency',
      profile: { subject: 'alice', mfaPerformed: true },
      emergencyCounter,
    })),
  );
  assert.equal(runtime.getLoginTransaction(first.txId).state, null);
  assert.equal(runtime.getLoginTransaction(second.txId).state, null);
});

test('completion check-and-set rejects an older in-flight counter after a newer transaction', async () => {
  const entered = Promise.withResolvers();
  const release = Promise.withResolvers();
  let calls = 0;
  const { runtime, input } = await emergencyRuntime(async () => {
    calls++;
    return true;
  });
  let attempt = 0;
  runtime.registry.get('emergency').connector.advance = async () => {
    const counter = 10 + attempt++;
    if (counter === 10) {
      entered.resolve();
      await release.promise;
    }
    return {
      step: { type: 'done', profile: { subject: 'alice', mfaPerformed: true } },
      state: { lastCounter: counter },
    };
  };
  const first = await runtime.beginLogin('emergency');
  const second = await runtime.beginLogin('emergency');
  const pending = runtime.advanceLogin(first.txId, input);
  await entered.promise;
  assert.equal(
    (await runtime.advanceLogin(second.txId, input)).step.type,
    'complete',
  );
  release.resolve();
  const denied = await pending;
  assert.equal(denied.step.code, 'iam.invalid_credentials');
  assert.equal(denied.setCookie, undefined);
  assert.equal(calls, 1);
  assert.equal(runtime.store.getEmergencyLastCounter(), 11);
});
