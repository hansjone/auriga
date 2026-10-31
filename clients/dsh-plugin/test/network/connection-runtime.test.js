/* global Headers */
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { sign, generateKeyPairSync } from 'node:crypto';
import tls from 'node:tls';
import { createServer } from 'node:http';
import { once } from 'node:events';
import { WebSocketServer } from 'ws';
import * as Plugin from '../../src/plugin.js';
import { createVault } from '../../src/local/vault.js';
import { setImmediate } from 'node:timers/promises';
import {
  createMemoryLedgerStore,
  createFileLedgerStore,
} from '../../src/network/host/store.js';
import { openLedger, reserveLocal } from '../../src/network/host/ledger.js';
import { mkdtemp, mkdir, readFile } from 'node:fs/promises';
import * as nodeFs from 'node:fs/promises';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { tmpdir } from 'node:os';
import { IDENTITY, createFixturePlatform } from './host/fixtures.js';
import { createHostAdapter } from '../../src/network/host/adapter.js';
import { saveProducerEpoch } from '../../src/network/durable/outbox.js';
import { fixture as nativeFixture } from './host/native/fixtures.js';
import { decodeJson, encodeJson } from '../../src/network/session/frames.js';
import { digest, signingInput } from '../../src/network/trust/verifier.js';
import {
  signedKeyset,
  signedAnnouncement,
  testRoots,
  publicationFixture,
  signedReadyResult,
} from './trust/keyset-fixtures.js';
import { NatsTransport } from '../../src/network/transport/nats-transport.js';
import { TransportError } from '../../src/network/transport/errors.js';
import {
  createDeviceProvider,
  DeviceControlUnavailable,
} from '../../src/network/device-provider.js';
import { startNatsServer } from './transport/support/nats-server.js';
import { ensureNatsServer } from './transport/support/nats-binary.js';
import {
  createCaSocketFactory,
  connectPeer,
  waitFor,
} from './transport/support/client.js';

