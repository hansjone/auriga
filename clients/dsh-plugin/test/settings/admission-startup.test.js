import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, rm, mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import { Readable } from 'node:stream';
import { EventEmitter } from 'node:events';
import { generateKeyPairSync, sign } from 'node:crypto';
import { apply } from '../../src/plugin.js';
import { createSettingsStore } from '../../src/settings/store.js';

const nativePeer = { principal: { kind: 'owner' } };

// Public host-port lifecycle fixture, not a real DSH/browser or IAM claim.
async function installation(t, preferences) {
  const dir = await mkdtemp(join(tmpdir(), 'auriga-startup-admission-'));
  const routes = [],
    injections = [],
    disposers = [],
    effects = new Map();
  let hook;
  const native = (req) =>
    req.headers.cookie?.split('; ').includes('native=valid');
  const ctx = {
    connection: {
      admit(req) {
        return native(req) ? { peer: nativePeer } : { rejection: 401 };
      },
      requestRejection(req) {
        return this.admit(req).rejection;
      },
      authorizeIndex(req, res) {
        if (native(req)) return true;
        res.writeHead(401);
        res.end();
        return false;
      },
      isTrusted(req) {
        return req.headers.host === 'localhost:3000';
      },
    },
    webServer: {
      protocol: 'http:',
      register(route) {
        routes.push(route);
        return () => {};
      },
    },
    on(name, callback) {
      assert.equal(name, 'connection/request');
      hook = callback;
      return () => {
        hook = undefined;
      };
    },
    effect(fn, name) {
      const dispose = fn();
      if (dispose) {
        disposers.push(dispose);
        effects.set(name, dispose);
      }
    },
    inject(keys, callback) {
      if (!keys.includes('llm') && !keys.includes('aurigaNetwork'))
        injections.push({ keys, callback, started: false });
    },
  };
  t.after(async () => {
    await Promise.all(disposers.reverse().map((dispose) => dispose()));
    await rm(dir, { recursive: true, force: true });
  });
  if (preferences) {
    const store = await createSettingsStore(join(dir, 'auriga-settings'));
    await store.save(preferences);
  }
  function request(
    path = '/api/test',
    body,
    cookies = 'native=valid',
    extra = {},
  ) {
    const req = Readable.from(body ? [Buffer.from(JSON.stringify(body))] : []);
    const socket = new EventEmitter();
    socket.destroy = () => {
      socket.destroyed = true;
      socket.emit('close');
    };
    Object.assign(req, {
      url: path,
      method: body ? 'POST' : 'GET',
      socket,
      headers: {
        host: 'localhost:3000',
        origin: 'http://localhost:3000',
        cookie: cookies,
        'content-type': 'application/json',
        ...extra,
      },
    });
    req.rawHeaders = Object.entries(req.headers).flat();
    return req;
  }
  function response() {
    const res = new EventEmitter();
    res.writeHead = (status, headers) => {
      res.status = status;
      res.headers = headers ?? {};
    };
    res.end = (body) => {
      res.body = body;
      res.writableEnded = true;
    };
    res.destroy = () => {
      res.destroyed = true;
      res.emit('close');
    };
    return res;
  }
  async function call(path, body, cookies, extra) {
    const req = request(path, body, cookies, extra),
      res = response();
    const route = routes
      .filter((row) => path.startsWith(row.path))
      .sort((a, b) => b.path.length - a.path.length)[0];
    await route.handler(req, res);
    return {
      status: res.status,
      headers: res.headers,
      body: res.body ? JSON.parse(res.body) : null,
    };
  }
  async function runReady() {
    const outcomes = [];
    for (;;) {
      const ready = injections.filter(
        (row) => !row.started && row.keys.every((key) => ctx[key]),
      );
      if (!ready.length) break;
      for (const row of ready) row.started = true;
      const results = await Promise.allSettled(
        ready.map((row) => row.callback(ctx)),
      );
      for (const result of results)
        if (result.status === 'rejected') outcomes.push(result.reason);
    }
    return outcomes;
  }
  async function closed() {
    await standalone();
  }
  async function standalone() {
    assert.equal(ctx.connection.authorizeIndex(request('/'), response()), true);
    for (const upgrade of [undefined, 'websocket'])
      assert.equal(
        ctx.connection.admit(
          request('/api/test', undefined, 'native=valid', { upgrade }),
        ).peer,
        nativePeer,
      );
    assert.equal(
      ctx.connection.admit(request('/api/test', undefined, '')).rejection,
      401,
    );
    assert.equal(
      hook,
      undefined,
      'local DSH HTTP is not bound to platform response lifetimes',
    );
  }
  await apply(ctx);
  return {
    dir,
    ctx,
    request,
    response,
    call,
    runReady,
    closed,
    standalone,
    closeProfile: () => effects.get('auriga-dsh: profile admission')?.(),
  };
}

