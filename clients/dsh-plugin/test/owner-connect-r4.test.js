import { ownerRequest } from './owner-request.js';
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { setTimeout as delay } from 'node:timers/promises';
import { join } from 'node:path';
import { runtimePeer } from './local-runtime-peer.js';
import { createConnectRuntime } from '../src/local/runtime.js';
import { createLocalLoginController } from '../src/login/local-controller.js';
import { installNativeLoginRPC } from '../src/local/native-rpc.js';
import { createVault } from '../src/local/vault.js';
import { PlatformError } from '../src/local/platform.js';

test('R4 saved credential remains recoverable after access/session expiry', async (t) => {
  const { runtime, request, credential } = await runtimePeer(t);
  t.mock.timers.enable({
    apis: ['Date'],
    now: credential.claims.exp * 1000 + 1,
  });
  const status = runtime.status(request);
  assert.equal(status.loggedIn, false);
  assert.equal(status.resumeAvailable, true);
  assert.equal(status.platform, 'recovering');
  runtime.prepare(ownerRequest(runtime, request));
  await runtime.cancelAttempt(request);
  assert.equal(runtime.status(request).resumeAvailable, true);
  assert.deepEqual(runtime.vault.snapshot().credential, credential);
  assert.deepEqual(runtime.vault.snapshot().revocations ?? [], []);
});

for (const [error, category] of [
  [new Error('login attempt busy'), 'busy'],
  [new Error('team account login unavailable'), 'configuration'],
  [new PlatformError(503, 'SECRET'), 'platform_http'],
  [
    new TypeError('SECRET', {
      cause: Object.assign(new Error('SECRET'), { code: 'ENOTFOUND' }),
    }),
    'dns',
  ],
])
  test(
    'R4 native RPC exposes only safe UI classification: ' + category,
    async (t) => {
      t.mock.method(console, 'warn', () => {});
      let handler;
      installNativeLoginRPC(
        {
          connection: {
            register: (_ctx, _path, callback) => {
              handler = callback;
            },
          },
        },
        () => ({
          nativeRequest: () => ({ headers: {} }),
          login: async () => {
            throw error;
          },
        }),
      );
      const result = await handler(
        'aurigaAuth/login',
        { args: { input: { action: 'begin' } } },
        new AbortController().signal,
        { principal: { kind: 'owner' } },
      );
      assert.equal(result.ok, false);
      assert.equal(result.error.diagnostic.category, category);
      assert.doesNotMatch(JSON.stringify(result), /SECRET/);
    },
  );

test('R4 offline cold start cancel/restart preserves saved device and automatically restores online', async (t) => {
  const f = await runtimePeer(t);
  const onlineFetch = globalThis.fetch;
  let offline = true;
  t.mock.method(globalThis, 'fetch', (...args) => {
    if (offline)
      throw new TypeError('offline', {
        cause: Object.assign(new Error('refused'), { code: 'ECONNREFUSED' }),
      });
    return onlineFetch(...args);
  });
  t.mock.timers.enable({ apis: ['Date', 'setInterval'], now: Date.now() });
  const directory = join(f.directory, 'cold');
  const saved = await createVault(directory);
  await saved.update((s) => {
    s.credential = f.credential;
  });
  saved.close();
  const runtime = await createConnectRuntime({
    directory,
    config: {
      platformOrigin: 'https://portal.example.com',
      issuer: 'https://portal.example.com',
      clientId: 'auriga-dsh',
      providerId: 'team',
    },
    ctx: {
      webServer: { protocol: 'http:' },
      tools: { register: () => () => {} },
      sessions: { get() {}, flush() {} },
      sessionController: { create() {}, prompt() {} },
    },
  });
  try {
    const request = runtime.nativeRequest({ principal: { kind: 'owner' } });
    const calls = [];
    const controller = createLocalLoginController({
      native: true,
      fetch: async (path, init) => {
        const envelope = JSON.parse(init.body),
          action = envelope.method.split('/')[1];
        calls.push(action);
        try {
          const value =
            action === 'status'
              ? runtime.prepare(ownerRequest(runtime, request)).body
              : action === 'cancel'
                ? (await runtime.cancelAttempt(request), { cancelled: true })
                : action === 'logout'
                  ? (await runtime.logout(request)).body
                  : {
                      step: (
                        await runtime.login(
                          request,
                          envelope.payload.args.input,
                        )
                      ).step,
                    };
          return Response.json({
            type: 'server-response',
            rpcId: envelope.rpcId,
            result: { ok: true, value },
          });
        } catch {
          return Response.json({
            type: 'server-response',
            rpcId: envelope.rpcId,
            result: {
              ok: false,
              error: {
                code: 'common.unavailable',
                diagnostic: { category: 'connection' },
              },
            },
          });
        }
      },
    });
    const signal = new AbortController().signal;
    await assert.rejects(controller.start({ signal }));
    await controller.cancel(signal);
    await assert.rejects(controller.restart({ signal }));
    assert.deepEqual(runtime.vault.snapshot().credential, f.credential);
    assert.deepEqual(runtime.vault.snapshot().revocations ?? [], []);
    assert.equal(calls.includes('logout'), false);
    assert.equal(runtime.status(request).resumeAvailable, true);
    offline = false;
    t.mock.timers.tick(10000);
    for (let i = 0; i < 50 && !runtime.isReady(); i++) await delay(10);
    assert.equal(runtime.isReady(), true);
    assert.equal(runtime.status(request).loggedIn, true);
    assert.deepEqual(runtime.vault.snapshot().credential, f.credential);
  } finally {
    await runtime.close();
  }
});

test('R4 closing an owner RPC does not cancel a shared login form', async () => {
  let handler,
    finish,
    cancelled = 0;
  const pending = new Promise((resolve) => {
    finish = resolve;
  });
  installNativeLoginRPC(
    {
      connection: {
        register: (_ctx, _path, callback) => {
          handler = callback;
        },
      },
    },
    () => ({
      nativeRequest: () => ({ headers: {} }),
      attemptToken: () => 'shared',
      cancelAttempt: async () => {
        cancelled++;
      },
      login: async () => pending,
    }),
  );
  const controller = new AbortController();
  const result = handler(
    'aurigaAuth/login',
    { args: { input: { action: 'begin' } } },
    controller.signal,
    { principal: { kind: 'owner' } },
  );
  controller.abort();
  finish({ step: { type: 'form', formId: 'team', fields: [] } });
  assert.equal((await result).ok, true);
  assert.equal(cancelled, 0);
});

for (const name of [undefined, 'Verified Alice'])
  test(
    'R4 display name comes from signed platform claims, never a saved form value: ' +
      name,
    async (t) => {
      const { runtime, request } = await runtimePeer(t, {
        claims: name ? { name } : {},
      });
      await runtime.vault.update((s) => {
        s.credential.accountName = 'Untrusted input';
      });
      assert.equal(runtime.status(request).account.displayName, name);
    },
  );

test('R4 an online owner does not reverify or reconnect Team every timer tick', async (t) => {
  t.mock.timers.enable({ apis: ['Date', 'setInterval'], now: Date.now() });
  const { runtime, requests } = await runtimePeer(t);
  const before = requests.length;
  for (let i = 0; i < 30; i++) {
    t.mock.timers.tick(1000);
    await delay(2);
  }
  assert.equal(runtime.isReady(), true);
  assert.equal(requests.length, before);
  assert.equal(
    runtime.status(runtime.nativeRequest({ principal: { kind: 'owner' } }))
      .communication,
    'ready',
  );
});
