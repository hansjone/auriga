import test, { after } from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { setTimeout as delay } from 'node:timers/promises';
import { createManaged } from '../src/modes/managed.js';
import { brokerFixture } from './broker-fixture.js';
import {
  makeIdentityKeys,
  signIdentity,
  INSTANCE_ID,
  OWNER_ID,
} from './managed-helpers.js';

// Real managed HTTP identity door, inherited identity and session guard. The
// local prompt handler represents native/personal model execution, not a claim
// that the production platform model metadata/tokenizer has been supplied.
for (const credential of [
  'valid',
  'rejected',
  'revoked',
  'expired',
  'bad-binding',
  'closing-snapshot',
  'closing-profile',
  'closing-team',
])
  test(`message outage preserves native model admission only with ${credential} managed shell identity`, async (t) => {
    const directory = await mkdtemp(join(tmpdir(), 'auriga-degraded-'));
    const cleanups = [() => rm(directory, { recursive: true, force: true })];
    t.after(async () => {
      const errors = [];
      for (const cleanup of cleanups.toReversed()) {
        try {
          await cleanup();
        } catch (error) {
          errors.push(error);
        }
      }
      if (errors.length === 1) throw errors[0];
      if (errors.length > 1)
        throw new AggregateError(errors, 'managed fixture cleanup failed');
    });
    const connection = brokerFixture(),
      tenantId = connection.identity.tenantId;
    connection.identity.memberId = OWNER_ID;
    if (credential === 'rejected') connection.transition('signed_out');
    if (['revoked', 'expired'].includes(credential))
      connection.transition('revoked');
    if (credential === 'bad-binding')
      connection.transition('authenticated', {
        memberId: connection.identity.deviceId,
      });
    let messageCalls = 0,
      modelCalls = 0;
    const entered = Promise.withResolvers(),
      original = connection.ipc.call;
    connection.ipc.call = async (message) => {
      if (
        (credential === 'closing-snapshot' && message.action === 'snapshot') ||
        (credential === 'closing-profile' &&
          message.call?.operation === 'profile_read') ||
        (credential === 'closing-team' &&
          message.call?.operation === 'team_initialize')
      ) {
        entered.resolve();
        return new Promise(() => {});
      }
      if (message.call?.operation === 'team_initialize') {
        messageCalls++;
        return {
          requestId: message.requestId,
          operation: 'team_initialize',
          error: {
            code: 'common.unavailable',
            retryable: true,
            upstreamStatus: 503,
          },
        };
      }
      return original(message);
    };
    t.mock.method(globalThis, 'fetch', () =>
      assert.fail(
        'the plugin must not refresh, exchange or send a platform token',
      ),
    );
    const routes = [],
      effects = [];
    const server = http.createServer((req, res) => {
      const route = routes.find(
        (row) => row.kind === 'exact' && row.path === req.url?.split('?')[0],
      );
      if (route) return route.handler(req, res);
      if (req.url === '/api/session/prompt') {
        modelCalls++;
        res.writeHead(200, { 'content-type': 'application/json' });
        res.end(
          JSON.stringify({ choices: [{ message: { content: 'model-ok' } }] }),
        );
        return;
      }
      res.writeHead(404);
      res.end();
    });
    const ctx = {
      profileContext: { dir: directory },
      tools: {
        register() {
          return () => {};
        },
      },
      sessions: {},
      sessionController: {},
      webServer: {
        server,
        register: (row) => {
          routes.push(row);
          return () => {};
        },
      },
      inject(names, callback) {
        if (names.every((name) => this[name])) callback(this);
      },
      effect(callback) {
        effects.push(callback());
      },
    };
    const keys = makeIdentityKeys();
    cleanups.push(() =>
      rm(dirname(keys.keyFile), { recursive: true, force: true }),
    );
    cleanups.push(async () => {
      server.closeAllConnections();
      await new Promise((resolve) => server.close(resolve));
    });
    const runtime = await createManaged({
      env: {
        AURIGA_MODE: 'managed',
        AURIGA_INSTANCE_ID: INSTANCE_ID,
        AURIGA_OWNER_ID: OWNER_ID,
        AURIGA_TENANT_ID: tenantId,
        AURIGA_PLATFORM_URL: 'https://platform.example.com',
      },
      identityKeys: keys.keySet,
      connection,
      config: {
        browserOrigin: {
          scheme: 'https',
          host: 'instance.i.example.com',
          port: 443,
        },
      },
      watchKeys: false,
      startHeartbeat: false,
    });
    const dispose = runtime.apply(ctx);
    cleanups.push(
      ...effects.filter((effect) => typeof effect === 'function'),
      () => runtime.close(),
      dispose,
    );
    await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
    if (credential.startsWith('closing-')) {
      await entered.promise;
      await dispose();
      await runtime.close();
      assert.equal(modelCalls, 0);
      return;
    }
    const request = (path, authenticated = true) =>
      new Promise((resolve, reject) => {
        const method = path === '/api/session/prompt' ? 'POST' : 'GET';
        const token = signIdentity(keys.privateKey, keys.kid, {
          htm: method,
          htu: path,
          tid: tenantId,
        }).token;
        const req = http.request(
          {
            host: '127.0.0.1',
            port: server.address().port,
            path,
            method,
            agent: false,
            headers: {
              'accept-language': 'zh-CN',
              ...(authenticated ? { 'Auriga-Identity': token } : {}),
            },
          },
          (res) => {
            let body = '';
            res.on('data', (chunk) => (body += chunk));
            res.on('end', () =>
              resolve({
                status: res.statusCode,
                body: path === '/' ? body : JSON.parse(body),
              }),
            );
          },
        );
        req.on('error', reject);
        req.end();
      });
    let health;
    const deadline = Date.now() + 3000;
    do {
      health = await request('/_auriga/health', false);
      if (credential !== 'valid' || (health.body.ready && messageCalls > 0))
        break;
      await delay(5);
    } while (Date.now() < deadline);
    assert.equal(health.status, 200);
    assert.equal(health.body.ready, credential === 'valid');
    assert.equal(runtime.networkStatus.connected, false);
    const prompt = await request('/api/session/prompt');
    assert.equal(prompt.status, credential === 'valid' ? 200 : 401);
    assert.equal(modelCalls, credential === 'valid' ? 1 : 0);
    if (credential !== 'valid') {
      const index = await request('/');
      assert.equal(index.status, 401);
      assert.match(
        index.body,
        ['revoked', 'expired'].includes(credential)
          ? /\u5b9e\u4f8b\u4f1a\u8bdd\u5df2\u5931\u6548/
          : /\u8fde\u63a5\u4e2d\u65ad/,
      );
    }
    if (credential === 'rejected') {
      connection.transition('authenticated');
      do {
        health = await request('/_auriga/health', false);
        if (health.body.ready) break;
        await delay(5);
      } while (Date.now() < deadline);
      assert.equal(
        health.body.ready,
        true,
        'the current shell identity recovers by push',
      );
      assert.equal((await request('/api/session/prompt')).status, 200);
    }
    if (credential === 'valid') {
      assert.ok(messageCalls > 0, 'the actual Team initializer failed first');
      assert.equal(prompt.body.choices[0].message.content, 'model-ok');
      assert.equal((await request('/api/session/prompt', false)).status, 401);
      assert.equal(modelCalls, 1, 'signed browser admission remains mandatory');
      connection.logout();
      assert.equal((await request('/api/session/prompt')).status, 401);
      assert.equal(modelCalls, 1);
    }
    assert.equal(
      connection.calls.some((c) => /oauth2/.test(c.path ?? '')),
      false,
    );
  });

