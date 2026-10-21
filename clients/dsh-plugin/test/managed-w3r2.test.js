import { test } from 'node:test';
import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import {
  createActivityProbe,
  refreshActivityFromHost,
} from '../src/managed/activity.js';
import { createHeartbeatClient } from '../src/managed/heartbeat.js';
import { createDoorLock } from '../src/managed/door.js';
import {
  browserFenceOk,
  installHostAuthentication,
} from '../src/managed/host-auth.js';
import { createManaged } from '../src/modes/managed.js';
import {
  makeIdentityKeys,
  signIdentity,
  INSTANCE_ID,
  OWNER_ID,
} from './managed-helpers.js';

const EDGE = `${INSTANCE_ID}.i.example.com`;

function pureDshCtx() {
  return {
    get(name) {
      if (name === 'agents') return { list: () => [] };
      if (name === 'terminals') return { list: () => [] };
      return undefined;
    },
  };
}

test('pure DSH: declared imBots none is a known 0; undeclared stays unknown', async () => {
  const declared = createActivityProbe();
  await refreshActivityFromHost(pureDshCtx(), declared, {
    browsers: 0,
    imBots: 'none',
  });
  const snap = declared.snapshot();
  assert.equal(snap.activeImBots, 0);
  assert.equal(snap.allKnown, true);

  const undeclared = createActivityProbe();
  await refreshActivityFromHost(pureDshCtx(), undeclared, { browsers: 0 });
  assert.equal(undeclared.snapshot().activeImBots, null);
  assert.deepEqual(undeclared.snapshot().unknownFields, ['activeImBots']);
});

test('declared imBots none never masks a present but failing IM service', async () => {
  const activity = createActivityProbe();
  const ctx = {
    get(name) {
      if (name === 'dshIm')
        return {
          listDeliveryCatalog() {
            throw new Error('down');
          },
        };
      return pureDshCtx().get(name);
    },
  };
  await refreshActivityFromHost(ctx, activity, { browsers: 0, imBots: 'none' });
  assert.equal(activity.snapshot().activeImBots, null);
});

test('pure DSH with declaration sends a heartbeat with known zeros', async () => {
  const activity = createActivityProbe();
  const bodies = [];
  const hb = createHeartbeatClient({
    platformUrl: 'http://192.0.2.10:8080',
    getInstanceToken: () => 'tok',
    pluginVersion: '0.0.0',
    dshVersion: '0.2.0-rc.2',
    activity,
    refreshActivity: () =>
      refreshActivityFromHost(pureDshCtx(), activity, {
        browsers: 0,
        imBots: 'none',
      }),
    fetchImpl: async (_url, init) => {
      bodies.push(JSON.parse(init.body));
      return new Response(JSON.stringify({ nextHeartbeatSeconds: 30 }), {
        status: 200,
        headers: { 'content-type': 'application/json' },
      });
    },
  });
  const result = await hb.send();
  assert.equal(result.ok, true);
  assert.equal(bodies.length, 1);
  assert.deepEqual(
    {
      runningTurns: bodies[0].activity.runningTurns,
      openTerminals: bodies[0].activity.openTerminals,
      activeImBots: bodies[0].activity.activeImBots,
      connectedClients: bodies[0].activity.connectedClients,
    },
    { runningTurns: 0, openTerminals: 0, activeImBots: 0, connectedClients: 0 },
  );
  assert.deepEqual(bodies[0].health, { ok: true });
  assert.equal(bodies[0].activity.lastInteractionAt, undefined);
});

test('skipped heartbeat reports field names once per unknown set', async () => {
  const activity = createActivityProbe();
  activity.update({ runningTurns: 0, openTerminals: 0, connectedClients: 0 });
  const skips = [];
  let fetched = 0;
  const hb = createHeartbeatClient({
    platformUrl: 'http://192.0.2.10:8080',
    getInstanceToken: () => 'tok',
    pluginVersion: '0.0.0',
    dshVersion: 'x',
    activity,
    onSkip: (fields) => skips.push(fields),
    fetchImpl: async () => {
      fetched += 1;
      return new Response('{}', { status: 200 });
    },
  });
  await hb.send();
  await hb.send();
  assert.deepEqual(skips, [['activeImBots']]);
  activity.markUnknown('openTerminals');
  await hb.send();
  assert.deepEqual(skips[1], ['openTerminals', 'activeImBots']);
  assert.equal(fetched, 0);
  assert.ok(!JSON.stringify(skips).includes('tok'));
});

