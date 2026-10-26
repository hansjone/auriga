/* global Headers */
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { sign, generateKeyPairSync } from 'node:crypto';
import tls from 'node:tls';
import { createServer } from 'node:http';
import * as Plugin from '../../src/plugin.js';
import { createVault } from '../../src/local/vault.js';
import { setImmediate } from 'node:timers/promises';
import {
  createMemoryLedgerStore,
  createFileLedgerStore,
} from '../../src/network/host/store.js';
import { openLedger, reserveLocal } from '../../src/network/host/ledger.js';
import { mkdtemp, mkdir, readFile } from 'node:fs/promises';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { IDENTITY } from './host/fixtures.js';
import { decodeJson, encodeJson } from '../../src/network/session/frames.js';
import { signingInput } from '../../src/network/trust/verifier.js';
import {
  signedKeyset,
  signedAnnouncement,
  testRoots,
  publicationFixture,
  signedReadyResult,
} from './trust/keyset-fixtures.js';
import { NatsTransport } from '../../src/network/transport/nats-transport.js';
import { createDeviceProvider } from '../../src/network/device-provider.js';
import {
  startNatsServer,
  findNatsServer,
} from './transport/support/nats-server.js';
import {
  createCaSocketFactory,
  connectPeer,
  waitFor,
} from './transport/support/client.js';

async function fixture(t, extra = {}) {
  const { openConnectionRuntime } =
    await import('../../src/network/connection-runtime.js');
  const { pair, payload, now } = publicationFixture();
  const registration = {
    tenant: 'tenant-fixture',
    realm: '2',
    node: '64',
    device: 'device-fixture',
    systemAddress: 'aur1:2.40.0',
    created: true,
    ...extra.registration,
  };
  const events = [],
    connections = [],
    sent = [];
  let runtime,
    tickets = 0;
  const ledger =
    extra.ledger ??
    createMemoryLedgerStore({
      beforeCommit: async (state) => {
        events.push('commit');
        await extra.commit?.(state);
      },
    });
  const trustStore = extra.trust ?? createMemoryLedgerStore();
  const controller = new AbortController();
  const prepared = {
    kind: 'device-connection',
    roots: testRoots,
    url: 'wss://network.example.com/',
    async register(signal) {
      signal.throwIfAborted();
      events.push('register');
      await extra.register?.();
      return registration;
    },
    async refresh(trust, signal) {
      await extra.refresh?.(trust, signal);
      signal.throwIfAborted();
      await trust.announce(signedAnnouncement(payload));
      signal.throwIfAborted();
      await trust.update(signedKeyset(payload));
    },
    async getTicket(signal) {
      signal.throwIfAborted();
      events.push('ticket-' + ++tickets);
      await extra.ticket?.(tickets);
      return {
        auth: { token: 'ticket-' + tickets },
        expiresAtMs: Date.now() + 3600000,
      };
    },
  };
  function response(request, connection, changes = {}) {
    const frame = {
      app_version: '1.0',
      type: 'ready_result',
      request_id: request.request_id,
      status: 'ready',
      epoch: String(connection.id),
      node_identity: { realm: '2', node: '64', incarnation: 'inc-1' },
      head_seq: '0',
      committed_cursor: '0',
      retention_low_seq: '0',
      local_high_water: '0',
      registration_revision: '0',
      seen_checkpoint: null,
      required_features: [],
      ...changes,
    };
    frame.server_auth = {
      version: 1,
      purpose: 'protocol_response',
      alg: 'Ed25519',
      kid: 'response',
      issuer_realm: '2',
      issued_at: payload.issued_at,
      not_after: new Date(now() + 120000).toISOString().replace('.000Z', 'Z'),
      audience: {
        realm: '2',
        node: '64',
        incarnation: 'inc-1',
        epoch: frame.epoch,
      },
      request_id: request.request_id,
      channel_nonce: request.channel_nonce,
      keyset_version: '5',
    };
    extra.auth?.(frame.server_auth, connection.id);
    frame.server_auth.signature = sign(
      null,
      signingInput(frame),
      pair.privateKey,
    ).toString('base64url');
    return encodeJson(frame);
  }
  runtime = await openConnectionRuntime({
    kind: 'device-connection',
    prepared,
    getAccess: async () => 'unused',
    authorizeLocal: async () => false,
    signal: controller.signal,
    now,
    stores: { ledger, trust: trustStore },
    ctx: new Proxy(
      {},
      {
        get() {
          assert.fail('basic connection touched Host/tool context');
        },
      },
    ),
    transportFactory(options) {
      const connection = {
        id: connections.length + 1,
        epoch: 1,
        state: 'idle',
        async start() {
          options.onState({ state: 'connecting', epoch: 0 });
          await options.getTicket({ reason: 'start', attempt: 0 });
          connection.state = 'ready';
          options.onState({ state: 'ready', epoch: 1 });
        },
        async stop() {
          events.push('stop-' + connection.id);
          await extra.stop?.(connection.id);
          connection.state = 'stopped';
          options.onState({ state: 'stopped', epoch: 1 });
        },
        async send(bytes) {
          const request = decodeJson(bytes);
          assert.equal(request.type, 'ready');
          sent.push({ request, connection });
          events.push('ready-' + connection.id);
          await extra.ready?.(connection.id, request);
          options.onMessage(
            response(request, connection, extra.result?.(connection.id)),
            connection.epoch,
          );
        },
        message(bytes) {
          options.onMessage(bytes, connection.epoch);
        },
        transition(state, epoch) {
          connection.state = state;
          connection.epoch = epoch;
          options.onState({ state, epoch });
        },
      };
      connections.push(connection);
      return connection;
    },
  });
  t.after(() => runtime.close());
  return {
    runtime,
    ledger,
    controller,
    events,
    connections,
    sent,
    response,
    prepared,
  };
}

