import { test } from 'node:test';
import assert from 'node:assert/strict';
import { setImmediate } from 'node:timers/promises';
import { runtimePeer } from '../local-runtime-peer.js';
import { platformConfig } from '../../src/local/platform.js';
import { createLocalLoginController } from '../../src/login/local-controller.js';
import { EventEmitter } from 'node:events';
import { mkdtemp, rm } from 'node:fs/promises';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import { Readable } from 'node:stream';
import { apply } from '../../src/plugin.js';
import { createSettingsStore } from '../../src/settings/store.js';
import { savedPlatformConfig } from '../../src/local/platform.js';

test('fresh native credential commit issues a browser session while Team is unavailable (synthetic IAM fixture only)', async (t) => {
  const { runtime, credential } = await runtimePeer(t);
  await runtime.resetAuthentication();
  t.mock.timers.enable({ apis: ['Date'], now: Date.now() });
  const previous = globalThis.fetch;
  t.mock.method(globalThis, 'fetch', async (url, init) => {
    const path = new URL(url).pathname;
    if (path === '/api/iam/v1/login/transactions')
      return Response.json({
        id: 'native-test',
        nativeDevice: {
          deviceCode: 'synthetic-device-code',
          expiresAt: new Date(Date.now() + 300000).toISOString(),
          interval: 1,
        },
        step: { type: 'form' },
      });
    if (path === '/api/iam/v1/login/transactions/native-test/advance')
      return Response.json({
        id: 'native-test',
        status: 'completed',
        step: { type: 'complete' },
      });
    if (path === '/oauth2/token')
      return Response.json({
        token_type: 'Bearer',
        scope: 'auriga.connect',
        access_token: credential.accessToken,
        refresh_token: credential.refreshToken,
      });
    if (path === '/ai/mcp') return Response.json({}, { status: 503 });
    return previous(url, init);
  });
  const request = { headers: {} };
  request.headers.cookie = runtime.prepare(request).setCookie.split(';')[0];
  assert.equal(
    (await runtime.login(request, { action: 'begin' })).step.type,
    'form',
  );
  t.mock.timers.tick(2000);
  const result = await runtime.login(request, {
    action: 'advance',
    input: { type: 'form', formId: 'totp', values: { code: 'synthetic' } },
  });
  assert.equal(result.step.type, 'complete');
  request.headers.cookie = result.setCookie[0].split(';')[0];
  await setImmediate();
  assert.equal(runtime.status(request).loggedIn, true);
  assert.equal(runtime.status(request).connected, false);
  assert.equal(runtime.status(request).communication, 'unavailable');
  assert.equal(
    runtime.vault.snapshot().credential.accessToken,
    credential.accessToken,
  );
});

for (const status of [401, 403, 503])
  test(`Team HTTP ${status} does not block verified login, refresh or logout`, async (t) => {
    const { runtime, request } = await runtimePeer(t, {
      teamRequest: async () => Response.json({}, { status }),
    });
    await setImmediate();
    const state = runtime.status(request);
    assert.equal(state.loggedIn, true);
    assert.equal(state.connected, false);
    assert.equal(state.communication, 'unavailable');
    assert.ok(runtime.resolve(request));
    const controller = createLocalLoginController({
      fetch: async () => Response.json(runtime.prepare(request).body),
    });
    assert.equal(
      (await controller.start({ signal: new AbortController().signal })).type,
      'complete',
    );
    const result = await runtime.logout(request);
    assert.equal(result.body.loggedIn, false);
    assert.equal(runtime.resolve(request), null);
    assert.equal(runtime.vault.snapshot().credential, null);
  });

test('a pending Team handshake never holds the browser login response', async (t) => {
  let release;
  const pending = new Promise((resolve) => {
    release = resolve;
  });
  t.after(() => release(Response.json({}, { status: 503 })));
  const { runtime, request } = await runtimePeer(t, {
    teamRequest: () => pending,
  });
  assert.equal(runtime.status(request).loggedIn, true);
  assert.equal(runtime.status(request).communication, 'connecting');
  await runtime.logout(request);
  release(Response.json({}, { status: 503 }));
  await setImmediate();
  assert.equal(runtime.isReady(), false);
  assert.equal(runtime.resolve(request), null);
});

test('connection configuration accepts the same explicit private HTTP origins as settings', () => {
  for (const platformOrigin of [
    'http://10.1.2.3:8080', // gitleaks:allow
    'http://[fd00::1]:8080', // gitleaks:allow
  ]) {
    assert.equal(
      platformConfig({
        platformOrigin,
        issuer: platformOrigin,
        clientId: 'auriga-dsh',
        providerId: 'team',
        allowLocalHttp: true,
      }).platformOrigin,
      platformOrigin,
    );
    assert.throws(() =>
      platformConfig({
        platformOrigin,
        issuer: platformOrigin,
        clientId: 'auriga-dsh',
        providerId: 'team',
      }),
    );
  }
});

test('trusted platform switch tombstones credentials and closes old HTTP/WS without stopping tasks', async (t) => {
  const { runtime, request, requests } = await runtimePeer(t);
  let httpClosed = false,
    wsClosed = false;
  const response = new EventEmitter();
  response.destroy = () => {
    httpClosed = true;
  };
  assert.equal(runtime.bindHttpResponse(request, response), true);
  const socket = new EventEmitter();
  socket.destroy = () => {
    wsClosed = true;
  };
  assert.equal(
    runtime.bindSocket({
      headers: { ...request.headers, upgrade: 'websocket' },
      socket,
    }),
    true,
  );
  await runtime.resetAuthentication();
  assert.equal(httpClosed, true);
  assert.equal(wsClosed, true);
  assert.equal(runtime.resolve(request), null);
  assert.equal(runtime.vault.snapshot().credential, null);
  assert.equal(runtime.prepare({ headers: {} }).body.resumeAvailable, false);
  assert.ok(requests.includes('/oauth2/revoke'));
});