test('directory cleanup failure leaves no managed fixture listener', () => {
  const env = { ...process.env };
  delete env.NODE_TEST_CONTEXT;
  const result = spawnSync(
    process.execPath,
    [
      '--import',
      new URL('./fixtures/managed-cleanup-failure.mjs', import.meta.url).href,
      '--test',
      '--test-name-pattern=message outage.*rejected',
      fileURLToPath(import.meta.url),
    ],
    { encoding: 'utf8', timeout: 10000, env },
  );
  assert.equal(result.error, undefined, result.stdout + result.stderr);
  assert.equal(
    result.status,
    1,
    'injected cleanup error must still fail the child',
  );
  assert.match(result.stdout, /ENOTEMPTY/);
  assert.match(result.stdout, /managed_cleanup_before_remove \[\]/);
  assert.match(result.stdout, /managed_cleanup_after_hooks \[\]/);
  assert.doesNotMatch(
    result.stdout,
    /managed fixture must release timers and TCP handles/,
  );
});

after(async () => {
  // Native TCP destruction may finish after server.close's callback on Windows.
  const remainingResources = () =>
    process
      .getActiveResourcesInfo()
      .filter((type) =>
        ['Timeout', 'TCPWrap', 'TCPSocketWrap', 'TCPServerWrap'].includes(type),
      );
  const deadline = Date.now() + 1000;
  while (remainingResources().length && Date.now() < deadline) await delay(5);
  const remaining = remainingResources();
  assert.deepEqual(
    remaining,
    [],
    'managed fixture must release timers and TCP handles',
  );
});
