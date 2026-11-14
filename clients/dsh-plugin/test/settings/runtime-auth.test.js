import test from 'node:test';
import assert from 'node:assert/strict';
import { setImmediate as nextTurn } from 'node:timers/promises';
import { shellRuntimePeer } from '../shell-runtime-peer.js';
import { teamRuntimePeer } from '../team-runtime-peer.js';
import { platformConfig, savedPlatformConfig } from '../../src/local/config.js';
import { mkdtemp, rm } from 'node:fs/promises';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import { Readable } from 'node:stream';
import { apply } from '../../src/plugin.js';
import { createSettingsStore } from '../../src/settings/store.js';

for (const outcome of ['authenticated', 'revoked'])
  test(`Team outage cannot override a shell ${outcome} snapshot`, async (t) => {
    const f = await teamRuntimePeer(t, {
      before() {
        throw new Error('Team unavailable');
      },
    });
    await f.settled();
    assert.equal(f.runtime.status(f.request).loggedIn, true);
    f.connection.transition(outcome);
    await nextTurn();
    assert.equal(
      f.runtime.status(f.request).loggedIn,
      outcome === 'authenticated',
    );
    assert.equal(f.runtime.getAccess, undefined);
  });
test('status refreshes preserve shell identity and never mint browser sessions', async (t) => {
  const f = await teamRuntimePeer(t);
  await f.settled();
  for (let i = 0; i < 3; i++) {
    const result = f.runtime.prepare(f.request);
    assert.equal(result.body.loggedIn, true);
    assert.equal(result.setCookie, undefined);
  }
  assert.equal(
    f.connection.calls.filter((c) => c.call?.operation === 'team_initialize')
      .length,
    1,
  );
});
test('a newly authenticated shell push admits the owner while Team is unavailable', async (t) => {
  const f = await teamRuntimePeer(t, {
    configure(c) {
      c.transition('signed_out');
    },
    before() {
      throw new Error('Team unavailable');
    },
  });
  assert.equal(f.runtime.status(f.request).loggedIn, false);
  f.connection.transition('authenticated');
  await nextTurn();
  assert.equal(f.runtime.status(f.request).loggedIn, true);
  assert.equal(f.runtime.status(f.request).connected, false);
  assert.equal(f.runtime.vault.snapshot().credential, undefined);
});
for (const status of [401, 403, 503])
  test(`Team HTTP ${status} cannot revoke shell identity or block explicit logout`, async (t) => {
    const f = await teamRuntimePeer(t, {
      before() {
        throw Object.assign(new Error('Team denied'), { status });
      },
    });
    await f.settled();
    assert.equal(f.runtime.status(f.request).loggedIn, true);
    assert.equal(f.runtime.status(f.request).communication, 'unavailable');
    assert.equal(f.runtime.prepare(f.request).setCookie, undefined);
    await f.runtime.command(f.request, 'logout');
    assert.equal(f.runtime.status(f.request).loggedIn, false);
    assert.equal(f.runtime.resolve(f.request), undefined);
  });
test('a pending Team handshake cannot hold login status, logout or shutdown', async (t) => {
  const entered = Promise.withResolvers();
  const f = await teamRuntimePeer(t, {
    before() {
      entered.resolve();
      return new Promise(() => {});
    },
  });
  await entered.promise;
  assert.equal(f.runtime.status(f.request).loggedIn, true);
  await f.runtime.command(f.request, 'logout');
  assert.equal(f.runtime.status(f.request).loggedIn, false);
  await f.runtime.close();
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

test('trusted platform switch fences old identity without stopping native tasks', async (t) => {
  let stopped = 0;
  const f = await shellRuntimePeer(t, {
    ctx: {
      sessionController: {
        stop() {
          stopped++;
        },
      },
    },
  });
  await f.runtime.resetAuthentication();
  assert.equal(f.runtime.resolve(f.request), undefined);
  assert.equal(f.runtime.prepare(f.request).body.loggedIn, false);
  assert.equal(f.runtime.vault.snapshot().credential, undefined);
  assert.equal(stopped, 0);
  assert.equal(
    f.connection.calls.some((c) => /oauth2/.test(c.path ?? '')),
    false,
  );
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
  assert.equal(next.providerId, 'discover');
  assert.equal(next.clientId, 'custom-client');
  assert.equal(next.network, undefined);
  assert.deepEqual(calls, []);
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
          return { peer: { principal: { kind: 'owner' } } };
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
    assert.equal((await first.call('/auriga/shell/status')).loggedIn, false);
  } finally {
    await first.close();
  }
  available = false;
  const down = await boot();
  try {
    assert.equal(
      (await down.call('/auriga/settings/state')).activeOrigin,
      'http://127.0.0.1:8080',
    );
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