test('first connection commits complete ledger under one lock, really stops C1, then new ticket C2 gates connected', async (t) => {
  let release;
  const stopGate = new Promise((resolve) => {
    release = resolve;
  });
  const f = await fixture(t, {
    stop: async (id) => {
      if (id === 1) await stopGate;
    },
  });
  const starting = f.runtime.start();
  while (!f.events.includes('stop-1')) await setImmediate();
  assert.equal(f.runtime.status.connected, false);
  assert.equal(f.ledger.snapshot().identity.nodeIncarnation, 'inc-1');
  await assert.rejects(f.ledger.open(), /locked/);
  assert.equal(f.connections.length, 1);
  release();
  await starting;
  assert.equal(f.runtime.status.connected, true);
  assert.deepEqual(f.events, [
    'register',
    'ticket-1',
    'ready-1',
    'commit',
    'stop-1',
    'ticket-2',
    'ready-2',
  ]);
  assert.notEqual(
    f.sent[0].request.channel_nonce,
    f.sent[1].request.channel_nonce,
  );
  assert.notEqual(f.sent[0].request.boot_id, f.sent[1].request.boot_id);
  assert.throws(() => f.runtime.consentUI({}), /unavailable/);
});

for (const [name, mutate] of [
  ['tenant', (s) => (s.identity.tenant = 'wrong')],
  ['node', (s) => (s.identity.node = '65')],
  ['realm', (s) => (s.identity.realm = '3')],
  ['incarnation', (s) => (s.identity.nodeIncarnation = 'wrong-inc')],
  ['bad map', (s) => (s.recipients = [])],
  ['bad revision', (s) => (s.revision = -1)],
  [
    'identity conflict',
    (s) => (s.identityConflict = 'network.recovery_conflict'),
  ],
  ['missing evidence', (s) => delete s.localQueue],
])
  test(
    'existing ledger rejects ' + name + ' without overwrite or bootstrap',
    async (t) => {
      const first = await fixture(t);
      await first.runtime.start();
      await first.runtime.close();
      const state = first.ledger.snapshot();
      mutate(state);
      first.ledger.restore(state);
      const f = await fixture(t, {
        ledger: first.ledger,
        registration: { created: false },
      });
      await assert.rejects(f.runtime.start());
      assert.equal(f.runtime.status.connected, false);
      assert.deepEqual(first.ledger.snapshot(), state);
      assert.equal(f.events.includes('commit'), false);
    },
  );