async function fixture(t, extra = {}) {
  const { openConnectionRuntime } =
    await import('../../src/network/connection-runtime.js');
  const { pair, payload, now } = publicationFixture();
  const businessPair = extra.businessDelivery
    ? generateKeyPairSync('ed25519')
    : undefined;
  if (extra.businessDelivery)
    payload.keys.push({
      ...payload.keys[0],
      kid: 'delivery',
      purpose: 'business_delivery',
      public_key_spki: businessPair.publicKey
        .export({ format: 'der', type: 'spki' })
        .toString('base64url'),
    });
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
    logs = [],
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
    ...(extra.storageKey ? { storageKey: extra.storageKey } : {}),
    roots: testRoots,
    url: 'wss://network.example.com/',
    origin: extra.origin ?? 'https://portal.example.com',
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
    return envelope(frame, request, connection);
  }
  function envelope(frame, request, connection) {
    frame.server_auth = {
      version: 1,
      purpose:
        frame.type === 'delivery' ? 'business_delivery' : 'protocol_response',
      alg: 'Ed25519',
      kid: frame.type === 'delivery' ? 'delivery' : 'response',
      issuer_realm: '2',
      issued_at: payload.issued_at,
      not_after: new Date(now() + 120000).toISOString().replace('.000Z', 'Z'),
      audience: {
        realm: '2',
        node: '64',
        incarnation: 'inc-1',
        epoch: frame.epoch ?? String(connection.id),
      },
      request_id: request.request_id,
      channel_nonce: request.channel_nonce,
      keyset_version: '5',
    };
    extra.auth?.(frame.server_auth, connection.id);
    frame.server_auth.signature = sign(
      null,
      signingInput(frame),
      frame.type === 'delivery' ? businessPair.privateKey : pair.privateKey,
    ).toString('base64url');
    return encodeJson(frame);
  }
  runtime = await openConnectionRuntime({
    kind: 'device-connection',
    prepared,
    getAccess: async () => 'unused',
    authorizeLocal: extra.authorizeLocal ?? (async () => false),
    signal: controller.signal,
    now,
    ...(extra.directory
      ? { directory: extra.directory }
      : { stores: { ledger, trust: trustStore } }),
    onRevoked: extra.onRevoked,
    onClosed: extra.onClosed,
    ctx:
      extra.ctx ??
      new Proxy(
        {},
        {
          get(_target, property) {
            if (property === 'logger')
              return {
                warn: (line) =>
                  logs.push(
                    JSON.parse(line.slice('auriga_network_diagnostic '.length)),
                  ),
              };
            assert.fail('basic connection touched Host/tool context');
          },
        },
      ),
    transportFactory(options) {
      if (extra.transportFactory) return extra.transportFactory(options);
      extra.transportOptions?.(options);
      if (extra.factoryError) throw new Error('transport factory failed');
      const connection = {
        id: connections.length + 1,
        epoch: 1,
        state: 'idle',
        async start() {
          options.onState({ state: 'connecting', epoch: 0 });
          await extra.start?.(connection.id);
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
          if (
            [
              'fetch',
              'receive_set',
              'attempt_begin',
              'execution_admit',
              'receipt',
            ].includes(request.type) &&
            extra.mailbox
          ) {
            sent.push({ request, connection });
            const result = await extra.mailbox(request, connection);
            options.onMessage(encodeJson(result), connection.epoch);
            return;
          }
          if (request.type.startsWith('source_') && extra.source) {
            sent.push({ request, connection });
            await extra.source(request, connection);
            return;
          }
          if (request.type === 'local_register') {
            sent.push({ request, connection });
            const frame = decodeJson(
              response(
                { ...request, channel_nonce: connection.nonce },
                connection,
                {
                  type: 'register_result',
                  result: 'registered',
                  local: request.candidate_local,
                  allocation_op_id: request.allocation_op_id,
                  address_type: request.address_type,
                  registration_id: 'reg-' + request.candidate_local,
                  registration_revision: String(
                    BigInt(request.expected_registration_revision) + 1n,
                  ),
                  local_high_water: request.candidate_local,
                  ...extra.registerResult?.(request),
                },
              ),
            );
            frame.server_auth.channel_nonce = connection.nonce;
            frame.server_auth.audience.epoch = String(connection.id);
            extra.registerAuth?.(frame.server_auth);
            frame.server_auth.signature = sign(
              null,
              signingInput(frame),
              pair.privateKey,
            ).toString('base64url');
            extra.registerFrame?.(frame);
            await extra.localRegister?.(request);
            options.onMessage(encodeJson(frame), connection.epoch);
            return;
          }
          assert.equal(request.type, 'ready');
          connection.nonce = request.channel_nonce;
          sent.push({ request, connection });
          events.push('ready-' + connection.id);
          await extra.ready?.(connection.id, request);
          options.onMessage(
            response(
              request,
              connection,
              extra.result?.(connection.id, request),
            ),
            connection.epoch,
          );
          extra.message?.(connection, request);
        },
        message(bytes) {
          options.onMessage(bytes, connection.epoch);
        },
        ticket: (reason = 'refresh') =>
          options.getTicket({ reason, attempt: 0 }),
        refreshError(error) {
          options.onState({ state: 'ready', epoch: connection.epoch, error });
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
    logs,
    connections,
    sent,
    response,
    envelope,
    prepared,
    payload,
    trustStore,
  };
}

test('a new device leaves the old ledger intact and cannot initialize a previously registered device without its own ledger', async (t) => {
  const dir = await mkdtemp(join(tmpdir(), 'auriga-device-generations-'));
  t.after(() => nodeFs.rm(dir, { recursive: true, force: true }));
  const old = await openLedger({
    store: createFileLedgerStore({ dir: join(dir, 'host') }),
    identity: { ...IDENTITY, node: '999' },
    initialize: true,
  });
  await old.close();
  const oldBytes = await readFile(join(dir, 'host/ledger.json'));
  const fresh = await fixture(t, {
    directory: dir,
    storageKey: 'a'.repeat(64),
  });
  await fresh.runtime.start();
  assert.equal(fresh.runtime.status.connected, true);
  await fresh.runtime.close();
  assert.deepEqual(await readFile(join(dir, 'host/ledger.json')), oldBytes);
  const missing = await fixture(t, {
    directory: dir,
    storageKey: 'b'.repeat(64),
    registration: { created: false },
  });
  await assert.rejects(
    missing.runtime.start(),
    /connection ledger unavailable/,
  );
});

function registrationHost() {
  const host = nativeFixture();
  const agent = host.attach('session-a');
  const tools = new Map();
  let cleanup = () => {};
  host.ctx.tools = {
    register(tool) {
      tools.set(tool.name, tool);
      return () => tools.delete(tool.name);
    },
  };
  host.ctx.inject = (_services, callback) => {
    host.ctx.effect = (effect) => {
      cleanup = effect();
    };
    callback(host.ctx);
    return {
      dispose() {
        cleanup();
      },
    };
  };
  return { ...host, agent, tools, unload: () => cleanup() };
}

test('connection runtime refuses an empty trusted Origin before opening transport', async (t) => {
  let opened = false;
  const f = await fixture(t, {
    origin: '',
    transportOptions() {
      opened = true;
    },
  });
  await assert.rejects(f.runtime.start(), /invalidOrigin/);
  assert.equal(opened, false);
});

test('connection runtime default socket retains trusted platform Origin independent of data URL', async (t) => {
  const server = createServer();
  const wss = new WebSocketServer({ server });
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  let socket, opened;
  const incoming = once(wss, 'connection');
  try {
    const f = await fixture(t, {
      transportOptions(options) {
        if (socket) return;
        socket = options.createSocket(
          `ws://127.0.0.1:${server.address().port}/net/v1/ws`,
        );
        opened = once(socket, 'open');
      },
    });
    await f.runtime.start();
    const [peer, request] = await incoming;
    await opened;
    assert.equal(request.headers.origin, 'https://portal.example.com');
    peer.terminate();
  } finally {
    socket?.terminate();
    await new Promise((ok) => wss.close(ok));
    await new Promise((ok) => server.close(ok));
  }
});

for (const count of [1, 129])
  test(`${count}条签名终态经生产FETCH落账后，原pending来源操作跨真实运行时重连保持不变`, async (t) => {
    const cursor = String(count);
    const host = registrationHost();
    const sources = [],
      readyRequests = [];
    let expired = false,
      firstTerminalPage = true;
    const f = await fixture(t, {
      ctx: host.ctx,
      source: (request) => sources.push(request),
      ready: (_id, request) => readyRequests.push(request),
      result(_id, request) {
        return {
          producer_epoch: '42',
          head_seq: expired ? cursor : '0',
          committed_cursor: expired ? cursor : '0',
          local_high_water: request.local_high_water,
          registration_revision: request.registration_revision,
          status:
            request.resume_cursor === (expired ? cursor : '0')
              ? 'ready'
              : 'recovery_conflict',
        };
      },
      mailbox(request, connection) {
        const context = { ...request, channel_nonce: connection.nonce };
        const seal = (frame) =>
          decodeJson(f.envelope(frame, context, connection));
        const offset =
          request.continuation === null ? 0 : Number(request.continuation);
        const end = Math.min(offset + 32, count);
        const items =
          expired && request.resume_cursor === '0'
            ? Array.from({ length: end - offset }, (_, index) => {
                const seq = String(offset + index + 1);
                return seal({
                  app_version: '1.0',
                  type: 'terminal',
                  epoch: String(connection.id),
                  delivery_id: 'expired-delivery-' + seq,
                  mail_seq: seq,
                  target_node: { realm: '2', node: '64' },
                  recipients: [
                    {
                      recipient_id: 'expired-recipient-' + seq,
                      transport_status: 'expired',
                      state_version: '2',
                    },
                  ],
                });
              })
            : [];
        // Expiry can commit while the FETCH header still carries the previous
        // cursor. Reconnect between this durable witness and the next poll.
        const committedCursor = expired && !firstTerminalPage ? cursor : '0';
        if (items.length && end === count) firstTerminalPage = false;
        return {
          app_version: '1.0',
          type: 'fetch_result',
          items,
          page: seal({
            app_version: '1.0',
            type: 'fetch_page',
            epoch: String(connection.id),
            request_id: request.request_id,
            head_seq: expired ? cursor : '0',
            snapshot_head: expired ? cursor : '0',
            scan_after: expired ? String(end) : '0',
            committed_cursor: committedCursor,
            next_continuation: items.length && end < count ? String(end) : null,
            manifest: items.map((item) => ({
              item_type: 'terminal',
              mail_seq: item.mail_seq,
              digest: digest(item),
            })),
          }),
        };
      },
    });
    f.prepared.submit = async () => {
      throw new Error('no HTTPS send in this fixture');
    };
    await f.runtime.start();
    await waitFor(
      () => host.tools.has('auriga_network_join'),
      'join installed',
    );
    await host.tools
      .get('auriga_network_join')
      .execute({}, { agent: host.agent });
    await waitFor(() => sources.length > 0, 'pending source recorded');
    expired = true;
    await waitFor(
      () => f.ledger.snapshot().durable.terminal?.[cursor],
      'signed terminal evidence committed',
    );
    const before = f.ledger.snapshot();
    assert.equal(before.durable.resumeCursor, '0');
    assert.equal(Object.keys(before.durable.inbound).length, 0);
    const transport = f.connections.at(-1);
    transport.transition('reconnecting', transport.epoch);
    transport.transition('ready', transport.epoch + 1);
    await waitFor(
      () => f.runtime.status.connected || !f.runtime.status.available,
      'strict READY completed',
    );
    assert.equal(f.runtime.status.connected, true);
    assert.equal(
      f.connections.length,
      3,
      'conflict C must close before fresh strict READY',
    );
    assert.equal(transport.state, 'stopped');
    assert.equal(readyRequests.at(-1).resume_cursor, cursor);
    assert.equal(f.ledger.snapshot().durable.resumeCursor, cursor);
    assert.equal(Object.keys(before.durable.terminal).length, count);
    await waitFor(() => sources.length > 1, 'original pending source resumed');
    assert.equal(sources[1].operation_id, sources[0].operation_id);
    const after = f.ledger.snapshot();
    assert.deepEqual(
      after.sourceControl.operations,
      before.sourceControl.operations,
    );
    assert.deepEqual(after.durable.terminal, before.durable.terminal);
    assert.equal(host.calls.filter((v) => v[0] === 'prompt').length, 0);
  });

test('正常生产收件与终态重连共用文件账本，回执及重连后的正常收件同时成立', async (t) => {
  const host = registrationHost();
  const root = fileURLToPath(
    new URL('../../../../.agent-evidence/', import.meta.url),
  );
  await mkdir(root, { recursive: true });
  const dir = await mkdtemp(join(root, 'send-receive-reconnect-'));
  let firstIntakeRenameDenied = false;
  const store = createFileLedgerStore({
    dir,
    platform: 'win32',
    fs: {
      ...nodeFs,
      async rename(from, to) {
        const draft = JSON.parse(await nodeFs.readFile(from, 'utf8'));
        if (
          !firstIntakeRenameDenied &&
          draft.durable?.inbound['recipient-1']?.stage === 'received'
        ) {
          firstIntakeRenameDenied = true;
          throw Object.assign(
            new Error('reader temporarily denies first intake replacement'),
            { code: 'EPERM' },
          );
        }
        await nodeFs.rename(from, to);
      },
    },
  });
  const commitFailures = [];
  const open = store.open.bind(store);
  store.open = async () => {
    const handle = await open();
    return {
      ...handle,
      async commit(value) {
        try {
          await handle.commit(value);
        } catch (error) {
          commitFailures.push({
            name: error.name,
            code: error.code,
            message: error.message,
          });
          throw error;
        }
      },
    };
  };
  const saved = async () =>
    JSON.parse(await readFile(join(dir, 'ledger.json'), 'utf8'));
  const localControl = {};
  const requests = [],
    sources = [];
  let stage = 0,
    serverCursor = '0';
  const f = await fixture(t, {
    ledger: store,
    ctx: host.ctx,
    businessDelivery: true,
    authorizeLocal: async (value) => value === localControl,
    source: (request) => sources.push(request),
    result(_id, request) {
      return {
        producer_epoch: '42',
        head_seq: String(stage),
        committed_cursor: serverCursor,
        local_high_water: request.local_high_water,
        registration_revision: request.registration_revision,
        status:
          request.resume_cursor === serverCursor
            ? 'ready'
            : 'recovery_conflict',
      };
    },
    async mailbox(request, connection) {
      requests.push(request);
      const context = { ...request, channel_nonce: connection.nonce };
      const seal = (frame) =>
        decodeJson(
          f.envelope(
            { app_version: '1.0', epoch: String(connection.id), ...frame },
            context,
            connection,
          ),
        );
      if (request.type === 'receive_set')
        return seal({
          type: 'receive_set_result',
          request_id: request.request_id,
          registration_id: 'reg-1',
          enabled: request.enabled,
          state_version: '2',
        });
      if (request.type === 'attempt_begin')
        return seal({
          type: 'attempt_result',
          request_id: request.request_id,
          recipient_id: request.recipient_id,
          attempt_id: request.attempt_id,
          result: 'entered',
        });
      if (request.type === 'execution_admit')
        return seal({
          type: 'admit_result',
          request_id: request.request_id,
          recipient_id: request.recipient_id,
          result: 'admitted',
          admitted_epoch: String(connection.id),
          state_version: '1',
          admission_id: 'admission-' + stage,
        });
      if (request.type === 'receipt') {
        const s = await saved();
        const recipient = request.results[0];
        assert.equal(
          s.durable.inbound[recipient.recipient_id].stage,
          'accepted',
        );
        assert.equal(recipient.expected_state_version, '1');
        assert.notEqual(String(s.revision), '1');
        assert.ok(host.calls.some((v) => v[0] === 'persistence-flush'));
        serverCursor = request.mail_seq;
        return seal({
          type: 'receipt_committed',
          request_id: request.request_id,
          operation_id: request.operation_id,
          delivery_id: request.delivery_id,
          mail_seq: request.mail_seq,
          committed_cursor: serverCursor,
          mail_item_closed: true,
          results: [
            {
              recipient_id: recipient.recipient_id,
              transport_status: 'host_accepted',
              state_version: '2',
            },
          ],
        });
      }
      assert.equal(request.type, 'fetch');
      const items = [];
      const cursor = serverCursor;
      if (stage && BigInt(request.resume_cursor) < BigInt(stage)) {
        if (stage === 2) {
          items.push(
            seal({
              type: 'terminal',
              delivery_id: 'delivery-2',
              mail_seq: '2',
              target_node: { realm: '2', node: '64' },
              recipients: [
                {
                  recipient_id: 'recipient-2',
                  transport_status: 'expired',
                  state_version: '2',
                },
              ],
            }),
          );
          // Header captures the cursor before expiration commits; force the
          // next READY to reconcile retained terminal evidence on another C.
          serverCursor = '2';
        } else {
          const message = {
            message_id: 'message-' + stage,
            kind: 'message',
            message_class: 'unicast',
            sender_kind: 'agent',
            from: {
              version: 1,
              realm: '2',
              node: '63',
              local: '1',
              type: 'session',
            },
            to: {
              version: 1,
              realm: '2',
              node: '64',
              local: '1',
              type: 'session',
            },
            body: { subtype: 'text', text: 'normal body ' + stage },
            expires_at: f.payload.valid_until,
          };
          items.push(
            seal({
              type: 'delivery',
              delivery_id: 'delivery-' + stage,
              mail_seq: String(stage),
              target_node: { realm: '2', node: '64' },
              message,
              source_context: {
                realm: '2',
                node: '63',
                sender_kind: 'agent',
                message_digest: digest(message),
              },
              recipients: [
                {
                  recipient_id: 'recipient-' + stage,
                  local: '1',
                  type: 'session',
                  state_version: '1',
                },
              ],
            }),
          );
        }
      }
      return {
        app_version: '1.0',
        type: 'fetch_result',
        items,
        page: seal({
          type: 'fetch_page',
          request_id: request.request_id,
          head_seq: String(stage),
          snapshot_head: String(stage),
          scan_after: String(stage),
          committed_cursor: cursor,
          next_continuation: null,
          manifest: items.map((item) => ({
            item_type: item.type,
            mail_seq: item.mail_seq,
            digest: digest(item),
          })),
        }),
      };
    },
  });
  f.prepared.submit = async () => {
    throw new Error('no submit fixture');
  };
  await f.runtime.start();
  await waitFor(() => host.tools.has('auriga_network_join'), 'join installed');
  await host.tools
    .get('auriga_network_join')
    .execute({}, { agent: host.agent });
  await waitFor(
    () => f.runtime.diagnostics.messaging.available,
    'messaging installed',
  );
  await f.runtime
    .messagingUI(localControl)
    .setReceive({ local: '1', enabled: true });
  stage = 1;
  await waitFor(
    async () =>
      (await saved()).durable.inbound['recipient-1']?.stage === 'committed',
    'first normal receipt',
  ).catch(async (error) => {
    throw new Error(
      error.message +
        JSON.stringify({
          status: f.runtime.status,
          diagnostics: f.runtime.diagnostics,
          requests: requests.map((v) => v.type),
          commitFailures,
        }),
    );
  });
  assert.equal((await saved()).durable.resumeCursor, '1');
  assert.equal(firstIntakeRenameDenied, true);
  stage = 2;
  await waitFor(
    async () => (await saved()).durable.terminal?.['2'],
    'terminal retained',
  ).catch(async (error) => {
    throw new Error(
      error.message +
        JSON.stringify({
          status: f.runtime.status,
          diagnostics: f.runtime.diagnostics,
          requests: requests.map((v) => v.type),
          commitFailures,
        }),
    );
  });
  const before = await saved();
  const connection = f.connections.at(-1);
  connection.transition('reconnecting', connection.epoch);
  connection.transition('ready', connection.epoch + 1);
  await waitFor(
    () => f.runtime.status.connected || !f.runtime.status.available,
    'reconnect finished',
  );
  assert.equal(f.runtime.status.connected, true);
  assert.equal(connection.state, 'stopped');
  assert.equal((await saved()).durable.resumeCursor, '2');
  await waitFor(() => sources.length > 1, 'original source resumed');
  assert.deepEqual(
    (await saved()).sourceControl.operations,
    before.sourceControl.operations,
  );
  stage = 3;
  await waitFor(
    async () =>
      (await saved()).durable.inbound['recipient-3']?.stage === 'committed',
    'normal receipt after terminal reconnect',
  );
  assert.equal((await saved()).durable.resumeCursor, '3');
  assert.equal(requests.filter((v) => v.type === 'receipt').length, 2);
  assert.equal(host.calls.filter((v) => v[0] === 'prompt').length, 2);
});

test('signed READY cursor only reconciles a complete retained acceptance prefix on a fresh C', async (t) => {
  const host = registrationHost();
  const store = createMemoryLedgerStore();
  const ledger = await openLedger({
    store,
    identity: IDENTITY,
    initialize: true,
  });
  await saveProducerEpoch(ledger, '42');
  const platform = createFixturePlatform();
  const adapter = createHostAdapter({
    ledger,
    host: await (
      await import('../../src/network/host/native/host-port.js')
    ).createNativeHostPort({ ctx: host.ctx }),
    network: platform.network,
  });
  await adapter.bindExistingSession({
    opId: 'join-1',
    hostSessionId: host.agent.id,
    cwd: host.agent.session.header.cwd,
    kind: 'session',
  });
  const binding = ledger.peek().bindings['1'];
  await ledger.update((draft) => {
    draft.durable.inbound['recipient-1'] = {
      delivery: {
        type: 'delivery',
        delivery_id: 'delivery-1',
        mail_seq: '1',
        source_context: { message_digest: 'sha256:' + 'a'.repeat(64) },
      },
      recipientId: 'recipient-1',
      local: '1',
      bindingId: binding.bindingId,
      hostSessionId: binding.hostSessionId,
      attemptId: 'attempt-1',
      epoch: '1',
      stateVersion: '1',
      stage: 'accepted',
      admission: {},
      receipt: {
        delivery_id: 'delivery-1',
        mail_seq: '1',
        results: [
          {
            recipient_id: 'recipient-1',
            host_request_id: 'recipient-1',
            message_digest: 'sha256:' + 'a'.repeat(64),
            status: 'host_accepted',
          },
        ],
      },
    };
  });
  await ledger.close();
  const f = await fixture(t, {
    ledger: store,
    ctx: {},
    result(id, request) {
      assert.equal(request.resume_cursor, id === 1 ? '0' : '1');
      return {
        producer_epoch: '42',
        head_seq: '1',
        committed_cursor: '1',
        local_high_water: '1',
        registration_revision: '1',
        status: id === 1 ? 'recovery_conflict' : 'ready',
      };
    },
  });
  await f.runtime.start();
  assert.equal(f.runtime.status.connected, true);
  assert.equal(f.connections.length, 2);
  assert.equal(f.connections[0].state, 'stopped');
  assert.equal(store.snapshot().durable.resumeCursor, '1');
  assert.equal(
    store.snapshot().durable.inbound['recipient-1'].stage,
    'accepted',
    'READY cursor is not an acceptance receipt',
  );
});

test('bootstrap and normal READY retry authenticated overload with identical requests and no extra connections', async (t) => {
  const sends = new Map();
  const f = await fixture(t, {
    result(id) {
      sends.set(id, (sends.get(id) ?? 0) + 1);
      return sends.get(id) === 1
        ? {
            type: 'error',
            epoch: '0',
            code: 'network.overloaded',
            scope: 'request',
            retryable: true,
          }
        : {};
    },
    auth(auth, id) {
      if (sends.get(id) === 1) {
        auth.audience.incarnation = 'unbound';
        auth.audience.epoch = '0';
      }
    },
  });
  await f.runtime.start();
  assert.equal(f.runtime.status.connected, true);
  assert.equal(f.connections.length, 2);
  assert.equal(f.events.filter((v) => v === 'register').length, 1);
  for (const id of [1, 2]) {
    const requests = f.sent
      .filter((v) => v.connection.id === id)
      .map((v) => v.request);
    assert.equal(requests.length, 2);
    assert.deepEqual(requests[1], requests[0]);
  }
});

test('strict C2 installs real zero-input registration tool, same writer persists credentials and binding across reopen', async (t) => {
  const host = registrationHost();
  const root = fileURLToPath(
    new URL('../../../../.agent-evidence/', import.meta.url),
  );
  await mkdir(root, { recursive: true });
  const dir = await mkdtemp(join(root, 'session-registration-'));
  const ledger = createFileLedgerStore({ dir });
  let opens = 0;
  const tracked = {
    open() {
      opens++;
      return ledger.open();
    },
  };
  const f = await fixture(t, { ctx: host.ctx, ledger: tracked });
  assert.equal(host.tools.size, 0);
  await f.runtime.start();
  await waitFor(
    () => host.tools.has('auriga_network_join'),
    'registration installed',
  );
  const tool = host.tools.get('auriga_network_join');
  const results = await Promise.all([
    tool.execute({}, { agent: host.agent }),
    tool.execute({}, { agent: host.agent }),
  ]);
  assert.deepEqual(results, [
    { status: 'registered', address: 'aur1:2.40.1' },
    { status: 'registered', address: 'aur1:2.40.1' },
  ]);
  assert.equal(
    opens,
    1,
    'bootstrap and registration must share exactly one writer',
  );
  assert.deepEqual(
    f.sent.map(({ request }) => request.type),
    ['ready', 'ready', 'local_register'],
  );
  const request = f.sent.at(-1).request;
  await f.runtime.close();
  assert.equal(host.tools.size, 0);
  const h = await ledger.open();
  const saved = await h.load();
  await h.close();
  assert.equal(saved.locals['1'].opId, request.allocation_op_id);
  assert.equal(
    'sha256:' + saved.locals['1'].fingerprint,
    request.allocation_fingerprint,
  );
  assert.equal(saved.locals['1'].registrationId, 'reg-1');
  assert.equal(saved.locals['1'].registrationRevision, '1');
  assert.equal(saved.locals['1'].addressType, 'session');
  assert.equal(saved.bindings['1'].hostSessionId, 'session-a');
  assert.equal(saved.bindings['1'].receiveMode, 'receive_none');
  for (const key of ['recipients', 'relations', 'inbox', 'bodies'])
    assert.deepEqual(saved[key], {});
  assert.equal(
    host.calls.some(([name]) => ['create', 'prompt'].includes(name)),
    false,
  );
});

for (const [name, change, auth] of [
  ['type', { type: 'ready_result' }],
  ['op', { allocation_op_id: 'different' }],
  ['address type', { address_type: 'subagent' }],
  ['local', { local: '2' }],
  ['revision', { registration_revision: '2' }],
  ['high water', { local_high_water: '0' }],
  [
    'purpose',
    null,
    (a) => {
      a.purpose = 'business_delivery';
    },
  ],
  [
    'audience',
    null,
    (a) => {
      a.audience.node = '65';
    },
  ],
  [
    'epoch',
    null,
    (a) => {
      a.audience.epoch = '99';
    },
  ],
  [
    'nonce',
    null,
    (a) => {
      a.channel_nonce = 'wrong';
    },
  ],
])
  test(
    'registration wire rejects signed wrong ' +
      name +
      ' and preserves original pending',
    async (t) => {
      const host = registrationHost();
      const f = await fixture(t, {
        ctx: host.ctx,
        registerResult: () => change ?? {},
        registerAuth: auth,
      });
      await f.runtime.start();
      await waitFor(() => host.tools.size, 'join installed');
      assert.deepEqual(
        await host.tools
          .get('auriga_network_join')
          .execute({}, { agent: host.agent }),
        { status: 'unavailable' },
      );
      assert.equal(f.ledger.snapshot().locals['1'].state, 'reserved');
      assert.deepEqual(f.ledger.snapshot().bindings, {});
    },
  );

test('registration rejects invalid signature without binding', async (t) => {
  const host = registrationHost();
  const f = await fixture(t, {
    ctx: host.ctx,
    registerFrame: (frame) => {
      frame.server_auth.signature = 'A'.repeat(86);
    },
  });
  await f.runtime.start();
  await waitFor(() => host.tools.size, 'join installed');
  assert.deepEqual(
    await host.tools
      .get('auriga_network_join')
      .execute({}, { agent: host.agent }),
    { status: 'unavailable' },
  );
  assert.deepEqual(f.ledger.snapshot().bindings, {});
});

test('missing Host durability leaves strict base READY independent and does not install join', async (t) => {
  const host = registrationHost();
  delete host.ctx.sessions.flush;
  const f = await fixture(t, { ctx: host.ctx });
  await f.runtime.start();
  await setImmediate();
  assert.equal(f.runtime.status.connected, true);
  assert.equal(host.tools.size, 0);
  assert.deepEqual(f.runtime.diagnostics.registration, {
    available: false,
    reason: 'host_unavailable',
  });
});

for (const event of ['logout', 'disconnect', 'new epoch', 'host unload'])
  test(
    event +
      ' fences delayed LOCAL_REGISTER while transient loss preserves the tool schema',
    async (t) => {
      const host = registrationHost(),
        gate = latch();
      let entered = false;
      const f = await fixture(t, {
        ctx: host.ctx,
        localRegister: async () => {
          entered = true;
          await gate.promise;
        },
      });
      await f.runtime.start();
      await waitFor(() => host.tools.size, 'join installed');
      const tool = host.tools.get('auriga_network_join');
      const pending = tool.execute({}, { agent: host.agent });
      await waitFor(() => entered, 'register sent');
      const original = f.ledger.snapshot().locals['1'];
      if (event === 'logout') f.controller.abort();
      else if (event === 'host unload') host.unload();
      else
        f.connections[1].transition(
          event === 'disconnect' ? 'reconnecting' : 'ready',
          2,
        );
      const transient = event === 'disconnect' || event === 'new epoch';
      assert.equal(host.tools.size, transient ? 1 : 0);
      gate.release();
      const expected = transient
        ? {
            status: 'unavailable',
            reason: 'network.overloaded',
            retryable: true,
          }
        : { status: 'unavailable', retryable: false };
      assert.deepEqual(await pending, expected);
      assert.deepEqual(f.ledger.snapshot().locals['1'], original);
      assert.deepEqual(f.ledger.snapshot().bindings, {});
      assert.deepEqual(await tool.execute({}, { agent: host.agent }), expected);
    },
  );

test('reconnect first reconciles original committed operation and reuses host binding without lifting counters', async (t) => {
  const host = registrationHost();
  let lose = true,
    f;
  f = await fixture(t, {
    ctx: host.ctx,
    registerFrame(frame) {
      if (lose) frame.server_auth.signature = 'A'.repeat(86);
    },
    result(id) {
      if (id !== 2 || !f?.ledger.snapshot()?.locals['1']) return {};
      return { local_high_water: '1', registration_revision: '1' };
    },
  });
  await f.runtime.start();
  await waitFor(() => host.tools.size, 'join installed');
  assert.deepEqual(
    await host.tools
      .get('auriga_network_join')
      .execute({}, { agent: host.agent }),
    { status: 'unavailable' },
  );
  const original = f.ledger.snapshot().locals['1'];
  lose = false;
  f.connections[1].transition('reconnecting', 2);
  f.connections[1].transition('ready', 2);
  await waitFor(
    () => f.runtime.diagnostics.registration.available,
    'join reinstalled after original-op reconciliation',
  );
  const registrations = f.sent.filter(
    ({ request }) => request.type === 'local_register',
  );
  assert.equal(registrations.length, 2);
  assert.equal(registrations[1].request.allocation_op_id, original.opId);
  assert.equal(
    registrations[1].request.allocation_fingerprint,
    'sha256:' + original.fingerprint,
  );
  assert.deepEqual(
    await host.tools
      .get('auriga_network_join')
      .execute({}, { agent: host.agent }),
    { status: 'registered', address: 'aur1:2.40.1' },
  );
  assert.equal(f.ledger.snapshot().localHighWater, '1');
});

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

test('legacy pending operation and unproven receipt revision are transmitted intact and are not cleared for recovery', async (t) => {
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
  const handle = await store.open();
  const legacy = await handle.load();
  delete legacy.receiptRevision;
  await handle.commit(legacy);
  await handle.close();
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

test('real registration writes preserve an independent empty receipt checkpoint across reconnect and file reopen', async (t) => {
  const host = registrationHost();
  const root = fileURLToPath(
    new URL('../../../../.agent-evidence/', import.meta.url),
  );
  await mkdir(root, { recursive: true });
  const dir = await mkdtemp(join(root, 'receipt-reconnect-'));
  const store = createFileLedgerStore({ dir });
  const readyRequests = [];
  const sourceRequests = [];
  const result = (_id, request) => ({
    // Match the production receiptReady gate, rather than always returning ready.
    status:
      request.receipt_checkpoint.checkpoint_revision === '0'
        ? 'ready'
        : 'recovery_conflict',
    local_high_water: request.local_high_water,
    registration_revision: request.registration_revision,
  });
  const f = await fixture(t, {
    ctx: host.ctx,
    ledger: store,
    result,
    ready: (_id, request) => readyRequests.push(request),
    // Observe a genuine production source request after its file commit; leave
    // its reply absent so reconnect must preserve the frozen pending operation.
    source: (request) => sourceRequests.push(request),
  });
  await f.runtime.start();
  await waitFor(
    () => host.tools.has('auriga_network_join'),
    'registration installed',
  );
  await host.tools
    .get('auriga_network_join')
    .execute({}, { agent: host.agent });
  await waitFor(() => sourceRequests.length > 0, 'source request committed');
  const before = JSON.parse(await readFile(join(dir, 'ledger.json'), 'utf8'));
  assert.ok(before.revision > 0);
  assert.equal(before.registrationRevision, '1');
  const transport = f.connections.at(-1);
  transport.transition('reconnecting', transport.epoch);
  transport.transition('ready', transport.epoch + 1);
  await waitFor(
    () => f.runtime.status.connected || !f.runtime.status.available,
    'READY completed',
  );
  assert.equal(f.runtime.status.connected, true);
  const request = readyRequests.at(-1);
  assert.equal(request.receipt_checkpoint.checkpoint_revision, '0');
  assert.equal(request.registration_revision, before.registrationRevision);
  assert.equal(request.local_high_water, before.localHighWater);
  assert.equal(request.pending_registration, null);
  await waitFor(() => sourceRequests.length > 1, 'same source op retried');
  assert.equal(sourceRequests[1].operation_id, sourceRequests[0].operation_id);
  await f.runtime.close();
  const reopened = await fixture(t, {
    ledger: store,
    registration: { created: false },
    result,
  });
  await reopened.runtime.start();
  assert.equal(reopened.runtime.status.connected, true);
  assert.equal(
    reopened.sent[0].request.receipt_checkpoint.checkpoint_revision,
    '0',
  );
  const saved = JSON.parse(await readFile(join(dir, 'ledger.json'), 'utf8'));
  assert.deepEqual(saved.locals, before.locals);
  assert.deepEqual(saved.bindings, before.bindings);
  assert.deepEqual(
    saved.sourceControl.operations,
    before.sourceControl.operations,
  );
  assert.ok(saved.revision >= before.revision);
});

test('a nonzero receipt history is preserved and never bypasses the real receiptReady gate', async (t) => {
  const store = createMemoryLedgerStore();
  const ledger = await openLedger({
    store,
    identity: IDENTITY,
    initialize: true,
  });
  await ledger.update((d) => {
    d.audit.push({ at: 'x', recipientId: 'r', reason: 'probe' });
  });
  await ledger.update((d) => {
    d.audit.length = 0;
  });
  await ledger.close();
  const before = store.snapshot();
  assert.equal(before.receiptRevision, 2);
  const f = await fixture(t, {
    ledger: store,
    registration: { created: false },
    result: (_id, request) => ({
      status:
        request.receipt_checkpoint.checkpoint_revision === '0'
          ? 'ready'
          : 'recovery_conflict',
    }),
  });
  await assert.rejects(f.runtime.start());
  assert.equal(f.sent[0].request.receipt_checkpoint.checkpoint_revision, '2');
  assert.equal(f.runtime.status.connected, false);
  assert.deepEqual(store.snapshot(), before);
});

function latch() {
  let release;
  const promise = new Promise((r) => {
    release = r;
  });
  return { promise, release };
}

test('one socket batch can deliver READY plus unrelated frames without disconnecting', async (t) => {
  const f = await fixture(t, {
    message(connection) {
      connection.message(encodeJson({ type: 'unrelated' }));
      connection.message(encodeJson({ type: 'unrelated' }));
    },
  });
  await f.runtime.start();
  await setImmediate();
  assert.equal(f.runtime.status.connected, true);
});

test('announcement refresh queues later frames and resumes in order', async (t) => {
  const gate = latch();
  let block = false,
    entered = false;
  const f = await fixture(t, {
    refresh: async () => {
      if (block) {
        entered = true;
        await gate.promise;
      }
    },
  });
  await f.runtime.start();
  block = true;
  f.connections[1].message(
    encodeJson({ keyset_announcement: signedAnnouncement(f.payload) }),
  );
  await waitFor(() => entered, 'announcement refresh');
  assert.equal(f.runtime.status.connected, false);
  for (let i = 0; i < 64; i++)
    f.connections[1].message(encodeJson({ type: 'unrelated' }));
  assert.equal(f.runtime.status.available, true);
  gate.release();
  await waitFor(
    () => f.runtime.status.connected,
    'queue resumed after announcement',
  );
  assert.equal(f.runtime.status.state, 'ready');
});

test('only exceeding the 64 pending frame bound closes with message_backpressure', async (t) => {
  const gate = latch();
  let block = false,
    entered = false,
    reason;
  const f = await fixture(t, {
    onClosed: (why) => {
      reason = why;
    },
    refresh: async () => {
      if (block) {
        entered = true;
        await gate.promise;
      }
    },
  });
  await f.runtime.start();
  block = true;
  f.connections[1].message(
    encodeJson({ keyset_announcement: signedAnnouncement(f.payload) }),
  );
  await waitFor(() => entered, 'blocked refresh');
  for (let i = 0; i < 64; i++)
    f.connections[1].message(encodeJson({ type: 'unrelated' }));
  assert.equal(f.runtime.status.available, true);
  f.connections[1].message(encodeJson({ type: 'unrelated' }));
  assert.equal(f.runtime.status.available, false);
  gate.release();
  await f.runtime.close();
  assert.equal(reason, 'message_backpressure');
});

test('transport factory synchronous failure still closes and releases both store locks', async (t) => {
  const f = await fixture(t, { factoryError: true });
  await assert.rejects(f.runtime.start(), /transport factory failed/);
  await assert.doesNotReject(f.runtime.close());
  const h = await f.ledger.open();
  await h.close();
  const cache = await f.trustStore.open();
  await cache.close();
});

test('synchronous transport stop failure cannot prevent ledger and trust lock release', async (t) => {
  const f = await fixture(t);
  await f.runtime.start();
  f.connections[1].stop = () => {
    throw new Error('synchronous stop failure');
  };
  await assert.doesNotReject(f.runtime.close());
  for (const store of [f.ledger, f.trustStore]) {
    const h = await store.open();
    await h.close();
  }
});

test('queued old-epoch frames are discarded after announcement refresh and cannot fail the new READY', async (t) => {
  const gate = latch();
  let block = false,
    entered = false;
  const f = await fixture(t, {
    refresh: async () => {
      if (block) {
        entered = true;
        await gate.promise;
      }
    },
  });
  await f.runtime.start();
  block = true;
  f.connections[1].message(
    encodeJson({ keyset_announcement: signedAnnouncement(f.payload) }),
  );
  await waitFor(() => entered, 'old epoch refresh');
  f.connections[1].message(new TextEncoder().encode('invalid old JSON'));
  f.connections[1].transition('reconnecting', 1);
  f.connections[1].transition('ready', 2);
  await waitFor(() => f.sent.length === 3, 'new epoch READY queued');
  block = false;
  gate.release();
  await waitFor(() => f.runtime.status.connected, 'new epoch verified');
  assert.equal(f.runtime.status.available, true);
});

for (const error of [
  { status: 401 },
  { status: 403, code: 'network.revoked' },
  { code: 'network.revoked' },
  { status: 403 },
  { status: 403, code: 'network.unauthorized' },
])
  test(
    'ticket denial distinguishes credential revocation ' +
      JSON.stringify(error),
    async (t) => {
      let revoked = 0,
        closedReason;
      const f = await fixture(t, {
        onRevoked: () => {
          revoked++;
        },
        onClosed: (reason) => {
          closedReason = reason;
        },
      });
      await f.runtime.start();
      f.prepared.getTicket = async () => {
        throw error;
      };
      await assert.rejects(f.connections[1].ticket());
      await f.runtime.close();
      const isRevoked =
        error.status === 401 || error.code === 'network.revoked';
      assert.equal(revoked, isRevoked ? 1 : 0);
      assert.equal(closedReason, isRevoked ? 'revoked' : 'forbidden');
    },
  );

for (const error of [
  new TypeError('fetch failed'),
  { name: 'TimeoutError' },
  { status: 429 },
  { status: 503 },
])
  test(
    'healthy READY survives transient refresh failure ' +
      (error.status ?? error.name),
    async (t) => {
      const f = await fixture(t);
      await f.runtime.start();
      const original = f.prepared.getTicket;
      f.prepared.getTicket = async () => {
        throw error;
      };
      await assert.rejects(f.connections[1].ticket());
      f.connections[1].refreshError(error);
      await setImmediate();
      assert.equal(f.runtime.status.connected, true);
      assert.equal(f.runtime.status.available, true);
      f.prepared.getTicket = original;
      await f.connections[1].ticket();
      assert.equal(f.connections.length, 2);
      assert.equal(f.runtime.status.connected, true);
    },
  );

test('invalid refreshed ticket still fails closed on a healthy connection', async (t) => {
  const f = await fixture(t);
  await f.runtime.start();
  f.prepared.getTicket = async () => {
    throw new Error('invalid device network ticket');
  };
  await assert.rejects(f.connections[1].ticket(), /invalid/);
  assert.equal(f.runtime.status.available, false);
});

test('healthy refresh retries a control fetch failure but rejects trust validation failure', async (t) => {
  const f = await fixture(t);
  await f.runtime.start();
  const original = f.prepared.refresh;
  f.prepared.refresh = async () => {
    throw new DeviceControlUnavailable('network control unavailable');
  };
  await assert.rejects(f.connections[1].ticket(), DeviceControlUnavailable);
  f.connections[1].refreshError(new DeviceControlUnavailable());
  assert.equal(f.runtime.status.connected, true);
  f.prepared.refresh = original;
  await f.connections[1].ticket();
  f.prepared.refresh = async () => {
    throw new TypeError('invalid keyset');
  };
  await assert.rejects(f.connections[1].ticket(), /invalid keyset/);
  assert.equal(f.runtime.status.available, false);
});

test('transient refresh never preserves connected past a newer announcement without its full keyset', async (t) => {
  const f = await fixture(t);
  await f.runtime.start();
  const gate = latch();
  let entered = false;
  f.prepared.refresh = async (trust) => {
    await trust.announce(
      signedAnnouncement({ ...f.payload, keyset_version: '6' }),
    );
    entered = true;
    await gate.promise;
    throw new DeviceControlUnavailable('network control unavailable');
  };
  const refresh = assert.rejects(
    f.connections[1].ticket(),
    DeviceControlUnavailable,
  );
  await waitFor(() => entered, 'new announcement fence');
  assert.equal(f.runtime.status.connected, false);
  gate.release();
  await refresh;
  assert.equal(f.runtime.status.available, false);
  assert.equal(f.runtime.status.reason, 'key_unavailable');
});
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
  assert.equal(f.logs.filter((event) => event.event === 'failure').length, 0);
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

test('first socket failure retries under the original registration and writer lock', async (t) => {
  const f = await fixture(t, {
    start(id) {
      if (id === 1)
        throw new TransportError('connection_closed', 'connection closed');
    },
  });
  await f.runtime.start();
  assert.equal(f.runtime.status.connected, true);
  assert.equal(f.events.filter((x) => x === 'register').length, 1);
  assert.equal(f.events.filter((x) => x === 'commit').length, 1);
  assert.equal(f.connections.length, 3);
  assert.ok(f.events.indexOf('stop-1') < f.events.indexOf('ready-2'));
});

test('first keys or ticket fetch outage does not abandon the original empty-ledger registration', async (t) => {
  for (const phase of ['refresh', 'ticket']) {
    let calls = 0;
    const f = await fixture(t, {
      [phase]() {
        if (++calls === 1)
          throw new DeviceControlUnavailable('temporary control failure');
      },
    });
    await f.runtime.start();
    assert.equal(f.runtime.status.connected, true);
    assert.equal(f.events.filter((x) => x === 'register').length, 1);
    assert.equal(f.events.filter((x) => x === 'commit').length, 1);
    await f.runtime.close();
  }
});

test('C1 socket loss before commit retries a fresh nonce without replaying initialize authority', async (t) => {
  const f = await fixture(t, {
    ready(id) {
      if (id === 1)
        throw new TransportError('connection_lost', 'connection lost');
    },
  });
  await f.runtime.start();
  assert.equal(f.runtime.status.connected, true);
  assert.equal(f.events.filter((x) => x === 'register').length, 1);
  assert.equal(f.events.filter((x) => x === 'commit').length, 1);
  assert.equal(new Set(f.sent.map((s) => s.request.channel_nonce)).size, 3);
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
  assert.equal(f.logs.length, 1);
  assert.equal(f.logs[0].phase, 'ledger_restore');
  assert.equal(f.logs[0].code, 'ledger_missing');
  assert.equal(f.logs[0].created, false);
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
  assert.deepEqual(
    second.logs.map((e) => e.event),
    ['ledger_restored', 'ready'],
  );
  assert.equal(second.logs[0].created, false);
});

for (const [stage, extra, code] of [
  [
    'register_http',
    {
      register: () => {
        throw Object.assign(new Error('SECRET-marker'), {
          status: 503,
          code: 'SECRET-marker',
          headers: { secret: 'SECRET-marker' },
        });
      },
    },
    'http_error',
  ],
  [
    'trust_load',
    {
      trust: (() => {
        const store = createMemoryLedgerStore();
        store.restore({ invalid: true });
        return store;
      })(),
    },
    'network.key_unavailable',
  ],
  [
    'trust_refresh',
    {
      refresh: () => {
        throw Object.assign(new Error('SECRET-marker'), {
          code: 'network.key_unavailable',
        });
      },
    },
    'network.key_unavailable',
  ],
  [
    'ticket_http',
    {
      ticket: () => {
        throw Object.assign(new Error('SECRET-marker'), { status: 403 });
      },
    },
    'http_error',
  ],
  ['wss_connect', { factoryError: true }, 'unknown'],
  [
    'bootstrap_ready',
    {
      ready: (id) => {
        if (id === 1)
          throw Object.assign(new Error('SECRET-marker'), {
            code: 'not_connected',
          });
      },
    },
    'not_connected',
  ],
  [
    'normal_ready',
    {
      ready: (id) => {
        if (id === 2)
          throw Object.assign(new Error('SECRET-marker'), {
            code: 'not_connected',
          });
      },
    },
    'not_connected',
  ],
  [
    'initial_commit',
    {
      commit: () => {
        throw Object.assign(new Error('SECRET-marker'), { code: 'ENOSPC' });
      },
    },
    'unknown',
  ],
])
  test('安全日志保留首失阶段 ' + stage, async (t) => {
    const f = await fixture(t, extra);
    await assert.rejects(f.runtime.start());
    await f.runtime.close();
    const failures = f.logs.filter((e) => e.event === 'failure');
    assert.equal(failures.length, 1);
    assert.equal(failures[0].phase, stage);
    assert.equal(failures[0].code, code);
    assert.equal(JSON.stringify(f.logs).includes('SECRET-marker'), false);
    assert.equal(f.runtime.status.connected, false);
    if (stage === 'initial_commit') assert.equal(failures[0].errno, 'ENOSPC');
  });

test('安全日志生产NatsTransport重试循环保留首个WSS错误，无需真实网络', async (t) => {
  let calls = 0;
  const f = await fixture(t, {
    transportFactory: (options) =>
      new NatsTransport({
        ...options,
        backoff: { baseMs: 1, maxMs: 1, stableMs: 10000 },
        nats: {
          tokenAuthenticator: () => () => ({ auth_token: 'SECRET-token' }),
          async wsconnect() {
            calls++;
            throw new TransportError('socket_incapable', 'SECRET-url', {
              cause: new Error('SECRET-cause'),
            });
          },
        },
      }),
  });
  const started = f.runtime.start().catch((error) => error);
  for (let i = 0; i < 100 && calls < 3; i++)
    await new Promise((resolve) => setTimeout(resolve, 2));
  assert.ok(calls >= 3);
  await f.runtime.close();
  await started;
  const failures = f.logs.filter((e) => e.event === 'failure');
  assert.equal(failures.length, 1);
  assert.equal(failures[0].phase, 'wss_connect');
  assert.equal(failures[0].code, 'socket_incapable');
  assert.equal(JSON.stringify(f.logs).includes('SECRET'), false);
});

for (const errno of [
  'EAI_AGAIN',
  'ERR_TLS_CERT_ALTNAME_INVALID',
  'SECRET-code',
])
  test('安全日志在SDK丢弃原错误前保留握手errno ' + errno, async (t) => {
    const sockets = [];
    const f = await fixture(t, {
      transportFactory: (options) =>
        new NatsTransport({
          ...options,
          backoff: { baseMs: 30000, maxMs: 30000, stableMs: 10000 },
          createSocket() {
            const listeners = new Map();
            const socket = {
              bufferedAmount: 0,
              send() {},
              close() {},
              terminate() {},
              addEventListener(type, listener) {
                if (!listeners.has(type)) listeners.set(type, new Set());
                listeners.get(type).add(listener);
              },
              removeEventListener(type, listener) {
                listeners.get(type)?.delete(listener);
              },
              error() {
                for (const listener of listeners.get('error'))
                  listener({
                    error: {
                      code: errno,
                      message: 'SECRET-message',
                      cause: 'SECRET-cause',
                    },
                    target: 'SECRET-url',
                  });
              },
              get errorListeners() {
                return listeners.get('error')?.size ?? 0;
              },
            };
            sockets.push(socket);
            return socket;
          },
          nats: {
            tokenAuthenticator: () => () => ({ auth_token: 'SECRET-token' }),
            async wsconnect(input) {
              await input.wsFactory('wss://example.invalid/');
              assert.equal(sockets[0].errorListeners, 2);
              sockets[0].error();
              // Same loss as the locked SDK: new generic error has no errno/cause.
              throw new Error('SECRET-message');
            },
          },
        }),
    });
    const started = f.runtime.start().catch((error) => error);
    await waitFor(
      () => f.logs.some((event) => event.event === 'failure'),
      'safe WSS failure',
    );
    assert.equal(
      sockets[0].errorListeners,
      1,
      'temporary diagnostic listener removed, original guard retained',
    );
    const before = JSON.stringify(f.logs);
    sockets[0].error();
    assert.equal(
      JSON.stringify(f.logs),
      before,
      'late socket error has no diagnostic observer',
    );
    await f.runtime.close();
    await started;
    const event = f.logs.find((e) => e.event === 'failure');
    assert.equal(event.phase, 'wss_connect');
    assert.equal(event.code, 'connection_lost');
    assert.equal(event.errno, errno === 'SECRET-code' ? 'none' : errno);
    assert.equal(JSON.stringify(f.logs).includes('SECRET'), false);
  });

test('安全日志trust缓存写失败不能误报初始业务账本提交', async (t) => {
  const f = await fixture(t, {
    trust: createMemoryLedgerStore({
      beforeCommit() {
        throw Object.assign(new Error('SECRET-path'), { code: 'EACCES' });
      },
    }),
  });
  await assert.rejects(f.runtime.start());
  await f.runtime.close();
  assert.equal(f.logs[0].phase, 'trust_refresh');
  assert.equal(f.logs[0].errno, 'EACCES');
  assert.equal(f.logs.length, 1);
});

test('安全日志将真实文件双写锁归类并保持持有者账本不变', async (t) => {
  const dir = await mkdtemp(join(tmpdir(), 'auriga-log-lock-'));
  const store = createFileLedgerStore({ dir });
  const holder = await store.open();
  try {
    const f = await fixture(t, { ledger: store });
    await assert.rejects(f.runtime.start(), /locked/);
    await f.runtime.close();
    assert.equal(f.logs[0].phase, 'ledger_open');
    assert.equal(f.logs[0].code, 'ledger_locked');
    assert.equal(f.logs[0].created, null);
    assert.deepEqual(f.events, []);
    assert.equal(await holder.load(), undefined);
  } finally {
    await holder.close();
    await nodeFs.rm(dir, { recursive: true, force: true });
  }
});

test('安全日志捕获重连中的首个WSS失败，重复和已退役连接不覆盖', async (t) => {
  const f = await fixture(t);
  await f.runtime.start();
  f.connections[0].refreshError({ code: 'SECRET-marker' });
  assert.equal(f.logs.filter((e) => e.event === 'failure').length, 0);
  for (let i = 0; i < 100; i++)
    f.connections[1].refreshError({
      code: 'unauthorized',
      message: 'SECRET-marker',
      cause: { secret: 'SECRET-marker' },
    });
  assert.equal(f.logs.filter((e) => e.event === 'failure').length, 1);
  const event = f.logs.find((e) => e.event === 'failure');
  assert.equal(event.phase, 'wss_connect');
  assert.equal(event.code, 'unauthorized');
  assert.equal(event.budget_ms, 30000);
  await f.runtime.close();
  f.connections[1].refreshError({ code: 'SECRET-marker' });
  assert.equal(f.logs.filter((e) => e.event === 'failure').length, 1);
  assert.equal(JSON.stringify(f.logs).includes('SECRET-marker'), false);
});

test('real NATS WSS with official device factory closes C1 before T2, binds new epochs and retries transient refresh without teardown', async (t) => {
  const binary = await ensureNatsServer();
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
  let ticketCount = 0,
    refreshFailures = 0,
    refreshMode = false;
  const refreshGate = latch();
  t.after(() => refreshGate.release());
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
    if (refreshMode) {
      if (refreshFailures++ === 0) return Response.json({}, { status: 503 });
      if (refreshFailures === 2)
        throw new TypeError('fixture temporary fetch failure');
      await refreshGate.promise;
    }
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
      expiresAt: new Date(Date.now() + 1000).toISOString(),
      connectionExpiresAt: new Date(Date.now() + 2000).toISOString(),
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
        refreshLeadMs: [500, 500],
        backoff: { baseMs: 20, maxMs: 40, stableMs: 10000 },
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
  refreshMode = true;
  const oldSocket = sockets[1];
  await waitFor(
    () => refreshFailures >= 2,
    'production transport refresh retries',
  );
  assert.equal(runtime.status.connected, true);
  assert.equal(runtime.status.available, true);
  assert.equal(ticketCount, 2);
  assert.equal(oldSocket.readyState, 1);
  refreshGate.release();
  await waitFor(
    () => transports[1].epoch === 2 && runtime.status.connected,
    'strict READY after refresh retry',
  );
  assert.equal(ticketCount, 3);
  assert.equal(frames.length, 3);
  assert.equal(ledger.snapshot().revision, 0);
  assert.equal(
    transports.length,
    2,
    'runtime kept its normal transport and ledger',
  );
  await runtime.close();
  assert.ok(sockets.every((socket) => socket.readyState === 3));
});

test('plugin actual entry resumes verified native device into official factory, durable C1/C2 connection and logout', async (t) => {
  const root = fileURLToPath(
    new URL('../../../../.agent-evidence/', import.meta.url),
  );
  await mkdir(root, { recursive: true });
  const profile = await mkdtemp(join(root, 'plugin-entry-'));
  const server = await startNatsServer({ binary: await ensureNatsServer() });
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
  const deviceDirectories = await nodeFs.readdir(
    join(profile, 'auriga-connect/network/devices'),
  );
  assert.equal(deviceDirectories.length, 1);
  const hostDirectory = join(
    profile,
    'auriga-connect/network/devices',
    deviceDirectories[0],
    'host',
  );
  const ledger = JSON.parse(
    await readFile(join(hostDirectory, 'ledger.json'), 'utf8'),
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
      await readFile(join(hostDirectory, 'ledger.lock'));
      return false;
    } catch (e) {
      return e.code === 'ENOENT';
    }
  }, 'logout releases host writer');
  assert.equal(ticketCount, 2);
  assert.equal(hostCalls, 0);
});
