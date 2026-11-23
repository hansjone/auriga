import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createServer, request } from 'node:http';
import { generateKeyPairSync, randomBytes } from 'node:crypto';
import { mkdtemp, rm, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import { createManaged } from '../src/modes/managed.js';
import { apply as attachObserver } from '../../../infra/agent/internal/renderer/resource_observer.mjs';
import { INSTANCE_ID, OWNER_ID, signIdentity } from './managed-helpers.js';

const catalogPath = '/auriga-node-resource-catalog';
const health = '/_auriga/health';
const status = '/auriga/shell/status';
const host = 'instance.i.example.com';
const origin = 'https://' + host;

test('production managed door confines the node Bearer to the renderer observer', async (t) => {
  const dir = await mkdtemp(join(tmpdir(), 'auriga-observation-'));
  const tokenFile = join(dir, 'instance.token');
  const observerTokenFile = join(dir, 'observer.token');
  const policyFile = join(dir, 'policy.json');
  const token = randomBytes(32).toString('base64url');
  const otherToken = randomBytes(32).toString('base64url');
  const bearer = { Authorization: 'Bearer ' + token };
  await writeFile(tokenFile, token + '\n', { mode: 0o600 });
  await writeFile(observerTokenFile, token + '\n', { mode: 0o600 });
  await writeFile(
    policyFile,
    JSON.stringify({ schemaVersion: 1, newSessionRequired: false }),
  );

  const { privateKey, publicKey } = generateKeyPairSync('ed25519');
  const identity = (htu, htm = 'GET') => ({
    'Auriga-Identity': signIdentity(privateKey, 'edge', { htm, htu }).token,
  });
  const clock = Math.floor(Date.now() / 1000);
  const runtime = await createManaged({
    env: {
      AURIGA_INSTANCE_ID: INSTANCE_ID,
      AURIGA_OWNER_ID: OWNER_ID,
      AURIGA_PLATFORM_URL: 'https://platform.example.com',
      AURIGA_INSTANCE_TOKEN_FILE: tokenFile,
    },
    now: () => clock,
    identityKeys: {
      keys: [{ ...publicKey.export({ format: 'jwk' }), kid: 'edge' }],
    },
    config: { browserOrigin: { scheme: 'https', host, port: 443 } },
    startHeartbeat: false,
  });

  const routes = [];
  const ordinaryRequests = [];
  const server = createServer((req, res) => {
    ordinaryRequests.push(req.url);
    const path = req.url.split('?')[0];
    const route = routes.find((row) =>
      row.kind === 'exact' ? path === row.path : path.startsWith(row.path),
    );
    if (route) void route.handler(req, res);
    else res.writeHead(404).end();
  });
  server.on('upgrade', (_req, socket) => socket.destroy());
  const connection = {
    browserPeer: {
      principal: { kind: 'owner' },
      signal: new AbortController().signal,
    },
    admit: () => ({ rejection: 401 }),
    requestRejection: () => 401,
    authorizeIndex: () => false,
  };
  const effects = [];
  const ctx = {
    connection,
    webServer: {
      server,
      register(route) {
        routes.push(route);
      },
    },
    effect: (effect) => effects.push(effect()),
    inject() {},
    on: () => () => {},
  };
  const stop = runtime.apply(ctx);
  t.after(async () => {
    stop();
    server.closeAllConnections();
    if (server.listening) await new Promise((resolve) => server.close(resolve));
    await rm(dir, { recursive: true, force: true });
  });
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));

  const send = (path, headers = {}, method = 'GET') =>
    new Promise((resolve, reject) => {
      const req = request(
        {
          hostname: '127.0.0.1',
          port: server.address().port,
          path,
          headers,
          method,
          agent: false,
        },
        (res) => {
          let body = '';
          res.setEncoding('utf8');
          res.on('data', (part) => {
            body += part;
          });
          res.on('end', () =>
            resolve({ status: res.statusCode, headers: res.headers, body }),
          );
          res.on('error', reject);
        },
      );
      req.on('upgrade', (_res, socket) => {
        socket.destroy();
        reject(new Error('unexpected upgrade'));
      });
      req.on('error', reject);
      req.end();
    });

  assert.equal(
    (await send(catalogPath, bearer)).status,
    503,
    'observer not bound',
  );
  let observed = 0;
  let released = 0;
  const observerCtx = {
    ...ctx,
    // No registration with DSH, even if an unrelated host route has this name.
    webServer: {
      server,
      register: () => assert.fail('observer registered as a host route'),
    },
    agentPresets: {
      async acquireScope(key) {
        observed++;
        return {
          key,
          async [Symbol.asyncDispose]() {
            released++;
          },
        };
      },
    },
    skills: {
      list: () => [
        { name: 'fixture', provider: 'fixture', path: '/fiction/SKILL.md' },
      ],
      get: () => ({ content: 'fictional fixture body' }),
    },
    pluginManager: { listBundles: () => [], listPlugins: () => [] },
  };
  await attachObserver(observerCtx, {
    tokenFile: observerTokenFile,
    policyFile,
  });
  const detach = effects.pop();
  t.after(detach);
  // A public prefix route, if present, must never receive an admitted Bearer.
  routes.push({
    kind: 'prefix',
    path: catalogPath,
    handler: () => assert.fail('node request escaped into the host router'),
  });

  const admitted = await send(catalogPath, bearer);
  assert.equal(admitted.status, 200);
  assert.equal(admitted.headers['cache-control'], 'no-store');
  assert.deepEqual(
    JSON.parse(admitted.body).skills.map((row) => row.preset),
    ['cordis', 'standard', 'ptc'],
  );
  assert.equal(released, 3);
  assert.equal(admitted.body.includes(token), false);
  assert.deepEqual(ordinaryRequests, []);
  assert.equal(runtime.activity.snapshot().lastInteractionAt, undefined);

  for (const [path, headers, method] of [
    [catalogPath, {}],
    [catalogPath, { Authorization: 'Bearer' }],
    [catalogPath, { Authorization: 'Bearer ' + otherToken }],
    [
      catalogPath,
      { Authorization: ['Bearer ' + token, 'Bearer ' + otherToken] },
    ],
    [catalogPath, identity(catalogPath)], // Real portal signature, after Edge strips Authorization.
    [catalogPath, { ...bearer, ...identity(catalogPath) }],
    [catalogPath, { ...bearer, Origin: origin }],
    [catalogPath, { ...bearer, 'Sec-Fetch-Site': 'same-origin' }],
    [catalogPath, { 'X-Auriga-Node-Resource-Observer': 'true' }],
    [catalogPath, bearer, 'POST'],
    [catalogPath, bearer, 'HEAD'],
    [catalogPath, { ...bearer, Connection: 'Upgrade', Upgrade: 'websocket' }],
    [catalogPath + '?', bearer],
    [catalogPath + '/child', bearer],
    ['/nested/..' + catalogPath, bearer],
    ['/%61uriga-node-resource-catalog', bearer],
    ['http://127.0.0.1' + catalogPath, bearer],
    ['/auriga/bootstrap', bearer],
    ['/api/settings/describe', bearer],
  ])
    assert.equal(
      (await send(path, headers, method)).status,
      401,
      method ?? 'GET ' + path,
    );
  assert.equal(observed, 3);
  assert.deepEqual(ordinaryRequests, []);
  assert.equal(runtime.activity.snapshot().lastInteractionAt, undefined);

  // A distinct renderer token makes the second check observable in isolation.
  await writeFile(observerTokenFile, otherToken);
  assert.equal((await send(catalogPath, bearer)).status, 403);
  await writeFile(tokenFile, ' \n');
  assert.equal((await send(catalogPath, bearer)).status, 401);
  assert.equal(observed, 3);
  await writeFile(tokenFile, token);
  detach();
  assert.equal((await send(catalogPath, bearer)).status, 503);

  const readiness = await send(health);
  assert.equal(readiness.status, 200);
  // Managed network is unconfigured here, so /_auriga/health reports ready=true
  // while session-gated panel routes below still require a ready user session.
  assert.equal(JSON.parse(readiness.body).ready, true);
  assert.equal((await send(health, bearer, 'HEAD')).status, 200);
  assert.equal(runtime.activity.snapshot().lastInteractionAt, undefined);
  assert.equal((await send(status, identity(status))).status, 200);
  assert.ok(runtime.activity.snapshot().lastInteractionAt);
  assert.equal(
    (
      await send(status, {
        ...identity(status),
        Origin: 'https://wrong.example.com',
      })
    ).status,
    401,
  );
  assert.equal(
    (await send('/auriga/bootstrap', identity('/auriga/bootstrap'))).status,
    401,
    'panel still requires a ready user session',
  );
});