for (const status of ['reconciling', 'recovery_conflict', 'identity_rollback'])
  test('signed ' + status + ' never opens connected', async (t) => {
    const f = await fixture(t, { result: () => ({ status }) });
    await assert.rejects(f.runtime.start());
    assert.equal(f.runtime.status.connected, false);
    assert.equal(f.ledger.snapshot(), undefined);
  });
for (const changes of [
  { local_high_water: '1' },
  { registration_revision: '1' },
  { committed_cursor: '1' },
  {
    seen_checkpoint: {
      checkpoint_seq: '0',
      checkpoint_revision: '1',
      summary_digest: 'sha256:' + '0'.repeat(64),
    },
  },
])
  test(
    'normal READY rejects signed inconsistent recovery ' +
      JSON.stringify(changes),
    async (t) => {
      const f = await fixture(t, { result: (id) => (id === 2 ? changes : {}) });
      await assert.rejects(f.runtime.start());
      assert.equal(f.runtime.status.connected, false);
      assert.equal(f.ledger.snapshot().revision, 0);
    },
  );

test('real pending operation and revision are transmitted intact and are not cleared for recovery', async (t) => {
  const store = createMemoryLedgerStore();
  const ledger = await openLedger({
    store,
    identity: IDENTITY,
    initialize: true,
  });
  await reserveLocal(ledger, {
    opId: 'op-pending',
    purpose: 'bind',
    mappingDigest: 'mapping',
    addressType: 'session',
  });
  await ledger.close();
  const before = store.snapshot();
  const f = await fixture(t, {
    ledger: store,
    registration: { created: false },
    result: () => ({ status: 'reconciling' }),
  });
  await assert.rejects(f.runtime.start());
  assert.equal(
    f.sent[0].request.receipt_checkpoint.checkpoint_revision,
    String(before.revision),
  );
  assert.equal(f.sent[0].request.local_high_water, before.localHighWater);
  assert.equal(
    f.sent[0].request.pending_registration.allocation_op_id,
    'op-pending',
  );
  assert.deepEqual(store.snapshot(), before);
});

function latch() {
  let release;
  const promise = new Promise((r) => {
    release = r;
  });
  return { promise, release };
}
test('logout during atomic commit disables state first, stops socket and retains writer until commit settles', async (t) => {
  const gate = latch();
  let entered = false;
  const f = await fixture(t, {
    commit: async () => {
      entered = true;
      await gate.promise;
    },
  });
  const start = f.runtime.start();
  const rejected = assert.rejects(start);
  while (!entered) await setImmediate();
  f.controller.abort();
  const closing = f.runtime.close();
  assert.equal(f.runtime.status.connected, false);
  assert.equal(f.runtime.status.available, false);
  await assert.rejects(f.ledger.open(), /locked/);
  gate.release();
  await rejected;
  await closing;
  const h = await f.ledger.open();
  await h.close();
  assert.equal(f.connections.length, 1);
  assert.equal(f.ledger.snapshot().identity.nodeIncarnation, 'inc-1');
});
for (const point of ['refresh', 'c1-ready', 'c1-stop', 'ticket2', 'c2-ready'])
  test(
    'logout at ' + point + ' cannot revive from late completion',
    async (t) => {
      const gate = latch();
      let entered = false;
      const wait = async () => {
        entered = true;
        await gate.promise;
      };
      const extra =
        point === 'refresh'
          ? { refresh: wait }
          : point === 'c1-ready'
            ? { ready: (id) => (id === 1 ? wait() : undefined) }
            : point === 'c1-stop'
              ? { stop: (id) => (id === 1 ? wait() : undefined) }
              : point === 'ticket2'
                ? { ticket: (n) => (n === 2 ? wait() : undefined) }
                : { ready: (id) => (id === 2 ? wait() : undefined) };
      const f = await fixture(t, extra);
      const start = f.runtime.start();
      const rejected = assert.rejects(start);
      while (!entered) await setImmediate();
      f.controller.abort();
      assert.equal(f.runtime.status.connected, false);
      gate.release();
      await rejected;
      await f.runtime.close();
      assert.equal(f.runtime.status.connected, false);
    },
  );

