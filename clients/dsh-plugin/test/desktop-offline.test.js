import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { runInNewContext } from 'node:vm';
import { installLocalAdmission } from '../src/local/host.js';
import { savedPlatformConfig } from '../src/local/platform.js';
import { runtimePeer } from './local-runtime-peer.js';
import { totpCode } from '../src/identity/crypto.js';

test('native owner local API and WS survive offline cold start without a platform runtime', () => {
  const peer = { principal: { kind: 'owner' } };
  const connection = {
    admit: (req) =>
      req.headers.cookie === 'api=valid' ? { peer } : { rejection: 401 },
    authorizeIndex: () => false,
    isTrusted: () => true,
  };
  const admission = installLocalAdmission(
    connection,
    {
      isConfigured: () => true,
      resolve: () => null,
      installHttpTracking: () => () => {},
    },
    { protocol: 'http:' },
  );
  try {
    for (const upgrade of [undefined, 'websocket']) {
      const req = {
        method: 'POST',
        url: '/api/session/create',
        headers: { host: 'localhost:3000', cookie: 'api=valid', upgrade },
        rawHeaders: ['Host', 'localhost:3000'],
      };
      assert.equal(connection.admit(req).peer, peer);
      assert.equal(
        connection.admit({
          ...req,
          headers: { ...req.headers, cookie: '', 'x-auriga-native': 'true' },
        }).rejection,
        401,
      );
      assert.equal(
        connection.admit({
          ...req,
          headers: { ...req.headers, origin: 'https://evil.example' },
        }).rejection,
        403,
      );
    }
  } finally {
    admission.dispose();
  }
});

test('Electron startup never redirects to a platform login even when offline', async () => {
  const script = await readFile(
    new URL('../src/login/startup.js', import.meta.url),
    'utf8',
  );
  const navigations = [];
  await runInNewContext(script, {
    location: {
      protocol: 'dsh-app:',
      replace: (path) => navigations.push(path),
    },
    fetch: async () => ({
      ok: true,
      json: async () => ({
        platformOrigin: 'https://portal.example.com',
        result: { ok: true, value: { loggedIn: false } },
      }),
    }),
    Date,
    setTimeout,
  });
  assert.deepEqual(navigations, []);
});

test('shared browser owner credential cannot bypass local login or reset emergency credentials', () => {
  const peer = { principal: { kind: 'owner' } };
  const connection = {
    admit: (request) =>
      request.headers.cookie === 'browser=valid'
        ? { peer }
        : { rejection: 401 },
    authorizeIndex: () => true,
    isTrusted: () => true,
  };
  const installed = installLocalAdmission(
    connection,
    {
      isConfigured: () => true,
      resolve: () => null,
      installHttpTracking: () => () => {},
    },
    { protocol: 'http:' },
  );
  try {
    const request = {
      method: 'POST',
      headers: {
        host: 'localhost:3000',
        cookie: 'browser=valid',
        origin: 'http://localhost:3000',
        'x-auriga-native': 'true',
        'content-type': 'application/json',
      },
      rawHeaders: ['Host', 'localhost:3000', 'Origin', 'http://localhost:3000'],
    };
    assert.equal(
      connection.admit({ ...request, url: '/api/session/create' }).rejection,
      401,
    );
    assert.equal(
      connection.admit({
        ...request,
        url: '/auriga-native/aurigaAuth/emergency',
      }).rejection,
      403,
    );
    assert.equal(
      connection.admit({ ...request, url: '/auriga-native/aurigaAuth/status' })
        .peer,
      peer,
    );
    assert.equal(
      connection.admit({
        ...request,
        headers: { ...request.headers, cookie: '' },
        url: '/auriga-native/aurigaAuth/status',
      }).rejection,
      401,
    );
  } finally {
    installed.dispose();
  }
});

test('saved platform configuration can be loaded while discovery is unreachable', async (t) => {
  t.mock.method(globalThis, 'fetch', async () => {
    throw new Error('offline');
  });
  const config = await savedPlatformConfig(
    undefined,
    'https://portal.example.com',
    { saved: true },
  );
  assert.equal(config.platformOrigin, 'https://portal.example.com');
  assert.equal(config.providerId, 'discover');
});

