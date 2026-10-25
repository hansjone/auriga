import { test } from 'node:test';
import assert from 'node:assert/strict';
import { setImmediate } from 'node:timers';
import {
  openNetworkRuntime,
  ticketCredentials,
} from '../../src/network/runtime.js';
import { createMemoryLedgerStore } from '../../src/network/host/store.js';
import {
  IDENTITY,
  createFakeHost,
  createFixturePlatform,
  peer,
} from './host/fixtures.js';
import { signingFixture } from './trust/fixtures.js';
import { signedKeyset, testRoots } from './trust/keyset-fixtures.js';
import { encodeJson, decodeJson } from '../../src/network/session/frames.js';
import { digest } from '../../src/network/trust/verifier.js';

async function fixture(t, overrides = {}, runtimeOptions = {}) {
  const signer = runtimeOptions.signer ?? (await signingFixture());
  const host = createFakeHost();
  host.addSession('native-session');
  const tools = new Map();
  const calls = [],
    frames = [];
  let message, state, nonce;
  let keyset;
  let transportOptions;
  const storage =
    runtimeOptions.storage ??
    Object.fromEntries(
      ['ledger', 'consent', 'trust', 'outbound', 'intake'].map((name) => [
        name,
        createMemoryLedgerStore(),
      ]),
    );
  const ports = {
    host,
    network: createFixturePlatform().network,
    async prepareTicket(input) {
      calls.push(input);
      return {
        tenant: IDENTITY.tenant,
        node: '64',
        device: 'device',
        ticket: 'a'.repeat(43),
        expiresAtMs: Date.now() + 3600000,
      };
    },
    async resolveSource(source) {
      return { ...source, userId: 'alice', ownershipRevision: '1' };
    },
    async organization() {
      return {
        available: true,
        revision: '1',
        sameOrganization: true,
        conversation: 'ask_each',
        execution: 'ask_each',
      };
    },
    async organizationPolicy() {
      return {
        available: true,
        revision: '1',
        conversation: 'ask_each',
        execution: 'ask_each',
      };
    },
    async sourceFor({ address }) {
      return {
        address,
        sourceBinding: 'binding-native',
        producerEpoch: '1',
        senderKind: 'agent',
        sendAllowed: true,
        firstContactAllowed: true,
        expiresAt: Date.now() + 60000,
      };
    },
    async resolveTarget() {
      return peer('418');
    },
    async releaseResult() {},
    async submit(request) {
      calls.push(request);
      return { status: 'platform_stored', internal: 'secret' };
    },
    async publishConsent() {},
    ...overrides,
  };
  keyset = signedKeyset(signer.config);
  const runtime = await openNetworkRuntime({
    prepared: {
      identity: IDENTITY,
      url: 'wss://network.example.com/',
      roots: testRoots,
      keyset,
      initialize: true,
      ...runtimeOptions.prepared,
    },
    ports,
    getAccess: async () => 'native-access',
    authorizeLocal: async (context) => context === 'authenticated',
    ctx: {
      logger: runtimeOptions.logger,
      tools: {
        register(tool) {
          tools.set(tool.name, tool);
          return () => tools.delete(tool.name);
        },
      },
    },
    now: signer.now,
    onClosed: runtimeOptions.onClosed,
    stores: {
      ledger: storage.ledger,
      consent: storage.consent,
      trust: await storage.trust.open(),
      outbound: await storage.outbound.open(),
      intake: await storage.intake.open(),
    },
    consentTiming: {
      responseDelayMs: 20,
      callbackTimeoutMs: 5,
      actionTimeoutMs: 10,
    },
    transportFactory(options) {
      transportOptions = options;
      message = options.onMessage;
      state = options.onState;
      return {
        epoch: 1,
        state: 'ready',
        async start() {
          await options.getTicket({ reason: 'start', attempt: 0 });
          state({ state: 'ready', epoch: 1 });
        },
        async stop() {
          state({ state: 'stopped', epoch: 1 });
        },
        async send(bytes) {
          const frame = decodeJson(bytes);
          frames.push(frame);
          let response;
          if (frame.type === 'ready') {
            nonce = frame.channel_nonce;
            response = {
              app_version: '1.0',
              type: 'ready_result',
              status: 'ready',
              node_identity: {
                realm: '2',
                node: '64',
                incarnation: IDENTITY.nodeIncarnation,
              },
              required_features: [],
              epoch: '7',
              request_id: frame.request_id,
              committed_cursor: '0',
              head_seq: '0',
            };
          }
          if (frame.type === 'fetch') {
            const available = (overrides.deliveries?.(signer) ?? []).filter(
              (item) =>
                BigInt(item.mail_seq) >
                BigInt(frame.continuation?.scan_after ?? frame.resume_cursor),
            );
            const items = available.slice(0, frame.max_items ?? 16);
            const tip = items.at(-1)?.mail_seq ?? frame.resume_cursor;
            response = {
              app_version: '1.0',
              type: 'fetch_result',
              page: {
                app_version: '1.0',
                type: 'fetch_page',
                request_id: frame.request_id,
                epoch: '7',
                head_seq: tip,
                committed_cursor: frame.resume_cursor,
                scan_after: tip,
                snapshot_head: available.at(-1)?.mail_seq ?? tip,
                next_continuation: null,
                manifest: [],
              },
              items,
            };
          }
          if (frame.type === 'attempt_begin')
            response = {
              app_version: '1.0',
              type: 'attempt_result',
              request_id: frame.request_id,
              attempt_id: frame.attempt_id,
              recipient_id: frame.recipient_id,
              result: 'entered',
              attempt_count: 1,
              max_attempts: 5,
            };
          if (frame.type === 'receipt')
            response = {
              app_version: '1.0',
              type: 'receipt_committed',
              request_id: frame.request_id,
              epoch: '7',
              committed_cursor: frame.mail_seq,
              results: frame.results,
            };
          if (!response) throw new Error('unexpected frame');
          const context = {
            ...signer.context,
            requestId: frame.request_id,
            channelNonce: nonce,
          };
          if (response.type === 'fetch_result') {
            for (const item of response.items) signer.seal(item, context);
            response.page.manifest = response.items.map((item) => ({
              item_type: item.type,
              mail_seq: item.mail_seq,
              digest: digest(item),
            }));
            signer.seal(response.page, context);
          } else signer.seal(response, context);
          message(encodeJson(response), 1);
        },
      };
    },
  });
  t.after(() => runtime.close());
  return {
    runtime,
    ports,
    tools,
    frames,
    calls,
    signer,
    storage,
    transportOptions: () => transportOptions,
    message: (v) => message(encodeJson(v), 1),
    state: (s) => state(s),
    nonce: () => nonce,
  };
}

