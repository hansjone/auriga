// T3b native context is deliberately a fixture. All identity/network/key/source
// decisions are made by the real Java/Broker/gateway production chain.
import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { createInterface } from 'node:readline';
import { dirname, join } from 'node:path';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { createDeviceProvider } from '../../../clients/dsh-plugin/src/network/device-provider.js';
import { openConnectionRuntime } from '../../../clients/dsh-plugin/src/network/connection-runtime.js';
import { createFileLedgerStore } from '../../../clients/dsh-plugin/src/network/host/store.js';
import { NatsTransport } from '../../../clients/dsh-plugin/src/network/transport/nats-transport.js';
import { createWsSocketFactory } from '../../../clients/dsh-plugin/src/network/transport/ws-socket.js';
import { decodeJson } from '../../../clients/dsh-plugin/src/network/session/frames.js';
import { digest } from '../../../clients/dsh-plugin/src/network/trust/verifier.js';
import { fixture as nativeFixture } from '../../../clients/dsh-plugin/test/network/host/native/fixtures.js';
import { waitUntil } from './assembly-process.mjs';
import { brokerGuards } from './assembly-broker.mjs';

const emit = (value) => process.stdout.write(JSON.stringify(value) + '\n');
const input = createInterface({ input: process.stdin });
const replies = new Map();
let stopping;
const stopRequested = new Promise((resolve) => {
  stopping = resolve;
});
input.on('line', (line) => {
  if (line === 'stop') {
    stopping();
    return;
  }
  const reply = JSON.parse(line);
  const done = replies.get(reply.id);
  if (done) {
    replies.delete(reply.id);
    done(reply);
  }
});
input.on('close', () => stopping());
let nextId = 0;
async function control(command) {
  const id = String(++nextId);
  const promise = new Promise((resolve) => replies.set(id, resolve));
  emit({ stage: 'control', id, command });
  let timer;
  try {
    const reply = await Promise.race([
      promise,
      new Promise((_, reject) => {
        timer = setTimeout(
          () => reject(new Error('control deadline exceeded')),
          25_000,
        );
      }),
    ]);
    assert.equal(reply.error, undefined);
    return reply;
  } finally {
    clearTimeout(timer);
    replies.delete(id);
  }
}

const spec = JSON.parse(
  await readFile(process.env.AURIGA_ASSEMBLY_PLUGIN, 'utf8'),
);
const { ready, config, gateway, artifacts } = spec;
const endpoints = [];
const checks = [];
let snapshotIndex = 0;
async function snapshot(label) {
  const result = (await control({ op: 'snapshot' })).tables;
  await writeFile(
    join(artifacts, `${++snapshotIndex}-${label}.json`),
    JSON.stringify(result, null, 2),
  );
  return result;
}
const table = (s, name) => s[name] ?? [];
const operation = (s, id) =>
  table(s, 'iam.network_source_operation').find(
    (v) => v.method === 'Register' && v.operation_id === id,
  );
const sourceRow = (s, binding) =>
  table(s, 'iam.network_source').find((v) => v.id === binding);