for (const startsStandalone of [false, true])
  test(
    startsStandalone
      ? 'R2 local WebSockets survive platform configuration changes'
      : 'R2 saved platform does not block local owner while dependencies recover',
    async (t) => {
      const origin = 'https://portal.example.com';
      const host = await installation(
        t,
        startsStandalone
          ? undefined
          : {
              platformOrigin: origin,
              deployment: 'cloud',
            },
      );
      await host.closed();
      host.ctx.profileContext = { dir: host.dir };
      assert.deepEqual(await host.runReady(), []);
      if (startsStandalone) {
        await host.standalone();
        const live = host.request('/api/ws', undefined, 'native=valid', {
          upgrade: 'websocket',
        });
        const finished = host.request('/api/ws', undefined, 'native=valid', {
          upgrade: 'websocket',
        });
        const http = host.request();
        const rejected = host.request('/api/ws', undefined, '', {
          upgrade: 'websocket',
        });
        assert.equal(host.ctx.connection.admit(live).peer, nativePeer);
        assert.equal(host.ctx.connection.admit(live).peer, nativePeer);
        assert.equal(live.socket.listenerCount('close'), 0);
        assert.equal(host.ctx.connection.admit(finished).peer, nativePeer);
        assert.equal(host.ctx.connection.admit(http).peer, nativePeer);
        assert.equal(host.ctx.connection.admit(rejected).rejection, 401);
        const finish = t.mock.method(finished.socket, 'destroy');
        finished.socket.destroy();
        let rejectionWhileClosing;
        live.socket.once('close', () => {
          rejectionWhileClosing = host.ctx.connection.admit(
            host.request('/api/ws', undefined, 'native=valid', {
              upgrade: 'websocket',
            }),
          ).rejection;
        });
        const save = await host.call(
          '/auriga/settings/save',
          {
            platformOrigin: origin,
            deployment: 'cloud',
          },
          'native=valid',
          { 'x-auriga-settings': '1' },
        );
        assert.equal(save.status, 200);
        assert.equal(
          live.socket.destroyed,
          undefined,
          'the old multiplexed transport must be destroyed before platform login is enabled',
        );
        assert.equal(
          rejectionWhileClosing,
          undefined,
          'close callbacks cannot race a native-only reconnection',
        );
        assert.equal(
          finish.mock.callCount(),
          1,
          'naturally closed sockets are no longer tracked',
        );
        assert.equal(
          http.socket.destroyed,
          undefined,
          'ordinary native HTTP is not a standalone WebSocket',
        );
        assert.equal(
          rejected.socket.destroyed,
          undefined,
          'native rejection must not register a socket',
        );
      }
      await host.closed();
      assert.equal((await host.call('/auriga/local-login/status')).status, 503);
      assert.equal(
        (await host.call('/auriga/settings/state')).body.platformOrigin,
        origin,
      );
      // The dependency can remain absent indefinitely: repeated requests stay shut.
      await host.closed();
      const { privateKey, publicKey } = generateKeyPairSync('ec', {
        namedCurve: 'P-256',
      });
      t.mock.method(globalThis, 'fetch', async (url, init) => {
        const path = new URL(url).pathname;
        if (path === '/api/iam/v1/login/providers')
          return Response.json({
            items: [
              {
                id: 'team',
                connectorType: 'team-account',
                primaryStep: 'form',
              },
            ],
          });
        if (path === '/api/iam/v1/login/transactions') {
          const input = JSON.parse(init.body);
          assert.equal(input.nativeConnect.clientId, 'auriga-dsh');
          assert.equal(input.nativeConnect.codeChallengeMethod, 'S256');
          return Response.json({
            id: 'native-startup',
            nativeDevice: {
              deviceCode: 'synthetic-code',
              expiresAt: new Date(Date.now() + 300000).toISOString(),
              interval: 1,
            },
            step: { type: 'form', formId: 'totp', fields: [] },
          });
        }
        if (path.endsWith('/native-startup/advance'))
          return Response.json({
            id: 'native-startup',
            status: 'completed',
            step: { type: 'complete' },
          });
        if (path === '/oauth2/token') {
          const input = new URLSearchParams(init.body);
          assert.equal(
            input.get('grant_type'),
            'urn:ietf:params:oauth:grant-type:device_code',
          );
          assert.ok(input.get('code_verifier'));
          const now = Math.floor(Date.now() / 1000);
          const encoded = [
            { alg: 'ES256', kid: 'startup-fixture' },
            {
              iss: origin,
              aud: 'auriga',
              ptyp: 'device',
              ctx: 'device',
              scope: 'auriga.connect',
              tid: 'tenant',
              sub: 'alice',
              did: 'device',
              iat: now,
              exp: now + 300,
            },
          ]
            .map((part) =>
              Buffer.from(JSON.stringify(part)).toString('base64url'),
            )
            .join('.');
          return Response.json({
            token_type: 'Bearer',
            scope: 'auriga.connect',
            refresh_token: 'synthetic-refresh',
            access_token:
              encoded +
              '.' +
              sign('sha256', Buffer.from(encoded), {
                key: privateKey,
                dsaEncoding: 'ieee-p1363',
              }).toString('base64url'),
          });
        }
        if (path === '/oauth2/jwks')
          return Response.json({
            keys: [
              {
                ...publicKey.export({ format: 'jwk' }),
                kid: 'startup-fixture',
              },
            ],
          });
        if (path === '/ai/mcp') return Response.json({}, { status: 503 });
        throw new Error('unexpected fixture path ' + path);
      });
      host.ctx.tools = {
        register() {
          return () => {};
        },
      };
      host.ctx.sessions = { get() {}, flush() {} };
      host.ctx.sessionController = { create() {}, prompt() {} };
      assert.deepEqual(await host.runReady(), []);
      await host.closed();
      const prepared = await host.call('/auriga/local-login/status');
      assert.equal(prepared.status, 200);
      assert.equal(prepared.body.loggedIn, false);
      const cookies = 'native=valid';
      const headers = { 'x-auriga-csrf': prepared.body.csrf };
      const begun = await host.call(
        '/auriga/local-login/login',
        { action: 'begin' },
        cookies,
        headers,
      );
      assert.equal(begun.status, 200);
      assert.equal(begun.body.step.type, 'form');
      t.mock.timers.enable({ apis: ['Date'], now: Date.now() + 1100 });
      const completed = await host.call(
        '/auriga/local-login/login',
        {
          action: 'advance',
          input: {
            type: 'form',
            formId: 'totp',
            values: { code: 'synthetic' },
          },
        },
        cookies,
        headers,
      );
      assert.equal(completed.status, 200);
      assert.equal(completed.body.step.type, 'complete');
      const authenticated = 'native=valid';
      assert.equal(
        host.ctx.connection.authorizeIndex(
          host.request('/', undefined, authenticated),
          host.response(),
        ),
        true,
      );
      assert.equal(
        host.ctx.connection.admit(
          host.request('/api/test', undefined, authenticated),
        ).peer,
        nativePeer,
      );
      const authenticatedSocket = host.request(
        '/api/ws',
        undefined,
        authenticated,
        {
          upgrade: 'websocket',
        },
      );
      assert.equal(
        host.ctx.connection.admit(authenticatedSocket).peer,
        nativePeer,
      );
      assert.equal(
        (
          await host.call(
            '/auriga/local-login/status',
            undefined,
            authenticated,
          )
        ).body.loggedIn,
        true,
      );
      await host.closed();
      if (startsStandalone) {
        const cleared = await host.call(
          '/auriga/settings/save',
          {
            platformOrigin: '',
            deployment: 'cloud',
          },
          authenticated,
          { 'x-auriga-settings': '1' },
        );
        assert.equal(cleared.status, 200);
        assert.equal(authenticatedSocket.socket.destroyed, undefined);
        await host.standalone();
      }
    },
  );