test('late C1 signed frame cannot settle C2, and transport reconnect never bootstraps again', async (t) => {
  const f = await fixture(t);
  await f.runtime.start();
  f.connections[0].message(f.response(f.sent[0].request, f.connections[0]));
  assert.equal(f.runtime.status.connected, true);
  f.connections[1].transition('reconnecting', 1);
  assert.equal(f.runtime.status.connected, false);
  f.connections[1].transition('ready', 2);
  while (!f.runtime.status.connected) await setImmediate();
  assert.equal(f.sent.length, 3);
  assert.equal(f.events.filter((x) => x === 'commit').length, 1);
});
test('C1 response delivered to live C2 cannot complete its new nonce/request handshake', async (t) => {
  const gate = latch();
  let entered = false;
  const f = await fixture(t, {
    ready: async (id) => {
      if (id === 2) {
        entered = true;
        await gate.promise;
      }
    },
  });
  const starting = f.runtime.start();
  while (!entered) await setImmediate();
  f.connections[1].message(f.response(f.sent[0].request, f.connections[0]));
  await setImmediate();
  assert.equal(f.runtime.status.connected, false);
  gate.release();
  await starting;
  assert.equal(f.runtime.status.connected, true);
});
test('uncertain register response never reuses created and missing ledger is denied on retry', async (t) => {
  const first = await fixture(t, {
    register: () => {
      throw new Error('register response lost');
    },
  });
  await assert.rejects(first.runtime.start(), /response lost/);
  await first.runtime.close();
  const next = await fixture(t, {
    ledger: first.ledger,
    registration: { created: false },
  });
  await assert.rejects(next.runtime.start(), /ledger/);
  assert.equal(next.connections.length, 0);
  assert.equal(first.ledger.snapshot(), undefined);
});
test('C2 failure leaves complete ledger and next created false attempt skips bootstrap', async (t) => {
  const first = await fixture(t, {
    result: (id) => (id === 2 ? { status: 'recovery_conflict' } : {}),
  });
  await assert.rejects(first.runtime.start());
  await first.runtime.close();
  const before = first.ledger.snapshot();
  const next = await fixture(t, {
    ledger: first.ledger,
    registration: { created: false },
  });
  await next.runtime.start();
  assert.equal(next.connections.length, 1);
  assert.deepEqual(first.ledger.snapshot(), before);
});
test('logout while C2 unknown-key verification refresh is pending cannot write after releasing the store', async (t) => {
  const gate = latch();
  let entered = false,
    refreshes = 0;
  const trust = createMemoryLedgerStore();
  const f = await fixture(t, {
    trust,
    auth: (auth, id) => {
      if (id === 2) {
        auth.kid = 'unknown-response';
        auth.keyset_version = '6';
      }
    },
    refresh: async () => {
      if (++refreshes === 4) {
        entered = true;
        await gate.promise;
      }
    },
  });
  const starting = f.runtime.start();
  const rejected = assert.rejects(starting);
  await waitFor(() => entered, 'verification refresh entered');
  f.controller.abort();
  await f.runtime.close();
  await rejected;
  const h = await trust.open();
  const marker = { privateProbe: 'next-owner' };
  await h.commit(marker);
  gate.release();
  await setImmediate();
  await setImmediate();
  assert.deepEqual(trust.snapshot(), marker);
  assert.equal(f.runtime.status.connected, false);
  await h.close();
});