async function endpoint(name, devicePath) {
  const directory = join(dirname(ready.config), name);
  const cwd = join(directory, 'cwd');
  await mkdir(cwd, { recursive: true });
  const native = nativeFixture();
  native.stored.get('session-a').meta.cwd = cwd;
  const agent = native.attach('session-a');
  const tools = new Map(),
    listeners = new Map(),
    frames = [],
    sockets = [],
    transports = [];
  let cleanup = () => {};
  native.ctx.tools = {
    register(tool) {
      tools.set(tool.name, tool);
      return () => tools.delete(tool.name);
    },
  };
  native.ctx.on = (event, callback) => {
    listeners.set(event, callback);
    return () => listeners.delete(event);
  };
  native.ctx.inject = (_services, callback) => {
    native.ctx.effect = (effect) => {
      cleanup = effect();
    };
    callback(native.ctx);
    return {
      dispose() {
        cleanup();
      },
    };
  };
  const roots = [
    {
      kid: config.rootKid,
      public_key_spki: (await readFile(config.rootPublicFile)).toString(
        'base64url',
      ),
    },
  ];
  const provider = createDeviceProvider({
    config: {
      platformOrigin: ready.https,
      issuer: ready.https,
      clientId: 'bridge-device',
      providerId: 'bridge',
      network: { realm: config.realm, wssUrl: gateway.wss, roots },
    },
    async getDevice() {
      const device = JSON.parse(await readFile(devicePath, 'utf8'));
      const claims = JSON.parse(
        Buffer.from(device.access_token.split('.')[1], 'base64url').toString(),
      );
      return { accessToken: device.access_token, claims };
    },
  });
  const prepared = await provider.prepare(new AbortController().signal);
  const store = createFileLedgerStore({ dir: join(directory, 'ledger') });
  const observations = [];
  const openFileStore = store.open.bind(store);
  store.open = async () => {
    const handle = await openFileStore();
    return {
      ...handle,
      async commit(value) {
        await handle.commit(value);
        // Observe only AFTER the genuine file ledger has completed its commit.
        for (const e of Object.values(value.sourceControl?.entries ?? {}))
          if (e.observation) observations.push(structuredClone(e.observation));
      },
    };
  };
  const socketFactory = createWsSocketFactory({
    ca: await readFile(gateway.ca),
    rejectUnauthorized: true,
  });
  const runtime = await openConnectionRuntime({
    kind: 'device-connection',
    prepared,
    ctx: native.ctx,
    directory,
    stores: { ledger: store },
    getAccess: async () => {
      throw new Error('AI credential is outside T3b');
    },
    authorizeLocal: async () => false,
    transportFactory(options) {
      const transport = new NatsTransport({
        ...options,
        createSocket(url) {
          const socket = socketFactory(url);
          sockets.push(socket);
          return socket;
        },
        onMessage(bytes, epoch) {
          frames.push({ direction: 'down', frame: decodeJson(bytes) });
          options.onMessage?.(bytes, epoch);
        },
      });
      const send = transport.send.bind(transport);
      transport.send = (bytes) => {
        frames.push({ direction: 'up', frame: decodeJson(bytes) });
        return send(bytes);
      };
      transports.push(transport);
      return transport;
    },
  });
  const value = {
    name,
    native,
    agent,
    runtime,
    tools,
    listeners,
    frames,
    sockets,
    transports,
    observations,
    prepared,
    socketFactory,
    directory,
    ledger: async () =>
      JSON.parse(await readFile(join(directory, 'ledger/ledger.json'), 'utf8')),
    async join() {
      await waitUntil(
        () => tools.has('auriga_network_join'),
        'join tool missing',
      );
      const result = await tools
        .get('auriga_network_join')
        .execute({}, { agent });
      assert.equal(
        result.status,
        'registered',
        'typed-local production registration failed',
      );
      return result;
    },
  };
  endpoints.push(value);
  await runtime.start();
  assert.equal(runtime.status.connected, true);
  return value;
}

function delivery(source, observation, connection, target) {
  const now = new Date(Math.floor(Date.now() / 1000) * 1000);
  const from = {
    version: 1,
    realm: connection.realm,
    node: connection.node,
    local: source.registration.local,
    type: 'session',
  };
  const message = {
    message_id: randomUUID(),
    from,
    reply_to: from,
    to: {
      version: 1,
      realm: target.realm,
      node: target.node,
      local: '0',
      type: 'system',
    },
    thread_id: randomUUID(),
    thread_sequence: '1',
    in_reply_to: null,
    kind: 'message',
    durability: 'durable',
    flags: [],
    ttl: 60,
    ext: { e2e_signature: null },
    body: { subtype: 'text', text: 'T3b internal authorization only' },
    sender_kind: 'agent',
    message_class: 'unicast',
    intent: 'information',
    authorization_ref: {
      source_binding: source.source_binding,
      source_revision: source.revision,
      policy_revision: observation.policy_revision,
      broadcast_grant_id: null,
    },
    created_at: now.toISOString().replace('.000Z', 'Z'),
    expires_at: new Date(+now + 60_000).toISOString().replace('.000Z', 'Z'),
  };
  return {
    message,
    sourceContext: {
      realm: from.realm,
      node: from.node,
      user: source.owner_user_id,
      sender_kind: 'agent',
      display_label: 'Alice',
      observed_at: observation.observed_at,
      message_digest: digest(message),
    },
  };
}

