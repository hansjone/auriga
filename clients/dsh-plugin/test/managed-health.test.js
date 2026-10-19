import { test } from 'node:test';
import assert from 'node:assert/strict';
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

test('health body reports protocol, version, and activity counts', () => {
  const activity = createActivityProbe();
  activity.update({
    runningTurns: 2,
    openTerminals: 1,
    activeImBots: 3,
    connectedClients: 4,
  });
  const body = buildHealthBody({
    pluginVersion: '0.0.0',
    ready: true,
    activity,
  });
  assert.equal(body.status, 'ok');
  assert.equal(body.ready, true);
  assert.equal(body.protocol, PROTOCOL_VERSION);
  assert.equal(body.pluginVersion, '0.0.0');
  assert.deepEqual(body.activity, {
    runningTurns: 2,
    openTerminals: 1,
    activeImBots: 3,
    connectedClients: 4,
  });
});

test('activity probe notifies on change', () => {
  const activity = createActivityProbe();
  let n = 0;
  activity.onChange(() => {
    n += 1;
  });
  activity.setRunningTurns(1);
  activity.setOpenTerminals(2);
  assert.equal(n, 2);
  assert.equal(activity.snapshot().runningTurns, 1);
  assert.equal(activity.snapshot().openTerminals, 2);
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