for (const step of ['write_tmp', 'sync_tmp', 'rename', 'sync_dir'])
  test(
    'real file ledger fault at ' +
      step +
      ' cannot expose connected or release writer early',
    async (t) => {
      const root = fileURLToPath(
        new URL('../../../../.agent-evidence/', import.meta.url),
      );
      await mkdir(root, { recursive: true });
      const dir = await mkdtemp(join(root, 'ledger-' + step + '-'));
      const store = createFileLedgerStore({
        dir,
        beforeStep: (at) => {
          if (at === step) throw new Error('fault-' + step);
        },
      });
      const f = await fixture(t, { ledger: store });
      await assert.rejects(f.runtime.start(), new RegExp('fault-' + step));
      await f.runtime.close();
      assert.equal(f.runtime.status.connected, false);
      assert.equal(f.connections.length, 1);
      const reader = await store.open();
      const saved = await reader.load();
      await reader.close();
      if (step === 'sync_dir')
        assert.equal(saved.identity.nodeIncarnation, 'inc-1');
      else assert.equal(saved, undefined);
    },
  );

test('created false plus missing ledger fails before keys/tickets/READY', async (t) => {
  const f = await fixture(t, { registration: { created: false } });
  await assert.rejects(f.runtime.start(), /ledger unavailable/);
  assert.deepEqual(f.events, ['register']);
  assert.equal(f.runtime.status.connected, false);
});

test('complete ledger plus created false uses one normal connection and never initializes again', async (t) => {
  const first = await fixture(t);
  await first.runtime.start();
  await first.runtime.close();
  const second = await fixture(t, {
    ledger: first.ledger,
    registration: { created: false },
  });
  await second.runtime.start();
  assert.equal(second.connections.length, 1);
  assert.equal(second.runtime.status.connected, true);
  assert.equal(second.events.includes('commit'), false);
});

test('real NATS WSS with official device factory closes C1 socket before T2 and binds distinct ingress/application epochs', async (t) => {
  const binary = findNatsServer();
  assert.ok(binary, 'locked nats-server 2.15.1 required');
  const server = await startNatsServer({ binary });
  t.after(() => server.close());
  const peer = await connectPeer(server, { token: 'fixture-no-auth-broker' });
  t.after(() => peer.close());
  const signer = publicationFixture();
  const identity = {
    tenant: '11111111-1111-4111-8111-111111111111',
    realm: '2',
    node: '64',
    device: '33333333-3333-4333-8333-333333333333',
    systemAddress: 'aur1:2.40.0',
  };
  const vectors = JSON.parse(
    await readFile(
      new URL(
        '../../../../contracts/network/fixtures/client-subjects.json',
        import.meta.url,
      ),
    ),
  );
  const frames = [],
    transports = [],
    sockets = [],
    ingresses = new Map();
  let ticketCount = 0;
  const sub = peer.subscribe('auriga.net.up.*.*', {
    callback: (error, msg) => {
      assert.ifError(error);
      const frame = decodeJson(msg.data),
        ingress = msg.subject.split('.').at(-1);
      assert.equal(frame.type, 'ready');
      assert.equal(
        ingresses.has(ingress),
        false,
        'new READY context requires genuinely new ingress',
      );
      ingresses.set(ingress, String(ingresses.size + 1));
      frames.push({ frame, ingress });
      peer.publish(
        'auriga.net.down.' + ingress,
        encodeJson(
          signedReadyResult(signer, frame, { epoch: ingresses.get(ingress) }),
        ),
      );
    },
  });
  t.after(() => sub.unsubscribe());
  await peer.flush();
  t.mock.method(globalThis, 'fetch', async (url, init) => {
    const path = url.pathname;
    assert.equal(init.headers.cookie, undefined);
    assert.equal(init.headers.authorization, 'Bearer native-device');
    if (path.endsWith('/register'))
      return Response.json({ ...identity, created: true });
    if (path.endsWith('/announcement'))
      return Response.json(signedAnnouncement(signer.payload));
    if (path.endsWith('/keys'))
      return Response.json(signedKeyset(signer.payload));
    assert.equal(path, '/api/iam/v1/network/tickets');
    if (ticketCount === 1) {
      assert.equal(sockets[0].readyState, 3);
      assert.equal(transports[0].state, 'stopped');
    }
    const vector = vectors[ticketCount++];
    return Response.json({
      ...vector,
      identity,
      protocolVersion: 1,
      serverAudience: 'test-broker',
      expiresAt: new Date(Date.now() + 30000).toISOString(),
      connectionExpiresAt: new Date(Date.now() + 3600000).toISOString(),
    });
  });
  const provider = createDeviceProvider({
    config: {
      platformOrigin: 'https://portal.example.com',
      issuer: 'https://portal.example.com',
      clientId: 'auriga-dsh',
      providerId: 'team',
      network: { realm: '2', roots: testRoots, wssUrl: server.url },
    },
    getDevice: async () => ({
      accessToken: 'native-device',
      claims: { tid: identity.tenant, did: identity.device, sub: 'subject' },
    }),
  });
  const controller = new AbortController();
  const prepared = await provider.prepare({ signal: controller.signal });
  const { openConnectionRuntime } =
    await import('../../src/network/connection-runtime.js');
  const ledger = createMemoryLedgerStore();
  const runtime = await openConnectionRuntime({
    kind: 'device-connection',
    prepared,
    signal: controller.signal,
    ctx: {},
    getAccess: async () => '',
    authorizeLocal: async () => false,
    stores: { ledger, trust: createMemoryLedgerStore() },
    transportFactory(options) {
      const socket = createCaSocketFactory(server.rootPem);
      const transport = new NatsTransport({
        ...options,
        createSocket: (url) => {
          const value = socket(url);
          sockets.push(value);
          return value;
        },
      });
      transports.push(transport);
      return transport;
    },
  });
  t.after(() => runtime.close());
  await runtime.start();
  assert.equal(runtime.status.connected, true);
  assert.equal(ticketCount, 2);
  assert.equal(frames.length, 2);
  assert.equal(ledger.snapshot().identity.nodeIncarnation, 'inc-1');
  assert.equal(transports[1].epoch, 1);
  assert.equal(ingresses.get(frames[1].ingress), '2');
  peer.publish(
    'auriga.net.down.' + frames[0].ingress,
    encodeJson(signedReadyResult(signer, frames[0].frame, { epoch: '1' })),
  );
  await peer.flush();
  await setImmediate();
  assert.equal(runtime.status.connected, true);
  await runtime.close();
  assert.ok(sockets.every((socket) => socket.readyState === 3));
});

