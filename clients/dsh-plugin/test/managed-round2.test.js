import { test } from 'node:test';
import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import { createDoorLock } from '../src/managed/door.js';
import { rawRequestPath, normalizeHttpPath } from '../src/managed/raw-path.js';
import {
  isNativePluginManageRequest,
  NATIVE_PLUGIN_MUTATIONS,
} from '../src/managed/guards.js';
import { buildHealthBody } from '../src/managed/health.js';
import { createHeartbeatClient } from '../src/managed/heartbeat.js';
import {
  createActivityProbe,
  refreshActivityFromHost,
} from '../src/managed/activity.js';
import { createManaged } from '../src/modes/managed.js';
import {
  makeIdentityKeys,
  signPanelManifest,
  panelManifestBase,
  INSTANCE_ID,
  OWNER_ID,
  TENANT_ID,
} from './managed-helpers.js';

test('rawRequestPath does not collapse .. or decode', () => {
  assert.equal(rawRequestPath('/a/../b?x=1'), '/a/../b');
  assert.equal(rawRequestPath('/x/%2e%2e/y'), '/x/%2e%2e/y');
  assert.equal(rawRequestPath('/_auriga/health'), '/_auriga/health');
});

test('door health bypass only for exact GET/HEAD raw path', async () => {
  const door = createDoorLock({
    instanceId: INSTANCE_ID,
    ownerId: OWNER_ID,
    getPublicKey: async () => null,
  });
  const ok = await door.check({ method: 'GET', url: '/_auriga/health' });
  assert.equal(ok.ok, true);
  assert.equal(ok.bypass, true);

  const sneaky = await door.check({
    method: 'GET',
    url: '/x/../_auriga/health',
  });
  assert.equal(sneaky.ok, false);
  assert.equal(sneaky.code, 'common.unauthenticated');

  const post = await door.check({ method: 'POST', url: '/_auriga/health' });
  assert.equal(post.ok, false);
});

test('apply throws when webServer.server is missing', async () => {
  const keys = makeIdentityKeys();
  const runtime = await createManaged({
    env: {
      AURIGA_MODE: 'managed',
      AURIGA_INSTANCE_ID: INSTANCE_ID,
      AURIGA_OWNER_ID: OWNER_ID,
      AURIGA_TENANT_ID: TENANT_ID,
      AURIGA_PLATFORM_URL: 'https://edge.example.com',
    },
    identityKeys: keys.keySet,
    panelKeys: keys.keySet,
    instanceToken: 'ait_test',
    watchKeys: false,
    startHeartbeat: false,
  });
  assert.throws(
    () => runtime.apply({ webServer: { register() {} } }),
    /requires ctx.webServer.server/,
  );
  runtime.close();
});

test('apply installs door when server present', async () => {
  const keys = makeIdentityKeys();
  const runtime = await createManaged({
    env: {
      AURIGA_MODE: 'managed',
      AURIGA_INSTANCE_ID: INSTANCE_ID,
      AURIGA_OWNER_ID: OWNER_ID,
      AURIGA_TENANT_ID: TENANT_ID,
      AURIGA_PLATFORM_URL: 'https://edge.example.com',
    },
    identityKeys: keys.keySet,
    panelKeys: keys.keySet,
    instanceToken: 'ait_test',
    watchKeys: false,
    startHeartbeat: false,
  });
  const server = new EventEmitter();
  const dispose = runtime.apply({
    webServer: { server, register() {} },
    connection: { requestRejection() {} },
    provide() {},
    inject() {},
    effect(fn) {
      return fn();
    },
  });
  assert.equal(typeof server.emit, 'function');
  dispose?.();
  runtime.close();
});

test('health body has only contract fields', () => {
  const body = buildHealthBody({ pluginVersion: '0.0.0', ready: true });
  assert.deepEqual(Object.keys(body).sort(), [
    'pluginVersion',
    'protocol',
    'ready',
    'status',
  ]);
  assert.equal('activity' in body, false);
});