test('browser fence: edge Origin with rewritten upstream Host passes, others fail', () => {
  const trusted = [EDGE];
  const req = (headers) => ({
    headers: { host: '172.18.0.2:7860', ...headers },
  });
  assert.equal(browserFenceOk(req({}), trusted), true);
  assert.equal(
    browserFenceOk(req({ origin: `https://${EDGE}` }), trusted),
    true,
  );
  assert.equal(
    browserFenceOk(req({ origin: `http://${EDGE}:8080` }), trusted),
    true,
  );
  assert.equal(
    browserFenceOk(req({ origin: 'https://evil.example.net' }), trusted),
    false,
  );
  assert.equal(browserFenceOk(req({ origin: `https://${EDGE}` }), []), false);
  assert.equal(
    browserFenceOk(req({ 'sec-fetch-site': 'cross-site' }), trusted),
    false,
  );
  assert.equal(browserFenceOk(req({ origin: 'null' }), trusted), false);
  // Same-origin direct access (Origin equals Host) stays allowed.
  assert.equal(
    browserFenceOk(req({ origin: 'http://172.18.0.2:7860' }), trusted),
    true,
  );
  // DNS rebinding: an undeclared name as Host is refused.
  assert.equal(
    browserFenceOk({ headers: { host: 'rebind.example.net:7860' } }, trusted),
    false,
  );
  assert.equal(
    browserFenceOk({ headers: { host: `${EDGE}:8080` } }, trusted),
    true,
  );
  assert.equal(browserFenceOk({ headers: {} }, trusted), false);
});

async function admittedRequest(door, server, headers) {
  const keys = door.__keys;
  const req = {
    method: 'GET',
    url: '/api/remote.mux',
    headers: {
      host: '172.18.0.2:7860',
      ...headers,
      'auriga-identity': signIdentity(keys.privateKey, keys.kid, {
        htm: 'GET',
        htu: '/api/remote.mux',
      }).token,
    },
  };
  await new Promise((resolve) => {
    server.once('request', () => resolve());
    server.emit('request', req, {});
  });
  return req;
}

test('door-admitted requests keep the Origin fence on native RPC and index', async () => {
  const keys = makeIdentityKeys();
  const door = createDoorLock({
    instanceId: INSTANCE_ID,
    ownerId: OWNER_ID,
    getPublicKey: () => keys.publicKey,
  });
  door.__keys = keys;
  const nativeCalls = [];
  const connection = {
    requestRejection: (req) => {
      nativeCalls.push(req);
      return 401;
    },
    authorizeIndex: () => false,
  };
  const dispose = installHostAuthentication(connection, door, {
    trustedHosts: [EDGE],
  });
  const server = new EventEmitter();
  const disposeDoor = door.install(server);

  const good = await admittedRequest(door, server, {
    origin: `https://${EDGE}`,
  });
  assert.equal(connection.requestRejection(good), undefined);
  assert.equal(connection.authorizeIndex(good, {}), true);

  const evil = await admittedRequest(door, server, {
    origin: 'https://evil.example.net',
  });
  assert.equal(connection.requestRejection(evil), 403);
  const res = {
    status: 0,
    writeHead(code) {
      this.status = code;
    },
    end() {},
  };
  assert.equal(connection.authorizeIndex(evil, res), false);
  assert.equal(res.status, 403);

  // Not admitted by the door: native check decides, unchanged.
  assert.equal(nativeCalls.length, 0);
  assert.equal(
    connection.requestRejection({ headers: { origin: `https://${EDGE}` } }),
    401,
  );
  assert.equal(nativeCalls.length, 1);
  disposeDoor();
  dispose();
});

test('door refuses checkContinue / checkExpectation / connect host listeners', () => {
  const keys = makeIdentityKeys();
  const door = createDoorLock({
    instanceId: INSTANCE_ID,
    ownerId: OWNER_ID,
    getPublicKey: () => keys.publicKey,
  });
  const server = new EventEmitter();
  let reached = 0;
  for (const event of ['checkContinue', 'checkExpectation', 'connect'])
    server.on(event, () => {
      reached += 1;
    });
  door.install(server);
  for (const event of ['checkContinue', 'checkExpectation']) {
    const res = {
      code: 0,
      writeHead(code) {
        this.code = code;
      },
      end() {},
    };
    server.emit(event, { method: 'POST', url: '/', headers: {} }, res);
    assert.equal(res.code, 417);
  }
  let destroyed = false;
  server.emit(
    'connect',
    { method: 'CONNECT', url: 'example.com:443', headers: {} },
    {
      destroy() {
        destroyed = true;
      },
    },
  );
  assert.equal(destroyed, true);
  assert.equal(reached, 0);
});

test('managed rejects an unknown imBots declaration', async () => {
  await assert.rejects(
    createManaged({
      env: {
        AURIGA_MODE: 'managed',
        AURIGA_INSTANCE_ID: INSTANCE_ID,
        AURIGA_OWNER_ID: OWNER_ID,
        AURIGA_PLATFORM_URL: 'http://192.0.2.10:8080',
      },
      identityKeys: { keys: [] },
      config: { imBots: 'some' },
      startHeartbeat: false,
      watchKeys: false,
    }),
    /imBots/,
  );
});
