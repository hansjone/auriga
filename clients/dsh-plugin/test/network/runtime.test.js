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

async function fixture(t, overrides = {}) {
  const signer = await signingFixture();
  const host = createFakeHost();
  host.addSession('native-session');
  const tools = new Map();
  const calls = [],
    frames = [];
  let message, state, nonce;
  let keyset;
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
    },
    ports,
    getAccess: async () => 'native-access',
    authorizeLocal: async (context) => context === 'authenticated',
    ctx: {
      tools: {
        register(tool) {
          tools.set(tool.name, tool);
          return () => tools.delete(tool.name);
        },
      },
    },
    now: signer.now,
    stores: {
      ledger: createMemoryLedgerStore(),
      consent: createMemoryLedgerStore(),
      trust: await createMemoryLedgerStore().open(),
      outbound: await createMemoryLedgerStore().open(),
      intake: await createMemoryLedgerStore().open(),
    },
    consentTiming: {
      responseDelayMs: 20,
      callbackTimeoutMs: 5,
      actionTimeoutMs: 10,
    },
    transportFactory(options) {
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
            const items = (overrides.deliveries?.(signer) ?? []).filter(
              (item) =>
                BigInt(item.mail_seq) >
                BigInt(frame.continuation?.scan_after ?? frame.resume_cursor),
            );
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
                snapshot_head: tip,
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
  const { runtime, tools } = await fixture(t, {
    async prepareTicket() {
      throw Object.assign(new Error('device revoked'), {
        code: 'network.revoked',
      });
    },
  });
  await assert.rejects(runtime.start(), /device revoked/);
  await runtime.close();
  assert.equal(runtime.status.connected, false);
  assert.equal(tools.size, 0);
});