test('heartbeat send catches fetch throw', async () => {
  const activity = createActivityProbe();
  activity.update({
    runningTurns: 1,
    openTerminals: 0,
    activeImBots: 0,
    connectedClients: 0,
  });
  const client = createHeartbeatClient({
    platformUrl: 'https://edge.example.com',
    getInstanceToken: () => 'ait',
    pluginVersion: '0.0.0',
    dshVersion: '0.2.0',
    activity,
    fetchImpl: async () => {
      throw new Error('network down');
    },
  });
  const result = await client.send();
  assert.equal(result.ok, false);
  assert.equal(result.error, true);
});

test('activity unknown when host services missing', async () => {
  const activity = createActivityProbe();
  await refreshActivityFromHost({}, activity);
  const snap = activity.snapshot();
  assert.equal(snap.allKnown, false);
  assert.ok(snap.unknownFields.includes('runningTurns'));
});

test('activity wires agents and terminals', async () => {
  const activity = createActivityProbe();
  const agent = { status: 'running' };
  const ctx = {
    get(name) {
      if (name === 'agents') return { list: () => [{ agent }] };
      if (name === 'terminals') return { list: () => [{ id: 1 }, { id: 2 }] };
      if (name === 'dshIm')
        return {
          listDeliveryCatalog: async () => ({
            bots: [{ id: 'b1', enabled: true }],
          }),
        };
      throw new Error('missing ' + name);
    },
  };
  await refreshActivityFromHost(ctx, activity, { browsers: 3 });
  const snap = activity.snapshot();
  assert.equal(snap.runningTurns, 1);
  assert.equal(snap.openTerminals, 2);
  assert.equal(snap.activeImBots, 1);
  assert.equal(snap.connectedClients, 3);
  assert.equal(snap.allKnown, true);
});

test('plugin manage HTTP guard catches path variants and PUT', () => {
  assert.equal(
    isNativePluginManageRequest('POST', '/api/pluginManager/installBundle/'),
    true,
  );
  assert.equal(
    isNativePluginManageRequest('POST', '//api/pluginManager/installBundle'),
    true,
  );
  assert.equal(
    isNativePluginManageRequest('POST', '/api/./pluginManager/installBundle'),
    true,
  );
  assert.equal(
    isNativePluginManageRequest('PUT', '/api/pluginManager/removeBundle'),
    true,
  );
  assert.equal(
    isNativePluginManageRequest('POST', '/api/pluginManager/installBundle;a=1'),
    true,
  );
  assert.equal(
    isNativePluginManageRequest('POST', '/api/pluginManager/installBundle%00'),
    true,
  );
  assert.equal(
    isNativePluginManageRequest('GET', '/api/pluginManager/installBundle'),
    false,
  );
  assert.equal(
    isNativePluginManageRequest('POST', '/api/pluginManager/listPlugins'),
    false,
  );
  assert.equal(
    normalizeHttpPath('/api/./pluginManager/InstallBundle/'),
    '/api/pluginmanager/installbundle',
  );
  assert.equal(
    normalizeHttpPath('/api/pluginManager/installBundle;a=1'),
    '/api/pluginmanager/installbundle',
  );
  assert.equal(NATIVE_PLUGIN_MUTATIONS.length, 6);
});

test('createManaged rejects missing identity keys file', async () => {
  await assert.rejects(
    () =>
      createManaged({
        env: {
          AURIGA_MODE: 'managed',
          AURIGA_INSTANCE_ID: INSTANCE_ID,
          AURIGA_OWNER_ID: OWNER_ID,
          AURIGA_PLATFORM_URL: 'https://edge.example.com',
        },
        watchKeys: false,
        startHeartbeat: false,
      }),
    /AURIGA_IDENTITY_KEYS_FILE/,
  );
});

test('panel entry with .js still accepted', () => {
  const keys = makeIdentityKeys('panel');
  const manifest = signPanelManifest(
    keys.privateKey,
    keys.kid,
    panelManifestBase('team.js'),
  );
  assert.equal(manifest.entry, 'team.js');
});
