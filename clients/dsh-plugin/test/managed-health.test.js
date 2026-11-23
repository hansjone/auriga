import { test } from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { setTimeout as delay } from 'node:timers/promises';
import { createActivityProbe } from '../src/managed/activity.js';
import { buildHealthBody, HEALTH_PATH } from '../src/managed/health.js';
import { createHeartbeatClient } from '../src/managed/heartbeat.js';
import { createAurigaSdk, SDK_VERSION } from '../src/managed/sdk.js';
import {
  checkProtocol,
  PROTOCOL_VERSION,
  MIN_SUPPORTED_PROTOCOL,
} from '../src/managed/protocol.js';
import { createManaged } from '../src/modes/managed.js';
import { brokerFixture } from './broker-fixture.js';
import {
  makeIdentityKeys,
  signPanelManifest,
  panelManifestBase,
  INSTANCE_ID,
  OWNER_ID,
  TENANT_ID,
} from './managed-helpers.js';

test('health path constant matches protocol', () => {
  assert.equal(HEALTH_PATH, '/_auriga/health');
});

test('health body reports only contract fields', () => {
  const body = buildHealthBody({
    pluginVersion: '0.0.0',
    ready: true,
  });
  assert.equal(body.status, 'ok');
  assert.equal(body.ready, true);
  assert.equal(body.protocol, PROTOCOL_VERSION);
  assert.equal(body.pluginVersion, '0.0.0');
  assert.equal('activity' in body, false);
});

test('activity probe notifies on change', () => {
  const activity = createActivityProbe();
  let n = 0;
  activity.onChange(() => {
    n += 1;
  });
  activity.setRunningTurns(1);
  activity.setOpenTerminals(2);
  activity.setRunningTurns(1); // same value → no notify
  assert.equal(n, 2);
  assert.equal(activity.snapshot().runningTurns, 1);
  assert.equal(activity.snapshot().openTerminals, 2);
  assert.equal(activity.snapshot().known.runningTurns, true);
});

test('heartbeat posts activity with Auriga-Protocol header', async () => {
  const activity = createActivityProbe();
  activity.update({
    runningTurns: 1,
    openTerminals: 0,
    activeImBots: 0,
    connectedClients: 1,
  });
  /** @type {RequestInit | undefined} */
  let seen;
  const fetchImpl = async (_url, init) => {
    seen = init;
    return {
      ok: true,
      status: 200,
      async json() {
        return {
          serverTime: new Date().toISOString(),
          protocol: { current: 1, minSupported: 1 },
          nextHeartbeatSeconds: 30,
        };
      },
    };
  };
  const client = createHeartbeatClient({
    platformUrl: 'https://edge.example.com',
    getInstanceToken: () => 'ait_test_token',
    pluginVersion: '0.0.0',
    dshVersion: '0.2.0-rc.2',
    activity,
    healthOk: () => true,
    fetchImpl: /** @type {typeof fetch} */ (fetchImpl),
  });
  const result = await client.send();
  assert.equal(result.ok, true);
  assert.equal(seen?.method, 'POST');
  const headers = /** @type {Record<string, string>} */ (seen?.headers);
  assert.equal(headers['auriga-protocol'], '1');
  assert.match(headers.authorization, /^Bearer ait_test_token$/);
  const body = JSON.parse(/** @type {string} */ (seen?.body));
  assert.equal(body.activity.runningTurns, 1);
  assert.equal(body.activity.openTerminals, 0);
  assert.equal(body.health.ok, true);
});

test('protocol accepts current and previous major', () => {
  assert.equal(checkProtocol(PROTOCOL_VERSION).ok, true);
  assert.equal(checkProtocol(MIN_SUPPORTED_PROTOCOL).ok, true);
  const old = checkProtocol(0);
  assert.equal(old.ok, false);
  assert.equal(old.code, 'plugin.protocol_unsupported');
});

test('auriga SDK exposes version identity kb notify files; no dshTeam', () => {
  const sdk = createAurigaSdk({
    getIdentity: () => ({ uid: OWNER_ID, displayName: 'Alice', role: 'owner' }),
  });
  assert.equal(sdk.version, SDK_VERSION);
  assert.deepEqual(sdk.identity(), {
    uid: OWNER_ID,
    displayName: 'Alice',
    role: 'owner',
  });
  assert.equal(typeof sdk.kb.query, 'function');
  assert.equal(typeof sdk.notify, 'function');
  assert.equal(typeof sdk.files.list, 'function');
  assert.equal('dshTeam' in sdk, false);
});

