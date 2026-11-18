/* global AbortSignal, Headers */
import test from 'node:test';
import assert from 'node:assert/strict';
import { createServer as httpsServer } from 'node:https';
import { createServer, get as httpGet } from 'node:http';
import {
  randomUUID,
  randomBytes,
  generateKeyPairSync,
  sign,
} from 'node:crypto';
import { mkdtemp, writeFile, readFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import tls from 'node:tls';
import { Agent } from 'undici';
import { setTimeout as delay } from 'node:timers/promises';
import { apply } from '../src/plugin.js';
import { createTestPki } from './network/transport/support/pki.js';
import { startNatsServer } from './network/transport/support/nats-server.js';
import { ensureNatsServer } from './network/transport/support/nats-binary.js';
import { connectPeer } from './network/transport/support/client.js';
import {
  publicationFixture,
  testRoots,
  signedV2Keyset,
  signedV2Announcement,
  signedReadyResult,
} from './network/trust/keyset-fixtures.js';
import {
  panelManifestBase,
  signIdentity,
  signPanelManifest,
} from './managed-helpers.js';
import { apply as attachObserver } from '../../../infra/agent/internal/renderer/resource_observer.mjs';
import { decodeJson, encodeJson } from '../src/network/session/frames.js';

test(
  'real managed plugin entry restores file-only platform access without a broker or helper',
  { timeout: 60000 },
  async (t) => {
    const dir = await mkdtemp(join(tmpdir(), 'auriga-managed-file-entry-'));
    const cleanup = [];
    t.after(async () => {
      for (const stop of cleanup.reverse()) await stop();
      await rm(dir, { recursive: true, force: true });
    });
    const nats = await startNatsServer({ binary: await ensureNatsServer() });
    cleanup.push(() => nats.close());
    const peer = await connectPeer(nats, { token: 'fixture-no-auth-broker' });
    cleanup.push(() => peer.close());
    const originalTls = tls.connect;
    t.mock.method(tls, 'connect', function (options, ...args) {
      return originalTls.call(
        this,
        Number(options.port) === nats.port
          ? { ...options, ca: nats.rootPem }
          : options,
        ...args,
      );
    });
    const pki = createTestPki(),
      key = generateKeyPairSync('ec', { namedCurve: 'P-256' }),
      edge = generateKeyPairSync('ed25519');
    const ids = {
      tenantId: randomUUID(),
      ownerId: randomUUID(),
      instanceId: randomUUID(),
      deviceId: randomUUID(),
    };
    const credential = randomBytes(32).toString('base64url');
    const modelCredential = [
      'amc',
      ids.tenantId,
      ids.ownerId,
      ids.instanceId,
      ids.deviceId,
      randomBytes(32).toString('base64url'),
    ].join('.');
    const signing = publicationFixture();
    const panelManifest = signPanelManifest(
      edge.privateKey,
      'edge',
      panelManifestBase(),
    );
    const identity = {
      tenant: ids.tenantId,
      device: ids.deviceId,
      realm: '2',
      node: '64',
      systemAddress: 'aur1:2.40.0',
    };
    const vectors = JSON.parse(
      await readFile(
        new URL(
          '../../../contracts/network/fixtures/client-subjects.json',
          import.meta.url,
        ),
      ),
    );
    const calls = [],
      frames = [];
    let tickets = 0,
      registers = 0,
      token,
      origin,
      hold;
    peer.subscribe('auriga.net.up.*.*', {
      callback(error, msg) {
        assert.ifError(error);
        const frame = decodeJson(msg.data);
        frames.push(frame);
        const ingress = msg.subject.split('.').at(-1);
        peer.publish(
          'auriga.net.down.' + ingress,
          encodeJson(
            signedReadyResult(signing, frame, { epoch: String(frames.length) }),
          ),
        );
      },
    });
    await peer.flush();
    const upstream = httpsServer(
      { key: pki.keyPem, cert: pki.certPem },
      async (req, res) => {
        const path = new URL(req.url, origin).pathname;
        const chunks = [];
        for await (const bytes of req) chunks.push(bytes);
        const body = Buffer.concat(chunks).toString();
        calls.push({
          path,
          method: req.method,
          authorization: req.headers.authorization,
          body,
        });
        const json = (value, status = 200) => {
          res.writeHead(status, { 'content-type': 'application/json' });
          res.end(JSON.stringify(value));
        };
        if (path === '/oauth2/token') {
          const form = new URLSearchParams(body);
          assert.equal(form.get('credential'), credential);
          assert.equal(
            form.get('grant_type'),
            'urn:auriga:params:oauth:grant-type:managed-device',
          );
          const now = Math.floor(Date.now() / 1000);
          const input = [
            { alg: 'ES256', kid: 'managed-jwk', typ: 'at+jwt' },
            {
              iss: origin,
              aud: 'auriga',
              ptyp: 'device',
              ctx: 'device',
              scope: 'auriga.connect',
              tid: ids.tenantId,
              sub: ids.ownerId,
              did: ids.deviceId,
              iat: now,
              exp: now + 180,
            },
          ]
            .map((v) => Buffer.from(JSON.stringify(v)).toString('base64url'))
            .join('.');
          token =
            input +
            '.' +
            sign('sha256', Buffer.from(input), {
              key: key.privateKey,
              dsaEncoding: 'ieee-p1363',
            }).toString('base64url');
          return json({
            access_token: token,
            token_type: 'Bearer',
            scope: 'auriga.connect',
            expires_in: 180,
          });
        }
        if (path === '/oauth2/jwks')
          return json({
            keys: [
              {
                ...key.publicKey.export({ format: 'jwk' }),
                kid: 'managed-jwk',
                alg: 'ES256',
                use: 'sig',
              },
            ],
          });
        if (path === '/api/workspace/v1/instance/heartbeat')
          return json({
            protocol: { current: 1, minSupported: 1 },
            nextHeartbeatSeconds: 30,
          });
        if (path === '/panel/manifest.json') return json(panelManifest);
        assert.equal(new Headers(req.headers).has('cookie'), false);
        assert.equal(
          req.headers.authorization,
          path.startsWith('/ai/v1/')
            ? 'Bearer ' + modelCredential
            : path.endsWith('/bootstrap/controls')
              ? undefined
              : 'Bearer ' + token,
        );
        if (path === '/api/iam/v1/me/device-profile')
          return json({
            account: {
              id: ids.ownerId,
              tenantId: ids.tenantId,
              displayName: 'Managed owner',
            },
          });
        if (path === '/api/iam/v1/network/register')
          return json({ ...identity, created: registers++ === 0 });
        if (path.endsWith('/bootstrap/controls'))
          return json(signedV2Keyset(signing.payload, ids.tenantId).control);
        if (path.endsWith('/keys/announcement'))
          return json(signedV2Announcement(signing.payload, {}, ids.tenantId));
        if (path.endsWith('/network/keys'))
          return json(signedV2Keyset(signing.payload, ids.tenantId));
        if (path.endsWith('/network/tickets'))
          return json({
            ...vectors[tickets++],
            identity,
            serverAudience: 'fixture-broker',
            expiresAt: new Date(Date.now() + 30000).toISOString(),
            connectionExpiresAt: new Date(Date.now() + 3600000).toISOString(),
            protocolVersion: 1,
          });
        if (path === '/ai/mcp') {
          const rpc = JSON.parse(body);
          if (rpc.method === 'notifications/initialized') {
            res.writeHead(202);
            res.end();
            return;
          }
          return json({
            jsonrpc: '2.0',
            id: rpc.id,
            result:
              rpc.method === 'initialize'
                ? { protocolVersion: '2025-06-18' }
                : rpc.method === 'tools/list'
                  ? {
                      tools: [
                        'auriga_list_endpoints',
                        'auriga_open_thread',
                        'auriga_reply_thread',
                        'auriga_read_thread',
                      ].map((name) => ({
                        name,
                        inputSchema: { type: 'object' },
                      })),
                    }
                  : {},
          });
        }
        if (path === '/api/resource/v1/packages') {
          if (hold) await hold;
          return json({ items: [] });
        }
        if (path === '/api/resource/v1/revocations') return json({ items: [] });
        if (path === '/ai/v1/models')
          return json({
            object: 'list',
            data: [
              {
                id: 'managed-model',
                object: 'model',
                owned_by: 'auriga',
                created: 1,
              },
            ],
          });
        return json({}, 404);
      },
    );
    await new Promise((resolve) => upstream.listen(0, '127.0.0.1', resolve));
    origin = 'https://127.0.0.1:' + upstream.address().port;
    cleanup.push(async () => {
      upstream.closeAllConnections();
      await new Promise((resolve) => upstream.close(resolve));
    });
    const dispatcher = new Agent({ connect: { ca: pki.rootPem } });
    cleanup.push(() => dispatcher.close());
    const fetcher = globalThis.fetch;
    t.mock.method(globalThis, 'fetch', (url, init = {}) =>
      fetcher(
        url,
        new URL(url).origin === origin ? { ...init, dispatcher } : init,
      ),
    );
    const file = join(dir, 'device-credential.json');
    const material = {
      ...ids,
      credential,
      expiresAt: new Date(Date.now() + 3600000).toISOString(),
      platformOrigin: origin,
      issuer: origin,
      network: { realm: '2', roots: testRoots, wssUrl: nats.url },
    };
    await writeFile(file, JSON.stringify(material), { mode: 0o600 });
    await writeFile(
      join(dir, 'model-credential.json'),
      JSON.stringify({
        tenantId: ids.tenantId,
        ownerId: ids.ownerId,
        instanceId: ids.instanceId,
        credentialVersion: 1,
        credential: modelCredential,
        baseUrl: origin + '/ai/v1',
        models: [{ id: 'managed-model' }],
      }),
      { mode: 0o600 },
    );
    const keysFile = join(dir, 'identity-keys.json'),
      instanceFile = join(dir, 'instance-token');
    await writeFile(
      keysFile,
      JSON.stringify({
        keys: [
          {
            ...edge.publicKey.export({ format: 'jwk' }),
            kid: 'edge',
            alg: 'EdDSA',
            use: 'sig',
          },
        ],
      }),
    );
    const instanceToken = randomBytes(32).toString('base64url');
    await writeFile(instanceFile, instanceToken);
    for (const [name, value] of Object.entries({
      AURIGA_MODE: 'managed',
      AURIGA_INSTANCE_ID: ids.instanceId,
      AURIGA_OWNER_ID: ids.ownerId,
      AURIGA_TENANT_ID: ids.tenantId,
      AURIGA_PLATFORM_URL: origin,
      AURIGA_IDENTITY_KEYS_FILE: keysFile,
      AURIGA_PANEL_KEYS_FILE: keysFile,
      AURIGA_INSTANCE_TOKEN_FILE: instanceFile,
      AURIGA_DEVICE_CREDENTIAL_FILE: file,
      AURIGA_WORKSPACE_ROOT: dir,
    })) {
      const previous = process.env[name];
      process.env[name] = value;
      cleanup.push(() => {
        if (previous === undefined) delete process.env[name];
        else process.env[name] = previous;
      });
    }
    const routes = [],
      effects = [],
      jobs = [],
      sessions = new Map(),
      tools = new Map();
    let adapter;
    const server = createServer(async (req, res) => {
      for (const route of [...routes].sort(
        (a, b) => b.path.length - a.path.length,
      ))
        if (req.url.startsWith(route.path)) {
          await route.handler(req, res);
          if (res.writableEnded) return;
        }
      res.writeHead(404);
      res.end();
    });
    await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
    cleanup.push(async () => {
      server.closeAllConnections();
      await new Promise((resolve) => server.close(resolve));
    });
    const local = 'http://127.0.0.1:' + server.address().port;
    const ctx = {
      logger: { warn: (line) => process.stderr.write(line + '\n') },
      webServer: {
        server,
        port: server.address().port,
        protocol: 'http:',
        register(route) {
          routes.push(route);
          return () => {};
        },
      },
      connection: { requestRejection: () => 401, register: () => () => {} },
      profileContext: { dir },
      tools: {
        register(tool) {
          tools.set(tool.name, tool);
          return () => tools.delete(tool.name);
        },
      },
      sessions: { get: (id) => sessions.get(id), flush: () => true },
      sessionController: {
        create({ sessionId }) {
          sessions.set(sessionId, { id: sessionId, header: { id: sessionId } });
          return { sessionId };
        },
        prompt() {},
      },
      llm: {
        registerAdapter(_providers, value) {
          adapter = value;
          return () => {
            adapter = undefined;
          };
        },
      },
      on: () => () => {},
      provide() {},
      effect(fn) {
        effects.push(fn());
      },
      inject(names, fn) {
        if (names.every((name) => ctx[name]))
          jobs.push(Promise.resolve().then(() => fn(ctx)));
      },
    };
    cleanup.push(async () => {
      for (const dispose of effects.reverse()) await dispose?.();
    });
    await apply(ctx, {
      browserOrigin: {
        scheme: 'http',
        host: '127.0.0.1',
        port: server.address().port,
      },
    });
    const policyFile = join(dir, 'observer-policy.json');
    await writeFile(
      policyFile,
      JSON.stringify({ schemaVersion: 1, newSessionRequired: false }),
    );
    let observationPause;
    await attachObserver(
      {
        ...ctx,
        agentPresets: { acquireScope: async (key) => ({ key }) },
        skills: {
          async list() {
            observationPause?.started.resolve();
            await observationPause?.release.promise;
            return [];
          },
          get: () => undefined,
        },
        pluginManager: { listBundles: () => [], listPlugins: () => [] },
      },
      { tokenFile: instanceFile, policyFile },
    );
    const observe = () =>
      new Promise((resolve, reject) => {
        const req = httpGet(
          local + '/auriga-node-resource-catalog',
          {
            headers: { Authorization: 'Bearer ' + instanceToken },
            signal: AbortSignal.timeout(5000),
          },
          (res) => {
            res.resume();
            res.once('end', () => resolve(res.statusCode));
            res.once('error', reject);
          },
        );
        req.once('error', reject);
      });
    for (let i = 0; i < jobs.length; i++) await jobs[i];
    const request = (path, init = {}) =>
      globalThis.fetch(local + path, {
        ...init,
        headers: {
          origin: local,
          'Auriga-Identity': signIdentity(edge.privateKey, 'edge', {
            aud: ids.instanceId,
            sub: ids.ownerId,
            tid: ids.tenantId,
            htm: init.method ?? 'GET',
            htu: path,
          }).token,
          ...init.headers,
        },
      });
    const until = async (work) => {
      const deadline = Date.now() + 40000;
      for (;;) {
        const result = await work();
        if (result) return result;
        assert.ok(
          Date.now() < deadline,
          'managed file entry becomes available ' +
            JSON.stringify(lastStatus) +
            JSON.stringify(calls.map((c) => c.path)),
        );
        await delay(20);
      }
    };
    let lastStatus;
    const status = await until(async () => {
      const response = await request('/auriga/shell/status');
      if (!response.ok) return;
      const body = await response.json();
      lastStatus = body.network;
      return body.network?.connected && body;
    });
    assert.equal(status.session.available, true);
    assert.equal(await observe(), 200);
    const panel = await request('/auriga/bootstrap');
    assert.equal(panel.status, 200);
    assert.equal((await panel.json()).manifest.version, panelManifest.version);
    assert.equal(
      (
        await request('/auriga/bootstrap', {
          headers: { origin: 'https://wrong.example.com' },
        })
      ).status,
      403,
    );
    assert.equal(
      (await (await fetch(local + '/_auriga/health')).json()).ready,
      true,
    );
    assert.equal(frames.length, 2);
    assert.equal(
      (await request('/auriga/resources/api/resource/v1/packages')).status,
      200,
    );
    await until(() => adapter);
    assert.equal((await adapter.listModels('auriga'))[0].id, 'managed-model');
    assert.ok(calls.some((call) => call.path === '/ai/mcp'));
    await until(() =>
      calls.some(
        (call) => call.path === '/api/workspace/v1/instance/heartbeat',
      ),
    );
    assert.equal((await request('/auriga/settings/state')).status, 200);
    const release = Promise.withResolvers();
    hold = release.promise;
    const pending = request('/auriga/resources/api/resource/v1/packages');
    await until(
      () =>
        calls.filter((call) => call.path === '/api/resource/v1/packages')
          .length === 2,
    );
    observationPause = {
      started: Promise.withResolvers(),
      release: Promise.withResolvers(),
    };
    const pendingObservation = observe();
    await observationPause.started.promise;
    await rm(file);
    assert.equal(await observe(), 401);
    observationPause.release.resolve();
    assert.equal(await pendingObservation, 403);
    assert.equal(
      (await request('/auriga/resources/api/resource/v1/packages')).status,
      401,
    );
    // The native guard retires the old operation before a late platform response.
    release.resolve();
    assert.ok((await pending).status >= 400);
    // The network refresh also detaches the old runtime. This must not reopen
    // observation with the still-mounted instance token.
    await delay(1250);
    assert.equal(await observe(), 401);
    const health = await fetch(local + '/_auriga/health');
    assert.equal(health.status, 200);
    assert.equal((await health.json()).ready, false);
  },
);