for (const released of [
  'entry',
  'runtime',
  'profile',
  'network',
  'overlapping entry',
]) {
  test(
    'node observation cannot publish after releasing the ' + released,
    async (t) => {
      const dir = await mkdtemp(join(tmpdir(), 'auriga-observation-lifetime-'));
      const tokenFile = join(dir, 'instance.token');
      const policyFile = join(dir, 'policy.json');
      const bearer = 'Bearer ' + randomBytes(32).toString('base64url');
      await writeFile(tokenFile, bearer.slice(7), { mode: 0o600 });
      await writeFile(
        policyFile,
        JSON.stringify({ schemaVersion: 1, newSessionRequired: false }),
      );
      const { publicKey } = generateKeyPairSync('ed25519');
      const managed = () =>
        createManaged({
          env: {
            AURIGA_INSTANCE_ID: INSTANCE_ID,
            AURIGA_OWNER_ID: OWNER_ID,
            AURIGA_PLATFORM_URL: 'http://127.0.0.1:1',
            AURIGA_INSTANCE_TOKEN_FILE: tokenFile,
          },
          identityKeys: {
            keys: [{ ...publicKey.export({ format: 'jwk' }), kid: 'edge' }],
          },
          config: { browserOrigin: { scheme: 'https', host, port: 443 } },
          startHeartbeat: false,
        });
      const server = createServer((_req, res) => res.writeHead(404).end());
      const original = server.emit;
      let endProfile, endNetwork, detachObserver;
      const entered = Promise.withResolvers();
      const release = Promise.withResolvers();
      let first = true;
      const runtimes = [];
      t.after(async () => {
        release.resolve();
        for (const runtime of runtimes.reverse()) await runtime.close();
        detachObserver?.();
        server.closeAllConnections();
        if (server.listening)
          await new Promise((resolve) => server.close(resolve));
        await rm(dir, { recursive: true, force: true });
      });
      const webServer = { server, register() {} };
      await attachObserver(
        {
          webServer,
          effect(fn) {
            detachObserver = fn();
          },
          agentPresets: { acquireScope: async (key) => ({ key }) },
          skills: {
            async list() {
              if (first) {
                first = false;
                entered.resolve();
                await release.promise;
              }
              return [];
            },
            get() {},
          },
          pluginManager: { listBundles: () => [], listPlugins: () => [] },
        },
        { tokenFile, policyFile },
      );
      const ctx = { webServer, effect: (fn) => fn(), on: () => () => {} };
      const base = await managed();
      runtimes.push(base);
      const stop = base.apply({
        ...ctx,
        inject(names, attach) {
          if (
            names[0] !== 'profileContext' ||
            !['profile', 'network'].includes(released)
          )
            return;
          attach({
            profileContext: { dir },
            effect(fn) {
              endProfile = fn();
            },
            inject(names, attach) {
              if (released === 'network' && names[0] === 'tools')
                attach({
                  effect(fn) {
                    endNetwork = fn();
                  },
                  inject() {},
                });
            },
          });
        },
      });
      await base.ready;
      await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
      const observe = () =>
        new Promise((resolve, reject) => {
          const req = request(
            {
              hostname: '127.0.0.1',
              port: server.address().port,
              path: catalogPath,
              headers: { Authorization: bearer },
              agent: false,
              timeout: 5000,
            },
            (res) => {
              res.resume();
              res.once('end', () => resolve(res.statusCode));
              res.once('error', reject);
            },
          );
          req.on('error', reject);
          req.on('timeout', () =>
            req.destroy(new Error('observation timed out')),
          );
          req.end();
        });
      const pending = observe();
      await Promise.race([
        entered.promise,
        pending.then(() => assert.fail('observer was not reached')),
      ]);
      const mountNext = async () => {
        const next = await managed();
        runtimes.push(next);
        return next.apply(ctx);
      };
      let stopNext;
      if (released === 'overlapping entry') stopNext = await mountNext();
      if (released === 'runtime') await base.close();
      else if (released === 'profile') endProfile?.();
      else if (released === 'network') await endNetwork?.();
      else await stop();
      release.resolve();
      const oldResponse = await pending;
      if (released === 'profile' || released === 'network') {
        assert.equal(await observe(), 401);
        await stop();
      }
      stopNext ??= await mountNext();
      assert.deepEqual([oldResponse, await observe()], [403, 200]);
      await stopNext();
      assert.equal(server.emit, original);
    },
  );
}