test('plugin actual entry resumes verified native device into official factory, durable C1/C2 connection and logout', async (t) => {
  const root = fileURLToPath(
    new URL('../../../../.agent-evidence/', import.meta.url),
  );
  await mkdir(root, { recursive: true });
  const profile = await mkdtemp(join(root, 'plugin-entry-'));
  const server = await startNatsServer({ binary: findNatsServer() });
  t.after(() => server.close());
  const peer = await connectPeer(server, { token: 'fixture-no-auth-broker' });
  t.after(() => peer.close());
  // TLS test CA only at this isolated server. Product factory and TLS verification stay enabled.
  const originalTls = tls.connect;
  t.mock.method(tls, 'connect', function (options, ...args) {
    return originalTls.call(
      this,
      Number(options.port) === server.port
        ? { ...options, ca: server.rootPem }
        : options,
      ...args,
    );
  });
  const signing = publicationFixture(),
    device = generateKeyPairSync('ec', { namedCurve: 'P-256' });
  const claims = {
    iss: 'https://portal.example.com',
    aud: 'auriga',
    ptyp: 'device',
    ctx: 'device',
    scope: 'auriga.connect',
    tid: '11111111-1111-4111-8111-111111111111',
    did: '33333333-3333-4333-8333-333333333333',
    sub: '22222222-2222-4222-8222-222222222222',
    iat: Math.floor(Date.now() / 1000),
    exp: Math.floor(Date.now() / 1000) + 3600,
  };
  const unsigned = [{ alg: 'ES256', typ: 'at+jwt', kid: 'device-jwk' }, claims]
    .map((v) => Buffer.from(JSON.stringify(v)).toString('base64url'))
    .join('.');
  const accessToken =
    unsigned +
    '.' +
    sign('sha256', Buffer.from(unsigned), {
      key: device.privateKey,
      dsaEncoding: 'ieee-p1363',
    }).toString('base64url');
  const vault = await createVault(join(profile, 'auriga-connect'));
  await vault.update((s) => {
    s.credential = {
      claims,
      accessToken,
      refreshToken: 'fixture-refresh-never-redeemed',
      platformOrigin: claims.iss,
      issuer: claims.iss,
      clientId: 'auriga-dsh',
    };
  });
  vault.close();
  const identity = {
    tenant: claims.tid,
    device: claims.did,
    realm: '2',
    node: '64',
    systemAddress: 'aur1:2.40.0',
  };
  const vectors = JSON.parse(
    await readFile(
      new URL(
        '../../../../contracts/network/fixtures/client-subjects.json',
        import.meta.url,
      ),
    ),
  );
  const wire = [],
    subjects = [],
    calls = [],
    tools = new Set(),
    routes = new Map(),
    effects = [],
    jobs = [];
  let ticketCount = 0,
    hostCalls = 0;
  peer.subscribe('auriga.net.up.*.*', {
    callback: (error, msg) => {
      assert.ifError(error);
      const request = decodeJson(msg.data);
      assert.equal(request.type, 'ready');
      wire.push(request);
      const ingress = msg.subject.split('.').at(-1);
      subjects.push(ingress);
      peer.publish(
        'auriga.net.down.' + ingress,
        encodeJson(
          signedReadyResult(signing, request, { epoch: String(wire.length) }),
        ),
      );
    },
  });
  await peer.flush();
  const http = createServer((req, res) => {
    const row = [...routes.values()].find((route) =>
      req.url.startsWith(route.path),
    );
    if (row) void row.handler(req, res);
    else {
      res.writeHead(404);
      res.end();
    }
  });
  await new Promise((resolve) => http.listen(0, '127.0.0.1', resolve));
  t.after(
    () =>
      new Promise((resolve) => {
        http.closeAllConnections();
        http.close(resolve);
      }),
  );
  const localOrigin = 'http://127.0.0.1:' + http.address().port,
    nativeFetch = globalThis.fetch;
  t.mock.method(globalThis, 'fetch', async (url, init = {}) => {
    url = new URL(url);
    if (url.origin === localOrigin) return nativeFetch(url, init);
    const path = url.pathname;
    calls.push(path);
    assert.equal(new Headers(init.headers).has('cookie'), false);
    if (path === '/oauth2/jwks')
      return Response.json({
        keys: [
          {
            ...device.publicKey.export({ format: 'jwk' }),
            kid: 'device-jwk',
            alg: 'ES256',
            use: 'sig',
          },
        ],
      });
    assert.equal(
      new Headers(init.headers).get('authorization'),
      'Bearer ' + accessToken,
    );
    if (path === '/ai/team/node/register')
      return Response.json({
        endpoint: { id: '44444444-4444-4444-8444-444444444444' },
      });
    if (path === '/ai/mcp') {
      const body = JSON.parse(init.body);
      if (body.method === 'notifications/initialized')
        return new Response(null, { status: 202 });
      return Response.json({
        jsonrpc: '2.0',
        id: body.id,
        result:
          body.method === 'initialize'
            ? { protocolVersion: '2025-06-18' }
            : body.method === 'tools/list'
              ? {
                  tools: [
                    'auriga_list_endpoints',
                    'auriga_open_thread',
                    'auriga_reply_thread',
                    'auriga_read_thread',
                  ].map((name) => ({ name, inputSchema: { type: 'object' } })),
                }
              : {},
      });
    }
    if (path === '/oauth2/revoke') return Response.json({});
    if (path === '/api/iam/v1/network/register')
      return Response.json({ ...identity, created: true });
    if (path.endsWith('/announcement'))
      return Response.json(signedAnnouncement(signing.payload));
    if (path === '/api/collab/v1/network/keys')
      return Response.json(signedKeyset(signing.payload));
    assert.equal(path, '/api/iam/v1/network/tickets');
    const vector = vectors[ticketCount++];
    return Response.json({
      ...vector,
      identity,
      serverAudience: 'fixture-broker',
      expiresAt: new Date(Date.now() + 30000).toISOString(),
      connectionExpiresAt: new Date(Date.now() + 3600000).toISOString(),
      protocolVersion: 1,
    });
  });
  const ctx = {
    profileContext: { dir: profile },
    webServer: {
      protocol: 'http:',
      register(route) {
        routes.set(route.path, route);
        return () => routes.delete(route.path);
      },
    },
    connection: {
      admit: () => ({}),
      authorizeIndex: () => true,
      isTrusted: () => true,
      requestRejection: () => undefined,
    },
    tools: {
      register(tool) {
        tools.add(tool.name);
        return () => tools.delete(tool.name);
      },
    },
    sessions: {
      get() {
        hostCalls++;
      },
      flush() {
        hostCalls++;
      },
    },
    sessionController: {
      create() {
        hostCalls++;
      },
      prompt() {
        hostCalls++;
      },
    },
    on() {
      return () => {};
    },
    effect(fn) {
      effects.push(fn());
    },
    inject(names, fn) {
      if (names.every((name) => ctx[name]))
        jobs.push(Promise.resolve().then(() => fn(ctx)));
    },
  };
  t.after(async () => {
    for (const dispose of effects.reverse()) dispose?.();
    await waitFor(() => Promise.resolve(true), 'effects');
  });
  await Plugin.apply(ctx, {
    connect: {
      platformOrigin: claims.iss,
      issuer: claims.iss,
      clientId: 'auriga-dsh',
      providerId: 'team',
      network: { realm: '2', roots: testRoots, wssUrl: server.url },
    },
  });
  await Promise.all(jobs);
  const before = await nativeFetch(localOrigin + '/auriga/local-login/status');
  const login = await before.json();
  assert.equal(login.network.connected, false);
  assert.equal(calls.length, 0);
  let cookie = before.headers.getSetCookie()[0].split(';')[0];
  const result = await nativeFetch(localOrigin + '/auriga/local-login/login', {
    method: 'POST',
    headers: {
      origin: localOrigin,
      cookie,
      'content-type': 'application/json',
      'x-auriga-csrf': login.csrf,
    },
    body: JSON.stringify({ action: 'resume' }),
  });
  assert.equal(result.status, 200, await result.text());
  cookie = result.headers.getSetCookie()[0].split(';')[0];
  const state = await waitFor(async () => {
    const response = await nativeFetch(
      localOrigin + '/auriga/local-login/status',
      { headers: { cookie } },
    );
    const value = await response.json();
    return value.network.connected && value;
  }, 'plugin official connected');
  assert.equal(state.network.connected, true);
  assert.equal(ticketCount, 2);
  assert.notEqual(subjects[0], subjects[1]);
  assert.equal(wire.length, 2);
  assert.equal(hostCalls, 0);
  assert.equal(tools.has('auriga_send_message'), false);
  const ledger = JSON.parse(
    await readFile(
      join(profile, 'auriga-connect/network/host/ledger.json'),
      'utf8',
    ),
  );
  assert.equal(ledger.identity.nodeIncarnation, 'inc-1');
  const logout = await nativeFetch(localOrigin + '/auriga/local-login/logout', {
    method: 'POST',
    headers: {
      origin: localOrigin,
      cookie,
      'content-type': 'application/json',
      'x-auriga-csrf': state.csrf,
    },
    body: '{}',
  });
  assert.equal(logout.status, 200);
  await waitFor(async () => {
    try {
      await readFile(join(profile, 'auriga-connect/network/host/ledger.lock'));
      return false;
    } catch (e) {
      return e.code === 'ENOENT';
    }
  }, 'logout releases host writer');
  assert.equal(ticketCount, 2);
  assert.equal(hostCalls, 0);
});
