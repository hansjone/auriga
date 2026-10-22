import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { once } from 'node:events';
import { createActivityProbe } from '../src/managed/activity.js';
import { createHeartbeatClient } from '../src/managed/heartbeat.js';
import { createManaged } from '../src/modes/managed.js';
import { makeIdentityKeys, INSTANCE_ID, OWNER_ID } from './managed-helpers.js';
import { browserController } from './terminal-fixtures.js';

test('real fetch sends unknown counters, checked health and interaction time; recovery is reported', async (t) => {
  const bodies = [];
  const server = createServer(async (req, res) => {
    assert.equal(req.url, '/api/workspace/v1/instance/heartbeat');
    assert.equal(req.method, 'POST');
    const chunks = [];
    for await (const chunk of req) chunks.push(chunk);
    bodies.push(JSON.parse(Buffer.concat(chunks).toString()));
    res.setHeader('content-type', 'application/json');
    res.end('{"nextHeartbeatSeconds":30}');
  });
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  t.after(() => new Promise((resolve) => server.close(resolve)));
  const when = '2026-10-22T00:00:00.000Z';
  const activity = createActivityProbe({ now: () => new Date(when) });
  activity.update({ runningTurns: 0, openTerminals: 0, connectedClients: 0 });
  activity.touchInteraction();
  const changes = [];
  let healthy = false;
  const client = createHeartbeatClient({
    platformUrl: `http://127.0.0.1:${server.address().port}`,
    getInstanceToken: () => 'hb1-generated-test-token',
    pluginVersion: '0.0.0',
    dshVersion: 'test',
    activity,
    healthOk: () => healthy,
    onUnknownActivity: (fields) => changes.push(fields),
  });
  t.after(() => client.stop());
  assert.equal((await client.send()).ok, true);
  assert.equal((await client.send()).ok, true);
  assert.equal(bodies.length, 2);
  assert.deepEqual(bodies[0].activity, {
    runningTurns: 0,
    openTerminals: 0,
    activeImBots: null,
    connectedClients: 0,
    lastInteractionAt: when,
  });
  assert.deepEqual(bodies[0].health, { ok: false });
  assert.deepEqual(changes, [['activeImBots']]);
  activity.setActiveImBots(0);
  healthy = true;
  assert.equal((await client.send()).ok, true);
  assert.deepEqual(bodies[2].health, { ok: true });
  assert.equal(bodies[2].activity.activeImBots, 0);
  assert.equal(bodies[2].activity.lastInteractionAt, when);
  assert.deepEqual(changes, [['activeImBots'], []]);
});

test('a failed health check sends unhealthy instead of suppressing the heartbeat', async () => {
  const bodies = [];
  const client = createHeartbeatClient({
    platformUrl: 'https://example.com',
    getInstanceToken: () => 'hb1-test-token',
    pluginVersion: '0.0.0',
    dshVersion: 'test',
    activity: createActivityProbe(),
    healthOk() {
      throw new Error('health source unavailable');
    },
    fetchImpl: async (_url, init) => {
      bodies.push(JSON.parse(init.body));
      return new Response('{}');
    },
  });
  assert.equal((await client.send()).ok, true);
  assert.equal(bodies[0].health.ok, false);
  assert.deepEqual(Object.values(bodies[0].activity), [null, null, null, null]);
});

test('managed heartbeat health follows actual host listening, loss and recovery', async (t) => {
  const hostServer = createServer();
  const bodies = [];
  const keys = makeIdentityKeys();
  const runtime = await createManaged({
    env: {
      AURIGA_MODE: 'managed',
      AURIGA_INSTANCE_ID: INSTANCE_ID,
      AURIGA_OWNER_ID: OWNER_ID,
      AURIGA_PLATFORM_URL: 'https://example.com',
    },
    identityKeys: keys.keySet,
    instanceToken: 'hb1-host-test-token',
    watchKeys: false,
    startHeartbeat: false,
    config: {
      browserOrigin: {
        scheme: 'https',
        host: 'instance.i.example.com',
        port: 443,
      },
      imBots: 'none',
      terminalSources: 'browser-only',
    },
    fetchImpl: async (_url, init) => {
      bodies.push(JSON.parse(init.body));
      return new Response('{}');
    },
  });
  t.after(() => runtime.close());
  await runtime.heartbeat.send();
  assert.equal(bodies.at(-1).health.ok, false);
  const dispose = runtime.apply({
    webServer: { server: hostServer, register() {} },
    get(name) {
      if (name === 'agents') return { list: () => [] };
      if (name === 'terminalController') return browserController();
      return undefined;
    },
  });
  t.after(dispose);
  await runtime.heartbeat.send();
  assert.equal(bodies.at(-1).health.ok, false);
  hostServer.listen(0, '127.0.0.1');
  await once(hostServer, 'listening');
  t.after(() => new Promise((resolve) => hostServer.close(resolve)));
  await runtime.heartbeat.send();
  assert.equal(bodies.at(-1).health.ok, true);
  await new Promise((resolve) => hostServer.close(resolve));
  await runtime.heartbeat.send();
  assert.equal(bodies.at(-1).health.ok, false);
  hostServer.listen(0, '127.0.0.1');
  await once(hostServer, 'listening');
  await runtime.heartbeat.send();
  assert.equal(bodies.at(-1).health.ok, true);
});

test('a failed refresh replaces old idle zeros with null and still sends', async () => {
  const activity = createActivityProbe();
  activity.update({
    runningTurns: 0,
    openTerminals: 0,
    activeImBots: 0,
    connectedClients: 0,
  });
  let seen;
  const client = createHeartbeatClient({
    platformUrl: 'https://example.com',
    getInstanceToken: () => 'hb1-refresh-test-token',
    pluginVersion: '0.0.0',
    dshVersion: 'test',
    activity,
    refreshActivity() {
      throw new Error('host refresh failed');
    },
    fetchImpl: async (_url, init) => {
      seen = JSON.parse(init.body);
      return new Response('{}');
    },
  });
  assert.equal((await client.send()).ok, true);
  assert.deepEqual(Object.values(seen.activity), [null, null, null, null]);
  assert.equal(seen.health.ok, false);
});
