import { test } from 'node:test';
import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import {
  createActivityProbe,
  refreshActivityFromHost,
} from '../src/managed/activity.js';
import { createHeartbeatClient } from '../src/managed/heartbeat.js';
import { createDoorLock, createReplayCache } from '../src/managed/door.js';
import { installHostAuthentication } from '../src/managed/host-auth.js';
import {
  makeIdentityKeys,
  signIdentity,
  INSTANCE_ID,
  OWNER_ID,
} from './managed-helpers.js';

test('unknown host values are sent as explicit null, never idle zeros', async () => {
  const activity = createActivityProbe();
  await refreshActivityFromHost({}, activity, { browsers: 0 });
  assert.equal(activity.snapshot().runningTurns, null);
  assert.equal(activity.snapshot().openTerminals, null);
  assert.equal(activity.snapshot().activeImBots, null);
  let calls = 0;
  let body;
  const client = createHeartbeatClient({
    platformUrl: 'https://example.com',
    pluginVersion: '0.0.0',
    dshVersion: '0.2.0-rc.2',
    activity,
    getInstanceToken: () => 'test-token',
    fetchImpl: async (_url, init) => {
      calls++;
      body = JSON.parse(init.body);
      return new Response('{}');
    },
  });
  const result = await client.send();
  assert.equal(result.ok, true);
  assert.deepEqual(body.activity, {
    runningTurns: null,
    openTerminals: null,
    activeImBots: null,
    connectedClients: 0,
  });
  assert.equal(calls, 1);
});

test('replay cache pressure fails closed without forgetting unexpired identities', () => {
  let clock = 100;
  const cache = createReplayCache({ now: () => clock, maxSize: 1 });
  assert.equal(cache.claim('first', 120), true);
  assert.equal(cache.claim('second', 121), false);
  assert.equal(cache.claim('first', 120), false);
  clock = 126;
  assert.equal(cache.claim('second', 140), true);
});

test('owner-scoped terminal lookup failures and missing agents stay unknown', async () => {
  const activity = createActivityProbe();
  const agent = { status: 'idle' };
  const ctx = {
    get: (name) =>
      name === 'agents'
        ? { list: () => [agent] }
        : name === 'terminals'
          ? {
              list: (owner) => {
                assert.equal(owner, agent);
                throw new Error('backend unavailable');
              },
            }
          : undefined,
  };
  await refreshActivityFromHost(ctx, activity, { browsers: 0 });
  assert.equal(activity.snapshot().openTerminals, null);
  await refreshActivityFromHost(
    { get: (name) => (name === 'terminals' ? { list: () => [] } : undefined) },
    activity,
  );
  assert.equal(activity.snapshot().openTerminals, null);
});

test('exited PTYs are not open terminals; malformed state stays unknown', async () => {
  const activity = createActivityProbe();
  let sessions = [
    { status: { kind: 'running' } },
    { status: { kind: 'exited' } },
  ];
  const ctx = {
    get: (name) =>
      name === 'agents'
        ? { list: () => [{ status: 'idle' }] }
        : name === 'terminals'
          ? { list: async () => sessions }
          : undefined,
  };
  await refreshActivityFromHost(ctx, activity);
  assert.equal(activity.snapshot().openTerminals, 1);
  sessions = [{}];
  await refreshActivityFromHost(ctx, activity);
  assert.equal(activity.snapshot().openTerminals, null);
});

test('invalid counts and agent collection cannot look idle', async () => {
  const activity = createActivityProbe();
  for (const value of [Infinity, -1, '0', NaN, 1.5]) {
    activity.setRunningTurns(value);
    assert.equal(activity.snapshot().runningTurns, null);
  }
  await refreshActivityFromHost(
    {
      get: (name) =>
        name === 'agents' ? { list: () => undefined } : undefined,
    },
    activity,
  );
  assert.equal(activity.snapshot().runningTurns, null);
  await refreshActivityFromHost(
    { get: (name) => (name === 'agents' ? { list: () => [{}] } : undefined) },
    activity,
  );
  assert.equal(activity.snapshot().runningTurns, null);
});

test('counter initialization, positive changes and heartbeat refresh cannot invent user activity', async () => {
  let clock = 1_700_000_000_000;
  const activity = createActivityProbe({ now: () => new Date(clock) });
  activity.update({
    runningTurns: 1,
    openTerminals: 2,
    activeImBots: 1,
    connectedClients: 1,
  });
  assert.equal(activity.snapshot().lastInteractionAt, undefined);
  activity.touchInteraction();
  const stamp = activity.snapshot().lastInteractionAt;
  clock += 60_000;
  activity.update({
    runningTurns: 2,
    openTerminals: 3,
    activeImBots: 2,
    connectedClients: 2,
  });
  const client = createHeartbeatClient({
    platformUrl: 'https://example.com',
    pluginVersion: '0.0.0',
    dshVersion: '0.2.0-rc.2',
    activity,
    getInstanceToken: () => 'unused',
    fetchImpl: async () => ({ ok: true, json: async () => ({}) }),
  });
  await client.send();
  await client.send();
  assert.equal(activity.snapshot().lastInteractionAt, stamp);
});

test('only door-admitted requests bypass the native DSH authentication', async () => {
  const keys = makeIdentityKeys();
  let clock = 1_700_000_000_000;
  const activity = createActivityProbe({ now: () => new Date(clock) });
  const door = createDoorLock({
    instanceId: INSTANCE_ID,
    ownerId: OWNER_ID,
    getPublicKey: () => keys.publicKey,
    onAuthenticated: () => activity.touchInteraction(),
  });
  const connection = {
    requestRejection: () => 401,
    authorizeIndex: () => false,
  };
  const disposeAuth = installHostAuthentication(connection, door);
  const server = new EventEmitter();
  const disposeDoor = door.install(server);
  const admitted = (req) =>
    new Promise((resolve) => {
      server.once('request', () => resolve());
      server.emit('request', req, {});
    });
  const req = {
    method: 'GET',
    url: '/',
    headers: {
      host: '192.0.2.20:7860',
      'auriga-identity': signIdentity(keys.privateKey, keys.kid, {
        htm: 'GET',
        htu: '/',
      }).token,
    },
  };
  assert.equal(
    connection.requestRejection({
      ...req,
      __aurigaIdentity: { sub: OWNER_ID },
    }),
    401,
  );
  await admitted(req);
  assert.equal(connection.requestRejection(req), undefined);
  assert.equal(connection.authorizeIndex(req), true);
  const first = activity.snapshot().lastInteractionAt;
  clock += 30_000;
  const health = {
    method: 'GET',
    url: '/_auriga/health',
    headers: { host: '192.0.2.20:7860' },
  };
  await admitted(health);
  assert.equal(activity.snapshot().lastInteractionAt, first);
  // Admitted (a native fence must not 401 readiness) but not an interaction.
  assert.equal(connection.requestRejection(health), undefined);
  assert.equal((await door.check(health, true)).code, 'common.unauthenticated');
  const second = {
    ...req,
    headers: {
      host: '192.0.2.20:7860',
      'auriga-identity': signIdentity(keys.privateKey, keys.kid, {
        htm: 'GET',
        htu: '/',
      }).token,
    },
  };
  await admitted(second);
  assert.notEqual(activity.snapshot().lastInteractionAt, first);
  disposeDoor();
  disposeAuth();
  assert.equal(connection.requestRejection(req), 401);
  assert.equal(connection.authorizeIndex(req), false);
});