test('assembly performs signed READY/FETCH and registers only to/body/thread from native context', async (t) => {
  const { runtime, tools, frames, calls } = await fixture(t);
  await runtime.start();
  assert.equal(runtime.status.connected, true);
  assert.deepEqual(
    frames.map((f) => f.type),
    ['ready', 'fetch'],
  );
  assert.equal(frames[0].receipt_checkpoint.summary_digest, digest({}));
  const tool = tools.get('auriga_send_message');
  assert.deepEqual(Object.keys(tool.parameters.properties).sort(), [
    'body',
    'thread',
    'to',
  ]);
  const result = await tool.execute(
    { to: 'aur1:2.3f.1a2', body: { subtype: 'text', text: 'hello' } },
    { agent: { session: { id: 'native-session' } } },
  );
  assert.deepEqual(result, { status: 'request_submitted' });
  assert.equal(calls.at(-1).source.source_binding, 'binding-native');
  assert.equal(calls[0].accessToken, 'native-access');
  await runtime.close();
  assert.equal(tools.size, 0);
  assert.equal(runtime.status.connected, false);
  await assert.rejects(runtime.session.submit({}), /not ready/);
});

test('consent bridge is authenticated and never projects ownership/internal state', async (t) => {
  const { runtime } = await fixture(t);
  await runtime.start();
  const ui = runtime.consentUI('authenticated');
  await ui.setDefault('conversation', 'deny_all');
  const view = await ui.snapshot();
  assert.equal(view.defaults.conversation, 'deny_all');
  assert.equal(JSON.stringify(view).includes('ownershipRevision'), false);
  await assert.rejects(runtime.consentUI('forged').snapshot(), /authenticated/);
});