test('saved origin replaces loader binding without carrying foreign provider ids or static network pins', async (t) => {
  const calls = [];
  t.mock.method(globalThis, 'fetch', async (url, init) => {
    calls.push([String(url), init.headers.get('authorization')]);
    return Response.json({
      items: [
        { id: 'new-team', connectorType: 'team-account', primaryStep: 'form' },
      ],
    });
  });
  const loader = {
    platformOrigin: 'https://old.example.com',
    issuer: 'https://old.example.com/issuer',
    clientId: 'custom-client',
    providerId: 'old-team',
  };
  const same = await savedPlatformConfig(loader, loader.platformOrigin);
  assert.equal(same.issuer, loader.issuer);
  assert.equal(calls.length, 0);
  const next = await savedPlatformConfig(loader, 'https://new.example.com');
  assert.equal(next.platformOrigin, 'https://new.example.com');
  assert.equal(next.issuer, 'https://new.example.com');
  assert.equal(next.providerId, 'new-team');
  assert.equal(next.clientId, 'custom-client');
  assert.equal(next.network, undefined);
  assert.deepEqual(calls, [
    ['https://new.example.com/api/iam/v1/login/providers', null],
  ]);
});

test('formal plugin entry consumes saved settings, applies changes immediately and reloads them after restart', async (t) => {
  const dir = await mkdtemp(join(tmpdir(), 'auriga-auth-settings-'));
  t.after(() => rm(dir, { recursive: true, force: true }));
  let available = true;
  t.mock.method(globalThis, 'fetch', async () => {
    if (!available) throw new Error('platform unavailable');
    return Response.json({
      items: [
        { id: 'team', connectorType: 'team-account', primaryStep: 'form' },
      ],
    });
  });
  const initial = await createSettingsStore(join(dir, 'auriga-settings'));
  await initial.save({
    platformOrigin: 'https://saved.example.com',
    deployment: 'local',
  });
  async function boot(config) {
    const routes = [],
      cleanups = [],
      pending = [];
    const ctx = {
      profileContext: { dir },
      connection: {
        requestRejection() {},
        admit() {
          return { peer: {} };
        },
        authorizeIndex() {
          return true;
        },
        isTrusted() {
          return true;
        },
      },
      webServer: {
        protocol: 'http:',
        register(route) {
          routes.push(route);
          return () => {};
        },
      },
      tools: {
        register() {
          return () => {};
        },
      },
      sessions: { get() {}, flush() {} },
      sessionController: { create() {}, prompt() {} },
      on() {
        return () => {};
      },
      effect(fn) {
        const dispose = fn();
        if (dispose) cleanups.push(dispose);
      },
      inject(keys, callback) {
        if (keys.includes('llm') || keys.includes('aurigaNetwork')) return;
        pending.push(callback(ctx));
      },
    };
    await apply(ctx, config);
    await Promise.all(pending);
    const call = async (path, body) => {
      const req = Readable.from(
        body ? [Buffer.from(JSON.stringify(body))] : [],
      );
      Object.assign(req, {
        url: path,
        method: body ? 'POST' : 'GET',
        headers: {
          host: 'localhost:3000',
          origin: 'http://localhost:3000',
          'content-type': 'application/json',
          'x-auriga-settings': '1',
        },
        rawHeaders: [
          'Host',
          'localhost:3000',
          'Origin',
          'http://localhost:3000',
        ],
      });
      const res = {
        writeHead(status) {
          this.status = status;
        },
        end(text) {
          this.body = JSON.parse(text);
        },
      };
      await routes
        .filter((route) => path.startsWith(route.path))
        .sort((a, b) => b.path.length - a.path.length)[0]
        .handler(req, res);
      assert.equal(res.status, 200);
      return res.body;
    };
    return {
      call,
      close: () => Promise.all(cleanups.reverse().map((fn) => fn())),
    };
  }
  const config = {
    connect: {
      platformOrigin: 'https://loader.example.com',
      issuer: 'https://loader.example.com',
      clientId: 'auriga-dsh',
      providerId: 'loader-team',
    },
  };
  const first = await boot(config);
  try {
    assert.equal(
      (await first.call('/auriga/settings/state')).activeOrigin,
      'https://saved.example.com',
    );
    const saved = await first.call('/auriga/settings/save', {
      platformOrigin: 'http://127.0.0.1:8080',
      deployment: 'cloud',
    });
    assert.equal(saved.activeOrigin, saved.platformOrigin);
    assert.equal(
      (await first.call('/auriga/local-login/status')).loggedIn,
      false,
    );
  } finally {
    await first.close();
  }
  available = false;
  const down = await boot();
  try {
    assert.equal((await down.call('/auriga/settings/state')).activeOrigin, '');
    available = true;
    const applied = await down.call('/auriga/settings/save', {
      platformOrigin: 'http://127.0.0.1:8080',
      deployment: 'cloud',
    });
    assert.equal(applied.activeOrigin, applied.platformOrigin);
  } finally {
    await down.close();
  }
  const restarted = await boot();
  try {
    assert.equal(
      (await restarted.call('/auriga/settings/state')).activeOrigin,
      'http://127.0.0.1:8080',
    );
  } finally {
    await restarted.close();
  }
});