try {
  const member = await endpoint('member', config.device);
  const external = await endpoint('external', config.externalDevice);
  assert.notEqual(member.directory, external.directory);
  let db = await snapshot('ready');
  assert.equal(table(db, 'iam.network_source').length, 0);
  const connections = table(db, 'iam.network_connection_anchor').map(
    (v) => v.connection_decoded,
  );
  assert.equal(
    connections.length,
    4,
    'both plugins must bootstrap C1 then replace it with C2',
  );
  for (const e of endpoints) {
    assert.ok(e.sockets.length >= 2);
    assert.equal(e.sockets[0].readyState, 3, 'bootstrap C1 must be closed');
    assert.equal(
      e.frames.filter((f) => f.direction === 'up' && f.frame.type === 'ready')
        .length,
      2,
    );
  }
  checks.push(
    'P1: HTTPS device-provider, real callout Redeem, C1 closed/C2 READY, two independent ledgers',
  );

  await control({ op: 'fault', method: 'SourceRegister', mode: 'drop' });
  await member.join();
  const lost = await waitUntil(async () => {
    const ledger = await member.ledger();
    const pending = Object.values(ledger.sourceControl?.operations ?? {}).find(
      (v) => !v.receipt,
    );
    if (!pending) return false;
    const state = await snapshot('lost-outer-reply');
    return operation(state, pending.id) ? { ledger, pending, state } : false;
  }, 'IAM commit/collab saved result did not precede lost outer response');
  assert.equal(lost.ledger.sourceControl.entries['1'].observation, null);
  const original = operation(lost.state, lost.pending.id);
  const originalBytes = original.response;
  const originalRequest = original.request;
  const originalC = original.request_decoded.connection;
  const priorSockets = member.sockets.length;
  member.sockets.at(-1).terminate();
  await waitUntil(
    () =>
      member.sockets.length > priorSockets && member.runtime.status.connected,
    'real broker reconnect failed',
  );
  await waitUntil(
    async () =>
      (await member.ledger()).sourceControl.operations[lost.pending.id].receipt,
    'same-op recovery receipt missing',
  );
  let ledger = await waitUntil(async () => {
    const s = await member.ledger();
    return s.sourceControl.entries['1'].observation ? s : false;
  }, 'fresh Get observation missing');
  db = await snapshot('recovered');
  assert.equal(table(db, 'iam.network_source').length, 1);
  assert.equal(
    operation(db, lost.pending.id).response,
    originalBytes,
    'recovery changed original IAM response bytes',
  );
  assert.equal(
    operation(db, lost.pending.id).request,
    originalRequest,
    'reconnect rewrote frozen original C/body',
  );
  const trace = (await control({ op: 'trace' })).records;
  const recovered = trace.find(
    (v) =>
      v.method === 'SourceRegister' &&
      v.request.operation_id === lost.pending.id &&
      v.request.mode === 'reconcile',
  );
  assert.ok(recovered, 'production plugin must send same-op reconcile');
  assert.notEqual(recovered.connection.ingress, originalC.ingress);
  assert.notEqual(recovered.connection.client_id, originalC.client_id);
  assert.equal(
    table(db, 'collab.network_source_operation').find(
      (v) => v.operation_id === lost.pending.id,
    ).original_connection_decoded.ingress,
    originalC.ingress,
  );
  checks.push(
    'P2/P4/F2: lost outer reply, real C replacement, identical persisted request/receipt, fresh Get required',
  );

  await control({ op: 'fault', method: 'SourceGet', mode: 'corrupt' });
  const beforeCorruption = member.sockets.length;
  member.sockets.at(-1).terminate();
  await waitUntil(
    () =>
      member.sockets.length > beforeCorruption &&
      member.runtime.status.connected,
    'corruption control reconnect failed',
  );
  await waitUntil(
    async () =>
      (await control({ op: 'trace' })).records.some(
        (v) => v.method === 'SourceGet' && v.fault === 'corrupt',
      ),
    'bad signature did not reach production plugin',
  );
  // One regular lane: a second outgoing Get proves the first attempt finished.
  // Inspect every completed real file commit, including observations that a
  // later attempt might already have cleared. No fixed sleep or timing guess.
  const corruptTrace = await waitUntil(async () => {
    const records = (await control({ op: 'trace' })).records.filter(
      (v) => v.method === 'SourceGet' && v.fault === 'corrupt',
    );
    return records.length >= 2 ? records : false;
  }, 'second corrupted Get was not attempted');
  const corruptIds = new Set(corruptTrace.map((v) => v.request.request_id));
  assert.equal(
    member.observations.some((v) => corruptIds.has(v.request_id)),
    false,
    'corrupt Get must not persist an active observation',
  );
  const corruptDb = await snapshot('bad-signature');
  assert.equal(table(corruptDb, 'iam.network_source').length, 1);
  assert.equal(operation(corruptDb, lost.pending.id).response, originalBytes);
  await control({ op: 'fault', method: 'SourceGet', mode: 'none' });
  ledger = await waitUntil(async () => {
    const s = await member.ledger();
    return s.sourceControl.entries['1'].observation ? s : false;
  }, 'valid Get positive control did not recover');
  checks.push(
    'F6: corrupt real signed Get rejected without plugin activation or new issuance; genuine Get restores observation',
  );

  await external.join();
  await waitUntil(
    async () =>
      (await external.ledger()).sourceControl.entries['1']?.observation,
    'EXTERNAL source not active',
  );
  db = await snapshot('external-active');
  assert.equal(table(db, 'iam.network_source').length, 2);
  checks.push(
    'MEMBER and EXTERNAL both issue through production typed-local/source controller',
  );

  // No virtual clock. IAM uses T3a's real 180s source policy; the native
  // witness separately bounds each observation. Wait through the saved expiry.
  const firstBinding = ledger.sourceControl.entries['1'].head;
  const realExpiry = Date.parse(
    ledger.sourceControl.entries['1'].facts[firstBinding].expires_at,
  );
  const expiryWait = realExpiry - Date.now() + 45_000;
  assert.ok(
    Number.isFinite(realExpiry) && expiryWait > 0 && expiryWait <= 240_000,
    'unexpected real source lifetime',
  );
  ledger = await waitUntil(
    async () => {
      const s = await member.ledger(),
        e = s.sourceControl.entries['1'];
      return e.head !== firstBinding && e.observation?.source.state === 'active'
        ? s
        : false;
    },
    'real-clock expiry/successor did not occur',
    expiryWait,
  );
  db = await snapshot('successor');
  const first = sourceRow(db, firstBinding);
  const entry = ledger.sourceControl.entries['1'];
  const successor = sourceRow(db, entry.head);
  assert.equal(first.state, '3');
  assert.equal(successor.predecessor, firstBinding);
  assert.equal(successor.revision, '1');
  assert.equal(operation(db, lost.pending.id).response, originalBytes);
  checks.push(
    'P3: natural real-clock expiry, fresh Get, new op/successor CAS, immutable original receipt',
  );

  const active = entry.observation.source;
  const targetLedger = await external.ledger();
  const targetTrace = (await control({ op: 'trace' })).records
    .filter(
      (v) =>
        v.method === 'Ready' &&
        v.connection.node === targetLedger.identity.node,
    )
    .at(-1);
  const target = targetTrace.connection;
  const source = successor.fact_decoded;
  const request = delivery(source, entry.observation, originalC, target);
  const allowed = await control({
    op: 'delivery',
    connection: target,
    ...request,
  });
  assert.equal(
    allowed.code,
    'OK',
    'fifth RPC positive control must reach live business authorization',
  );
  assert.equal(allowed.allowed, true);
  member.native.agents.delete('session-a');
  member.native.sessions.delete('session-a');
  member.listeners.get('session/disposed')({ id: 'session-a' });
  await waitUntil(
    async () => (await member.ledger()).sourceControl.entries['1'].stopped,
    'local stop not durable',
  );
  db = await waitUntil(async () => {
    const s = await snapshot('revocation');
    const chain = table(s, 'iam.network_source_chain').find(
      (v) => v.id === successor.chain_id,
    );
    return chain?.stopped ? s : false;
  }, 'native close must persist stopped chain');
  assert.equal(sourceRow(db, firstBinding).state, '3');
  assert.equal(sourceRow(db, active.source_binding).state, '2');
  assert.equal(
    (await member.ledger()).sourceControl.entries['1'].observation,
    null,
  );
  assert.ok(
    member.frames.some(
      (v) =>
        v.direction === 'up' &&
        v.frame.type === 'source_revoke' &&
        !('expected_revision' in v.frame),
    ),
  );
  const denied = await control({
    op: 'delivery',
    connection: target,
    ...request,
  });
  assert.equal(denied.code, 'PermissionDenied');
  assert.equal(denied.reason, 'common.forbidden');
  assert.equal(denied.allowed, false);
  checks.push(
    'P5/P6: durable native stop, versionless Revoke, successor REVOKED/old EXPIRED, real fifth RPC terminal forbidden with prior positive control',
  );

  // Every claim is backed by frames, delegated generated RPC and DB snapshots.
  const guards = await brokerGuards(external.prepared, external.socketFactory);
  await writeFile(
    join(artifacts, 'broker-guards.json'),
    JSON.stringify(guards, null, 2),
  );
  checks.push(
    'Broker 2.15.1: spent ticket Redeem denied, foreign ingress pub/sub ACL denied, real account subscription/payload limits',
  );

  const finalTrace = (await control({ op: 'trace' })).records;
  assert.ok(
    finalTrace.filter(
      (v) => v.method === 'RedeemNetworkTicket' && v.code === 'OK',
    ).length >= 5,
  );
  for (const method of [
    'Ready',
    'LocalRegister',
    'SourceRegister',
    'SourceGet',
    'SourceRevoke',
  ])
    assert.ok(
      finalTrace.some((v) => v.method === method && v.code === 'OK'),
      `missing real ${method}`,
    );
  for (const e of endpoints) {
    assert.equal(
      e.frames.some((v) => v.frame.type === 'send'),
      false,
    );
    await writeFile(
      join(artifacts, `${e.name}-frames.json`),
      JSON.stringify(e.frames, null, 2),
    );
    await writeFile(
      join(artifacts, `${e.name}-ledger.json`),
      JSON.stringify(await e.ledger(), null, 2),
    );
  }
  await writeFile(
    join(artifacts, 'rpc.json'),
    JSON.stringify(finalTrace, null, 2),
  );
  emit({ stage: 'verified', checks });
  await stopRequested;
} catch (error) {
  // Preserve the real file commits/frames/DB facts on a business failure too.
  // A mutant's accepted bad signature must remain independently inspectable.
  for (const e of endpoints) {
    await writeFile(
      join(artifacts, `${e.name}-frames.json`),
      JSON.stringify(e.frames, null, 2),
    );
    await writeFile(
      join(artifacts, `${e.name}-observations.json`),
      JSON.stringify(e.observations, null, 2),
    );
    try {
      await writeFile(
        join(artifacts, `${e.name}-ledger.json`),
        JSON.stringify(await e.ledger(), null, 2),
      );
    } catch {
      /* Initialization may have failed. */
    }
  }
  try {
    await snapshot('failure');
  } catch {
    /* Preserve the first failure. */
  }
  emit({
    stage: 'failed',
    name: error.name,
    code: error.code,
    message: error.message,
  });
  process.exitCode = 1;
} finally {
  for (const e of endpoints.reverse()) await e.runtime.close();
  input.close();
}