test('ticket token and per-ticket subjects are private and rotate with each ticket', () => {
  const a = ticketCredentials({
    tenant: 't',
    node: '64',
    device: 'd',
    ticket: 'a'.repeat(43),
    expiresAtMs: Date.now() + 60000,
  });
  const b = ticketCredentials({
    tenant: 't',
    node: '64',
    device: 'd',
    ticket: 'b'.repeat(43),
    expiresAtMs: Date.now() + 60000,
  });
  assert.deepEqual(Object.keys(JSON.parse(a.auth.token)).sort(), [
    'device',
    'node',
    'tenant',
    'ticket',
  ]);
  assert.match(a.subjects.upSubject, /^auriga\.net\.up\.[a-z2-7]{26}$/);
  assert.notEqual(a.subjects.upSubject, b.subjects.upSubject);
  assert.throws(() => ticketCredentials({ ticket: 'invalid' }), /ticket/);
});

for (const mode of ['local', 'managed']) {
  test(`${mode} can connect without host isolation and reports the development warning once`, async (t) => {
    const previous = process.env.AURIGA_MODE;
    process.env.AURIGA_MODE = mode;
    t.after(() => {
      if (previous === undefined) delete process.env.AURIGA_MODE;
      else process.env.AURIGA_MODE = previous;
    });
    const warnings = [];
    const { runtime, tools, state } = await fixture(
      t,
      {},
      {
        logger: { warn: (message) => warnings.push(message) },
      },
    );
    assert.equal(runtime.status.isolation.isolated, false);
    assert.equal(runtime.status.isolation.required, false);
    await runtime.start();
    assert.equal(runtime.status.connected, true);
    assert.equal(tools.size, 1);
    assert.equal(runtime.status.isolation.state, 'unisolated-development');
    assert.deepEqual(runtime.diagnostics.isolation, runtime.status.isolation);
    assert.equal(warnings.length, 1);
    assert.match(warnings[0], /未隔离（开发阶段）/);
    state({ state: 'ready', epoch: 1 });
    assert.equal(warnings.length, 1);
    await runtime.close();
    assert.equal(runtime.status.isolation.isolated, false);
  });
}

test('invalid signatures never reach consent or host', async (t) => {
  const { runtime, signer, message, nonce } = await fixture(t);
  await runtime.start();
  const head = signer.seal(
    { app_version: '1.0', type: 'head', epoch: '7', head_seq: '1' },
    { ...signer.context, requestId: null, channelNonce: nonce() },
  );
  head.head_seq = '2';
  message(head);
  await new Promise((r) => setImmediate(r));
  assert.equal(
    runtime.session.diagnostics.at(-1).code,
    'network.signature_invalid',
  );
  assert.deepEqual(await runtime.consent.listPending('authenticated'), []);
});

test('signed delivery runs ATTEMPT then consent then host inbox then RECEIPT_COMMITTED', async (t) => {
  const { runtime, frames } = await fixture(t, {
    deliveries: (signer) => [signer.delivery('assembled')],
    organization: async () => ({
      available: true,
      revision: '1',
      sameOrganization: true,
      conversation: 'allow',
      execution: 'allow',
    }),
  });
  await runtime
    .consentUI('authenticated')
    .setDefault('conversation', 'same_org');
  await runtime.start();
  assert.deepEqual(
    frames.map((f) => f.type),
    ['ready', 'fetch', 'attempt_begin', 'receipt'],
  );
  assert.equal(frames.at(-1).results[0].acceptance_mode, 'inbox_only');
  assert.equal(frames.at(-1).results[0].execution_status, 'not_applicable');
  assert.equal(runtime.session.resumeCursor, '10');
  assert.equal(runtime.diagnostics.pending.intake, 0);
});

test('ask-each first contact is retained for local consent and never becomes host acceptance', async (t) => {
  const { runtime, frames } = await fixture(t, {
    deliveries: (signer) => [signer.delivery('pending')],
  });
  await runtime.start();
  assert.equal(
    frames.some((f) => f.type === 'receipt'),
    false,
  );
  const view = await runtime.consentUI('authenticated').snapshot();
  assert.equal(view.pending.length, 1);
  assert.equal(JSON.stringify(view).includes('message_digest'), false);
  assert.equal(runtime.adapter.pendingReceipts().length, 0);
});

test('a revoked device ticket closes the runtime and unregisters its tool', async (t) => {
  let notify;
  const ended = new Promise((resolve) => {
    notify = resolve;
  });
  const { runtime, tools, ports, transportOptions } = await fixture(
    t,
    {},
    { onClosed: notify },
  );
  await runtime.start();
  assert.equal(tools.size, 1);
  ports.prepareTicket = async () => {
    throw Object.assign(new Error('device revoked'), {
      code: 'network.revoked',
    });
  };
  await assert.rejects(
    transportOptions().getTicket({ reason: 'refresh', attempt: 0 }),
    /device revoked/,
  );
  assert.equal(await ended, 'revoked');
  assert.equal(runtime.status.connected, false);
  assert.equal(runtime.status.available, false);
  assert.equal(tools.size, 0);
});