test('browser offline emergency uses Argon2 and durable TOTP replay fence; never starts platform capabilities', async (t) => {
  const { runtime, directory } = await runtimePeer(t);
  const native = runtime.nativeRequest({ principal: { kind: 'owner' } });
  await assert.rejects(runtime.resetEmergency({ headers: {} }), /native owner/);
  const enrollment = await runtime.resetEmergency(native);
  t.mock.method(globalThis, 'fetch', async () => {
    throw new Error('offline');
  });
  const request = { headers: {} };
  request.headers.cookie = runtime.prepare(request).setCookie.split(';')[0];
  const choose = await runtime.login(request, { action: 'begin' });
  assert.equal(choose.step.fields[0].options[0].value, 'local-emergency');
  const form = await runtime.login(request, {
    action: 'advance',
    input: {
      type: 'form',
      formId: 'auriga-provider',
      values: { provider: 'local-emergency' },
    },
  });
  assert.equal(form.step.formId, 'emergency');
  const input = {
    type: 'form',
    formId: 'emergency',
    values: {
      username: enrollment.username,
      password: enrollment.password,
      otp: totpCode(enrollment.totpSecret, Math.floor(Date.now() / 30000)),
    },
  };
  const bad = await runtime.login(request, {
    action: 'advance',
    input: { ...input, values: { ...input.values, password: 'wrong' } },
  });
  assert.equal(bad.step.code, 'iam.invalid_credentials');
  const loggedIn = await runtime.login(request, { action: 'advance', input });
  assert.equal(loggedIn.step.type, 'complete');
  request.headers.cookie = loggedIn.setCookie[0].split(';')[0];
  assert.equal(runtime.status(request).localOnly, true);
  assert.equal(runtime.status(request).connected, false);
  assert.equal(runtime.status(request).network.available, false);
  assert.ok(runtime.bindSocket(request));
  const { createVault } = await import('../src/local/vault.js');
  const { createLocalEmergency } = await import('../src/local/emergency.js');
  const vault = await createVault(directory);
  t.after(() => vault.close());
  const restarted = createLocalEmergency({ vault, secure: false });
  const tx = await restarted.begin('local');
  assert.equal(
    (await restarted.advance(tx.txId, input, 'local')).step.code,
    'iam.admission_denied',
  );
  const saved = vault.snapshot();
  assert.match(saved.emergency.config.passwordHash, /^\$argon2id\$/);
  assert.doesNotMatch(JSON.stringify(saved), new RegExp(enrollment.password));
});

test('platform provider chooser includes emergency and binds the selected provider to nativeConnect', async (t) => {
  const { runtime } = await runtimePeer(t);
  t.mock.method(globalThis, 'fetch', async (url, init) => {
    if (new URL(url).pathname.endsWith('/providers'))
      return Response.json({
        items: [
          {
            id: 'team',
            connectorType: 'team-account',
            name: 'Team',
            primaryStep: 'form',
          },
          {
            id: 'emergency-provider',
            connectorType: 'emergency',
            primaryStep: 'form',
          },
        ],
      });
    assert.equal(JSON.parse(init.body).providerId, 'emergency-provider');
    return Response.json({
      id: 'tx',
      nativeDevice: {
        deviceCode: 'test',
        expiresAt: new Date(Date.now() + 60000).toISOString(),
        interval: 1,
      },
      step: { type: 'form', formId: 'emergency', fields: [] },
    });
  });
  const request = runtime.nativeRequest({ principal: { kind: 'owner' } });
  runtime.prepare(request);
  const result = await runtime.login(request, { action: 'begin' });
  assert.equal(result.step.fields[0].options.length, 2);
  const selected = await runtime.login(request, {
    action: 'advance',
    input: {
      type: 'form',
      formId: 'auriga-provider',
      values: { provider: 'emergency-provider' },
    },
  });
  assert.equal(selected.step.formId, 'emergency');
});
