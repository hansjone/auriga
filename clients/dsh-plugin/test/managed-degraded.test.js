import test, { after } from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import { Buffer } from 'node:buffer';
import { generateKeyPairSync, sign } from 'node:crypto';
import { mkdtemp, writeFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { setTimeout as delay } from 'node:timers/promises';
import { createManaged } from '../src/modes/managed.js';
import {
  makeIdentityKeys,
  signIdentity,
  INSTANCE_ID,
  OWNER_ID,
} from './managed-helpers.js';

// Exercise the production managed mode, file binding, ES256 verification,
// health route and both identity guards. Only remote IAM/message/model services
// and the host's model handler are fixtures; no real DSH, PG or model is started.
for (const credential of [
  'valid',
  'rejected',
  'revoked',
  'expired',
  'bad-signature',
  'closing-grant',
  'closing-jwks',
  'closing-profile',
])
  test(`message outage preserves model admission only with ${credential} managed identity`, async (t) => {
    const directory = await mkdtemp(join(tmpdir(), 'auriga-degraded-'));
    t.after(() => rm(directory, { recursive: true, force: true }));
    const tenantId = '00000000-0000-4000-8000-000000000001';
    const deviceId = '00000000-0000-4000-8000-000000000004';
    const origin = 'https://platform.example.com';
    const file = join(directory, 'device.json');
    const root = generateKeyPairSync('ed25519');
    await writeFile(
      file,
      JSON.stringify({
        tenantId,
        instanceId: INSTANCE_ID,
        ownerId: OWNER_ID,
        deviceId,
        credential: 'a'.repeat(43),
        expiresAt: new Date(
          Date.now() + (credential === 'expired' ? -1000 : 600000),
        ).toISOString(),
        platformOrigin: origin,
        issuer: origin,
        network: {
          realm: '9',
          wssUrl: 'wss://platform.example.com/net/v1/ws',
          roots: [
            {
              kid: 'root',
              public_key_spki: root.publicKey
                .export({ format: 'der', type: 'spki' })
                .toString('base64url'),
            },
          ],
        },
      }),
      { mode: 0o600 },
    );
    const issuer = generateKeyPairSync('ec', { namedCurve: 'prime256v1' });
    const now = Math.floor(Date.now() / 1000);
    const jwt = [
      { alg: 'ES256', kid: 'iam' },
      {
        iss: origin,
        aud: 'auriga',
        ptyp: 'device',
        ctx: 'device',
        scope: 'auriga.connect',
        tid: tenantId,
        sub: OWNER_ID,
        did: deviceId,
        iat: now,
        exp: now + 300,
      },
    ]
      .map((value) => Buffer.from(JSON.stringify(value)).toString('base64url'))
      .join('.');
    const signature = sign('sha256', Buffer.from(jwt), {
      key: issuer.privateKey,
      dsaEncoding: 'ieee-p1363',
    });
    if (credential === 'bad-signature') signature[0] ^= 1;
    const accessToken = jwt + '.' + signature.toString('base64url');
    let exchanges = 0,
      messageCalls = 0,
      modelCalls = 0,
      recovered = false;
    let entered;
    const pendingRequest = new Promise((resolve) => {
      entered = resolve;
    });
    t.mock.method(globalThis, 'fetch', async (input, init) => {
      const url = new URL(input);
      assert.equal(url.origin, origin);
      const heldPath = {
        'closing-grant': '/oauth2/token',
        'closing-jwks': '/oauth2/jwks',
        'closing-profile': '/api/iam/v1/network/systems',
      }[credential];
      if (heldPath && url.pathname === heldPath) {
        return new Promise((resolve, reject) => {
          const stop = () => {
            clearTimeout(timer);
            reject(new Error('fixture request aborted'));
          };
          const timer = setTimeout(stop, 60000);
          t.after(stop);
          init.signal.addEventListener('abort', stop, { once: true });
          entered();
        });
      }
      const response = (body, status = 200) =>
        new Response(JSON.stringify(body), { status });
      if (url.pathname === '/oauth2/token') {
        exchanges++;
        return (credential === 'rejected' && !recovered) ||
          credential === 'revoked'
          ? response(
              {
                code:
                  credential === 'revoked'
                    ? 'network.revoked'
                    : 'common.forbidden',
              },
              403,
            )
          : response({
              access_token: accessToken,
              token_type: 'Bearer',
              scope: 'auriga.connect',
              expires_in: 300,
            });
      }
      if (url.pathname === '/oauth2/jwks')
        return response({
          keys: [{ ...issuer.publicKey.export({ format: 'jwk' }), kid: 'iam' }],
        });
      if (url.pathname.startsWith('/api/iam/v1/network/systems'))
        return response({ items: [] });
      if (url.pathname === '/api/iam/v1/network/register') {
        messageCalls++;
        return response({ code: 'common.unavailable' }, 503);
      }
      if (url.pathname === '/ai/v1/chat/completions') {
        modelCalls++;
        return response({ choices: [{ message: { content: 'model-ok' } }] });
      }
      assert.fail('unexpected remote route: ' + url.pathname);
    });
    const routes = [];
    const server = http.createServer(async (req, res) => {
      const route = routes.find(
        (row) => row.kind === 'exact' && row.path === req.url?.split('?')[0],
      );
      if (route) return route.handler(req, res);
      if (req.url === '/api/session/prompt') {
        const response = await globalThis.fetch(
          origin + '/ai/v1/chat/completions',
        );
        res.writeHead(response.status);
        res.end(await response.text());
        return;
      }
      res.writeHead(404);
      res.end();
    });
    const effects = [];
    const ctx = {
      profileContext: { dir: directory },
      tools: {},
      sessions: {},
      sessionController: {},
      webServer: { server, register: (row) => routes.push(row) },
      inject(names, callback) {
        if (names.every((name) => this[name])) callback(this);
      },
      effect(callback) {
        effects.push(callback());
      },
    };
    const keys = makeIdentityKeys();
    t.after(() => rm(dirname(keys.keyFile), { recursive: true, force: true }));
    const runtime = await createManaged({
      env: {
        AURIGA_MODE: 'managed',
        AURIGA_INSTANCE_ID: INSTANCE_ID,
        AURIGA_OWNER_ID: OWNER_ID,
        AURIGA_TENANT_ID: tenantId,
        AURIGA_PLATFORM_URL: origin,
        AURIGA_DEVICE_CREDENTIAL_FILE: file,
      },
      identityKeys: keys.keySet,
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
    t.after(async () => {
      await dispose();
      await runtime.close();
      for (const effect of effects.reverse())
        if (typeof effect === 'function') await effect();
      server.closeAllConnections();
      await new Promise((resolve) => server.close(resolve));
    });
    await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
    if (credential.startsWith('closing-')) {
      await pendingRequest;
      assert.ok(process.getActiveResourcesInfo().includes('Timeout'));
      await dispose();
      await runtime.close();
      assert.equal(process.getActiveResourcesInfo().includes('Timeout'), false);
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
    const deadline = Date.now() + 5000;
    do {
      health = await request('/_auriga/health', false);
      if (
        credential === 'valid'
          ? health.body.ready && messageCalls > 0
          : credential === 'expired'
            ? runtime.networkStatus.reason === 'revoked'
            : exchanges > 0
      )
        break;
      await delay(10);
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
        credential === 'revoked' || credential === 'expired'
          ? /实例会话已失效，请从门户重新进入/
          : /连接中断，正在重试/,
      );
    }
    if (credential === 'rejected') {
      recovered = true;
      const deadline = Date.now() + 5000;
      do {
        health = await request('/_auriga/health', false);
        if (health.body.ready) break;
        await delay(10);
      } while (Date.now() < deadline);
      assert.equal(
        health.body.ready,
        true,
        'same managed material automatically recovers',
      );
      assert.equal((await request('/api/session/prompt')).status, 200);
    }
    if (credential === 'revoked' || credential === 'expired') {
      const before = exchanges;
      await delay(1100);
      assert.equal(
        exchanges,
        before,
        'permanent rejection does not retry grant',
      );
      if (credential === 'expired') assert.equal(exchanges, 0);
    }
    if (credential === 'valid') {
      assert.ok(messageCalls > 0, 'real message registration must fail first');
      assert.equal(prompt.body.choices[0].message.content, 'model-ok');
      assert.equal((await request('/api/session/prompt', false)).status, 401);
      assert.equal(modelCalls, 1, 'signed browser identity remains mandatory');
    }
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