test('a full intake store rejects only that contact with a uniform receipt and remains online', async (t) => {
  const first = await fixture(t);
  await first.runtime.close();
  const now = first.signer.now();
  first.storage.intake.restore(
    Object.fromEntries(
      Array.from({ length: 256 }, (_, index) => {
        const requestId = `retained-${index}`;
        return [
          JSON.stringify(['conversation', requestId]),
          {
            request: {
              layer: 'conversation',
              requestId,
              source: {
                tenant: IDENTITY.tenant,
                realm: '2',
                node: String(1 + Math.floor(index / 8)),
              },
              digest: 'a'.repeat(64),
              createdAt: now - 1000,
              expiresAt: now + 3600000,
              bytes: 100,
            },
            recipientIds: [`old-${index}`],
            preview: 'private',
          },
        ];
      }),
    ),
  );
  const { runtime, frames } = await fixture(
    t,
    { deliveries: (signer) => [signer.delivery('full')] },
    { storage: first.storage, signer: first.signer },
  );
  await runtime.start();
  assert.equal(runtime.status.connected, true);
  assert.equal(runtime.diagnostics.pending.intake, 256);
  assert.equal(runtime.diagnostics.runtime.intake_backpressure, 1);
  assert.equal(
    frames.find((frame) => frame.type === 'receipt').results[0].code,
    'network.target_unavailable',
  );
});

async function until(predicate) {
  const deadline = Date.now() + 10000;
  while (!predicate()) {
    if (Date.now() > deadline) throw new Error('runtime timeout');
    await new Promise((resolve) => setImmediate(resolve));
  }
}
const submission = (key, source = 'native-source') => ({
  kind: 'message',
  to: peer('418'),
  durability: 'durable',
  intent: 'information',
  body: { subtype: 'text', text: 'hello' },
  source: { source_binding: source },
  idempotency: { producer_epoch: '1', key: `send-${key}` },
});

test('257 ordinary seven-day deliveries free metadata after receipts and do not lock the runtime across restart', async (t) => {
  const world = await fixture(t, {
    deliveries: (signer) =>
      Array.from({ length: 257 }, (_, index) => {
        const raw = signer.delivery(`load-${index}`);
        raw.mail_seq = String(index + 1);
        raw.message.kind = 'message';
        raw.message.intent = 'information';
        delete raw.message.contact_scope;
        raw.message.thread_id = 'load-thread';
        raw.message.expires_at = '2026-11-01T00:00:00Z';
        raw.source_context.message_digest = digest(raw.message);
        return raw;
      }),
  });
  await world.runtime.adapter.reserveOutboundRequest({
    requestId: 'request-load',
    local: '0',
    threadId: 'load-thread',
    peer: peer('418'),
    kinds: ['message'],
  });
  await world.runtime.adapter.activateOutboundRequest('request-load');
  await world.runtime.start();
  await until(
    () =>
      !world.runtime.status.connected ||
      world.runtime.session.resumeCursor === '257',
  );
  assert.equal(world.runtime.status.connected, true);
  assert.equal(world.runtime.session.resumeCursor, '257');
  assert.equal(world.runtime.diagnostics.pending.intake, 0);
  assert.equal(
    world.frames.filter((frame) => frame.type === 'receipt').length,
    257,
  );
  await world.runtime.close();
  const resumed = await fixture(
    t,
    {},
    { storage: world.storage, signer: world.signer },
  );
  await resumed.runtime.start();
  assert.equal(resumed.runtime.status.connected, true);
  assert.deepEqual(
    await resumed.runtime.session.submit(submission('after-load')),
    { status: 'platform_stored', internal: 'secret' },
  );
});