test('createManaged wires door keys health sdk', async () => {
  const keys = makeIdentityKeys();
  const panel = makeIdentityKeys('panel-kid');
  const manifest = signPanelManifest(
    panel.privateKey,
    panel.kid,
    panelManifestBase(),
  );
  const fetchImpl = async (url) => {
    const href = String(url);
    if (href.includes('/panel/manifest.json')) {
      return {
        ok: true,
        status: 200,
        async text() {
          return JSON.stringify(manifest);
        },
      };
    }
    if (href.includes('/heartbeat')) {
      return {
        ok: true,
        status: 200,
        async json() {
          return {
            serverTime: new Date().toISOString(),
            protocol: { current: 1, minSupported: 1 },
            nextHeartbeatSeconds: 30,
          };
        },
      };
    }
    return {
      ok: false,
      status: 404,
      async text() {
        return '';
      },
    };
  };

  const runtime = await createManaged({
    config: {
      browserOrigin: {
        scheme: 'https',
        host: 'instance.i.example.com',
        port: 443,
      },
    },
    env: {
      AURIGA_MODE: 'managed',
      AURIGA_INSTANCE_ID: INSTANCE_ID,
      AURIGA_OWNER_ID: OWNER_ID,
      AURIGA_TENANT_ID: TENANT_ID,
      AURIGA_PLATFORM_URL: 'https://edge.example.com',
    },
    identityKeys: keys.keySet,
    panelKeys: panel.keySet,
    instanceToken: 'ait_test',
    fetchImpl: /** @type {typeof fetch} */ (fetchImpl),
    watchKeys: false,
    startHeartbeat: false,
  });

  assert.equal(runtime.mode, 'managed');
  assert.equal(runtime.protocol, 1);
  const health = runtime.healthBody();
  assert.equal(health.ready, true);
  assert.equal(health.protocol, 1);

  const got = await runtime.getManifest();
  assert.equal(got.version, '1.0.0');
  assert.equal(got.element, 'auriga-team-panel');

  assert.equal(runtime.sdk.identity().uid, OWNER_ID);
  runtime.close();
});

async function startManagedHealthFixture(t, extraOptions = {}) {
  const directory = await mkdtemp(join(tmpdir(), 'auriga-managed-health-'));
  const keys = makeIdentityKeys();
  const routes = [];
  const effects = [];
  const server = http.createServer((req, res) => {
    const path = req.url?.split('?')[0];
    const route = routes.find(
      (row) => row.kind === 'exact' && row.path === path,
    );
    if (route) return route.handler(req, res);
    res.writeHead(404);
    res.end();
  });
  const ctx = {
    profileContext: { dir: directory },
    tools: { register: () => () => {} },
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
  const { env: extraEnv, ...restOptions } = extraOptions;
  const runtime = await createManaged({
    config: {
      browserOrigin: {
        scheme: 'https',
        host: 'instance.i.example.com',
        port: 443,
      },
    },
    env: {
      AURIGA_MODE: 'managed',
      AURIGA_INSTANCE_ID: INSTANCE_ID,
      AURIGA_OWNER_ID: OWNER_ID,
      AURIGA_TENANT_ID: TENANT_ID,
      AURIGA_PLATFORM_URL: 'https://edge.example.com',
      ...extraEnv,
    },
    identityKeys: keys.keySet,
    watchKeys: false,
    startHeartbeat: false,
    ...restOptions,
  });
  const dispose = runtime.apply(ctx);
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  t.after(async () => {
    for (const effect of effects.toReversed()) {
      if (typeof effect === 'function') await effect();
    }
    await dispose();
    await runtime.close();
    server.closeAllConnections();
    await new Promise((resolve) => server.close(resolve));
    await rm(dirname(keys.keyFile), { recursive: true, force: true });
    await rm(directory, { recursive: true, force: true });
  });
  const getHealth = () =>
    new Promise((resolve, reject) => {
      const req = http.request(
        {
          host: '127.0.0.1',
          port: server.address().port,
          path: '/_auriga/health',
          method: 'GET',
          agent: false,
        },
        (res) => {
          let body = '';
          res.on('data', (chunk) => (body += chunk));
          res.on('end', () =>
            resolve({ status: res.statusCode, body: JSON.parse(body) }),
          );
        },
      );
      req.on('error', reject);
      req.end();
    });
  return { runtime, dispose, getHealth, directory };
}

test('managed /_auriga/health is ready when managed network is unconfigured', async (t) => {
  const { runtime, dispose, getHealth } = await startManagedHealthFixture(t);
  await runtime.ready;
  const res = await getHealth();
  assert.equal(res.status, 200);
  assert.equal(res.body.ready, true);
  assert.equal(res.body.status, 'ok');
  await dispose();
  const afterDispose = await getHealth();
  assert.equal(afterDispose.status, 200);
  assert.equal(afterDispose.body.ready, false);
});

test('managed /_auriga/health stays ready=false when deviceCredentialFile is configured without session', async (t) => {
  const missingFile = join(
    tmpdir(),
    'auriga-nonexistent-device-credential.json',
  );
  const { runtime, getHealth } = await startManagedHealthFixture(t, {
    deviceCredentialFile: missingFile,
  });
  await runtime.ready;
  const res = await getHealth();
  assert.equal(res.status, 200);
  assert.equal(res.body.ready, false);
});

test('managed /_auriga/health gates on sessionAvailable when network connection is configured', async (t) => {
  const connection = brokerFixture();
  connection.identity.memberId = OWNER_ID;
  connection.transition('signed_out');
  const { runtime, getHealth } = await startManagedHealthFixture(t, {
    connection,
    env: { AURIGA_TENANT_ID: connection.identity.tenantId },
  });
  await runtime.ready;
  const before = await getHealth();
  assert.equal(before.status, 200);
  assert.equal(before.body.ready, false);

  connection.transition('authenticated');
  let after;
  const deadline = Date.now() + 3000;
  do {
    after = await getHealth();
    if (after.body.ready) break;
    await delay(10);
  } while (Date.now() < deadline);
  assert.equal(after.status, 200);
  assert.equal(after.body.ready, true);
});