test('R2 empty profile keeps DSH owner access while tool services are absent', async (t) => {
  const host = await installation(t);
  await host.closed();
  host.ctx.profileContext = { dir: host.dir };
  assert.deepEqual(await host.runReady(), []);
  await host.standalone();
});

test('R2 corrupt platform settings do not revoke DSH owner access', async (t) => {
  const host = await installation(t);
  await mkdir(join(host.dir, 'auriga-settings'), { recursive: true });
  await writeFile(join(host.dir, 'auriga-settings/settings.json'), '{');
  host.ctx.profileContext = { dir: host.dir };
  assert.ok((await host.runReady()).length > 0);
  await host.closed();
});

test('R2 late platform settings do not change DSH owner authority', async (t) => {
  const host = await installation(t);
  host.ctx.profileContext = { dir: host.dir };
  const reading = host.runReady();
  host.closeProfile();
  await reading;
  await host.closed();
});

test('R2 saving a platform while tools are missing preserves owner admission', async (t) => {
  const host = await installation(t);
  host.ctx.profileContext = { dir: host.dir };
  assert.deepEqual(await host.runReady(), []);
  await host.standalone();
  const saved = await host.call(
    '/auriga/settings/save',
    { platformOrigin: 'https://portal.example.com', deployment: 'cloud' },
    'native=valid',
    { 'x-auriga-settings': '1' },
  );
  assert.equal(saved.status, 200);
  assert.equal(saved.body.activeOrigin, '');
  await host.closed();
});

test('R2 plugin profile disposal does not destroy DSH local WebSockets', async (t) => {
  const host = await installation(t);
  host.ctx.profileContext = { dir: host.dir };
  assert.deepEqual(await host.runReady(), []);
  const req = host.request('/api/ws', undefined, 'native=valid', {
    upgrade: 'websocket',
  });
  assert.equal(host.ctx.connection.admit(req).peer, nativePeer);
  host.closeProfile();
  assert.equal(req.socket.destroyed, undefined);
  await host.closed();
});