test('257 uncertain submission failures apply session backpressure without closing and legacy restart cannot self-lock', async (t) => {
  const world = await fixture(t, {
    submit: async () => {
      throw new Error('lost response');
    },
  });
  await world.runtime.start();
  for (let index = 0; index < 257; index++)
    await assert.rejects(world.runtime.session.submit(submission(index)));
  assert.equal(world.runtime.status.connected, true);
  assert.ok(world.runtime.diagnostics.pending.outbound <= 16);
  // One producer's pending metadata cannot consume another native session's budget.
  world.ports.submit = async () => ({ status: 'platform_stored' });
  assert.deepEqual(
    await world.runtime.session.submit(
      submission('another', 'another-session'),
    ),
    { status: 'platform_stored' },
  );
  await world.runtime.close();
  world.storage.outbound.restore(
    Object.fromEntries(
      Array.from({ length: 256 }, (_, index) => [
        `legacy-${index}`,
        { request: submission(index), status: 'pending' },
      ]),
    ),
  );
  const resumed = await fixture(
    t,
    {},
    { storage: world.storage, signer: world.signer },
  );
  await resumed.runtime.start();
  assert.equal(resumed.runtime.status.connected, true);
  assert.equal(resumed.runtime.diagnostics.pending.outbound, 0);
  await resumed.runtime.session.submit(submission('after-legacy'));
});

test('257 definitive submission rejections release their durable records immediately', async (t) => {
  let submitted = 0;
  const { runtime } = await fixture(t, {
    submit: async () => {
      submitted++;
      throw Object.assign(new Error('rejected'), { status: 422 });
    },
  });
  await runtime.start();
  for (let index = 0; index < 257; index++)
    await assert.rejects(runtime.session.submit(submission(index)), /rejected/);
  assert.equal(submitted, 257);
  assert.equal(runtime.status.connected, true);
  assert.equal(runtime.diagnostics.pending.outbound, 0);
});

test('a real metadata commit failure self-closes and notifies its owner after cleanup', async (t) => {
  let fail = false,
    notify;
  const ended = new Promise((resolve) => {
    notify = resolve;
  });
  const storage = Object.fromEntries(
    ['ledger', 'consent', 'trust', 'outbound', 'intake'].map((name) => [
      name,
      createMemoryLedgerStore({
        beforeCommit() {
          if (name === 'outbound' && fail) throw new Error('disk failure');
        },
      }),
    ]),
  );
  const { runtime, tools } = await fixture(
    t,
    {},
    { storage, onClosed: notify },
  );
  await runtime.start();
  fail = true;
  await assert.rejects(
    runtime.session.submit(submission('disk')),
    /disk failure/,
  );
  assert.equal(await ended, 'storage_failure');
  assert.deepEqual(runtime.status, {
    available: false,
    isolation: {
      required: false,
      isolated: false,
      state: 'unisolated-development',
    },
    connected: false,
    state: 'unavailable',
  });
  assert.equal(tools.size, 0);
});

test('a host ledger commit failure also self-closes before invoking any native prompt', async (t) => {
  let fail = false,
    notify;
  const ended = new Promise((resolve) => {
    notify = resolve;
  });
  const storage = Object.fromEntries(
    ['ledger', 'consent', 'trust', 'outbound', 'intake'].map((name) => [
      name,
      createMemoryLedgerStore({
        beforeCommit() {
          if (name === 'ledger' && fail)
            throw new Error('host storage failure');
        },
      }),
    ]),
  );
  const { runtime, tools, ports } = await fixture(
    t,
    {},
    { storage, onClosed: notify },
  );
  await runtime.start();
  fail = true;
  await tools
    .get('auriga_send_message')
    .execute(
      { to: 'aur1:2.3f.1a2', body: { subtype: 'text', text: 'hello' } },
      { agent: { session: { id: 'native-session' } } },
    );
  assert.equal(await ended, 'storage_failure');
  assert.equal(runtime.status.available, false);
  assert.equal(ports.host.counts.prompt, 0);
  assert.equal(tools.size, 0);
});

test('tools register only after READY, disappear on ticket failures, and reject forged source fields', async (t) => {
  const world = await fixture(t);
  assert.equal(world.tools.size, 0);
  await world.runtime.start();
  const tool = world.tools.get('auriga_send_message');
  const execution = { agent: { session: { id: 'native-session' } } };
  for (const field of [
    'source',
    'from',
    'category',
    'source_binding',
    'sender_kind',
  ]) {
    const result = await tool.execute(
      {
        to: 'aur1:2.3f.1a2',
        body: { subtype: 'text', text: 'hello' },
        [field]: 'forged',
      },
      execution,
    );
    assert.deepEqual(result, {
      status: 'error',
      code: 'network.protocol_error',
    });
  }
  world.state({
    state: 'ready',
    epoch: 1,
    error: new Error('ticket unavailable'),
  });
  assert.equal(world.tools.size, 0);
  assert.equal(
    (await tool.execute({ to: 'aur1:2.3f.1a2' }, execution)).code,
    'network.target_unavailable',
  );
});

test('blocked and denied first contacts emit the same rejection surface and private audits', async (t) => {
  for (const reason of ['blocked', 'denied']) {
    const { runtime, frames, storage } = await fixture(t, {
      deliveries: (signer) => [signer.delivery(reason)],
    });
    if (reason === 'blocked')
      await runtime.adapter.inboundPolicy.blockSource(peer('418'));
    else
      await runtime
        .consentUI('authenticated')
        .setDefault('conversation', 'deny_all');
    await runtime.start();
    const receipt = frames.find((frame) => frame.type === 'receipt').results[0];
    assert.equal(receipt.status, 'rejected');
    assert.equal(receipt.code, 'network.target_unavailable');
    assert.ok(storage.ledger.snapshot().audit.length > 0);
    assert.ok(runtime.diagnostics.runtime[`contact_${reason}`] > 0);
  }
});

test('consent publication contains only this batch and never forwards another source preview or raw frames', async (t) => {
  const published = [];
  const { runtime } = await fixture(t, {
    publishConsent: async (events, metadata) => {
      published.push({ events, metadata });
    },
    deliveries: (signer) =>
      ['one', 'two'].map((id, index) => {
        const raw = signer.delivery(id);
        raw.mail_seq = String(index + 1);
        raw.message.from.node = String(63 - index);
        raw.source_context.node = raw.message.from.node;
        raw.source_context.message_digest = digest(raw.message);
        return raw;
      }),
  });
  await runtime.start();
  const ui = runtime.consentUI('authenticated');
  const snapshot = await ui.snapshot();
  assert.equal(snapshot.pending.length, 2);
  await ui.decide({
    layer: 'conversation',
    requestId: 'msg-one',
    choice: 'deny',
    expectedRevision: snapshot.policyRevision,
  });
  const batch = published.at(-1);
  assert.deepEqual(Object.keys(batch.metadata), [
    JSON.stringify(['conversation', 'msg-one']),
  ]);
  assert.deepEqual(Object.keys(Object.values(batch.metadata)[0]).sort(), [
    'digest',
    'recipientIds',
    'requestId',
  ]);
  assert.equal(JSON.stringify(batch.metadata).includes('msg-two'), false);
  assert.equal(JSON.stringify(batch.metadata).includes('preview'), false);
});

test('blocked contact previews publish a denied decision and audit without a host acceptance receipt', async (t) => {
  const events = [];
  const { runtime, frames } = await fixture(t, {
    publishConsent: async (batch) => events.push(...batch),
    deliveries: (signer) => {
      const raw = signer.delivery('preview');
      return [
        {
          app_version: '1.0',
          type: 'contact_prompt',
          epoch: '7',
          delivery_id: raw.delivery_id,
          mail_seq: raw.mail_seq,
          contact_id: 'contact-preview',
          contact_scope: 'conversation',
          request_version: '1',
          state_version: '1',
          recipient: raw.recipients[0],
          from: raw.message.from,
          source_context: raw.source_context,
          expires_at: raw.message.expires_at,
          preview: 'private preview',
        },
      ];
    },
  });
  await runtime.adapter.inboundPolicy.blockSource(peer('418'));
  await runtime.start();
  assert.ok(
    events.some(
      (event) =>
        event.requestId === 'contact-preview' && event.status === 'denied',
    ),
  );
  assert.ok(runtime.diagnostics.runtime.contact_blocked > 0);
  assert.equal(
    frames.some((frame) => frame.type === 'receipt'),
    false,
  );
  assert.equal(runtime.adapter.pendingReceipts().length, 0);
});

test('uncertain submission reconciliation never exceeds three durable attempts across restarts', async (t) => {
  let world = await fixture(t, {
    submit: async () => {
      throw new Error('response lost');
    },
  });
  await world.runtime.start();
  await assert.rejects(world.runtime.session.submit(submission('reconcile')));
  const storage = world.storage,
    signer = world.signer;
  let reconciled = 0;
  await world.runtime.close();
  for (let attempt = 0; attempt < 4; attempt++) {
    world = await fixture(
      t,
      {
        reconcileSubmission: async () => {
          reconciled++;
          throw new Error('still unknown');
        },
      },
      { storage, signer },
    );
    await world.runtime.start();
    assert.equal(world.runtime.status.connected, true);
    await world.runtime.close();
  }
  assert.equal(reconciled, 3);
  assert.equal(world.runtime.diagnostics.pending.outbound, 0);
});
