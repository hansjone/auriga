import test from 'node:test';
import assert from 'node:assert/strict';
import { setTimeout as delay } from 'node:timers/promises';
import { fixture as nativeFixture } from './host/native/fixtures.js';
import { IDENTITY } from './host/fixtures.js';
import { signingFixture } from './trust/fixtures.js';
import { openLedger } from '../../src/network/host/ledger.js';
import { createMemoryLedgerStore } from '../../src/network/host/store.js';
import {
  connectionRegistrar,
  createSessionRegistration,
} from '../../src/network/session-registration.js';
import { decodeJson, encodeJson } from '../../src/network/session/frames.js';

// Native and remote-signing fixtures around the production tool, allocator and
// registrar. These are not real-host or remote platform acceptance evidence.
async function fixture(t, options = {}) {
  const native = nativeFixture();
  const agent = native.attach('session-a');
  const signer = await signingFixture();
  const connection = new AbortController();
  const execution = new AbortController();
  const store = createMemoryLedgerStore();
  const ledger = await openLedger({
    store,
    identity: IDENTITY,
    initialize: true,
  });
  const requests = [],
    reservations = [],
    starts = [],
    tools = new Map();
  let listener,
    current = true,
    activeListeners = 0,
    registered = 0;
  let firstSend;
  const sent = new Promise((resolve) => {
    firstSend = resolve;
  });
  native.ctx.tools = {
    register(tool) {
      tools.set(tool.name, tool);
      return () => tools.delete(tool.name);
    },
  };
  const assertCurrent = () => {
    if (!current) throw new Error('retired');
  };
  const registerLocal = connectionRegistrar({
    transport: {
      onMessage(handler) {
        listener = handler;
        activeListeners++;
        return () => {
          activeListeners--;
          listener = undefined;
        };
      },
      async send(bytes) {
        const request = decodeJson(bytes);
        requests.push(request);
        starts.push(Date.now());
        reservations.push(store.snapshot().locals['1']);
        firstSend();
        if (options.silent && requests.length > 1) return;
        const overloaded = requests.length === 1 || options.always;
        let frame = overloaded
          ? {
              app_version: '1.0',
              type: 'error',
              request_id: request.request_id,
              code: 'network.overloaded',
              scope: 'request',
              retryable: true,
              retry_after: options.retryAfter ?? null,
              related: {},
            }
          : {
              app_version: '1.0',
              type: 'register_result',
              request_id: request.request_id,
              local: request.candidate_local,
              allocation_op_id: request.allocation_op_id,
              address_type: request.address_type,
              registration_id: 'registration-1',
              result: 'registered',
              registration_revision: '1',
              local_high_water: '1',
            };
        frame = options.change?.(frame) ?? frame;
        const context = { ...signer.context, requestId: request.request_id };
        options.context?.(context);
        frame = signer.seal(frame, context);
        if (options.badSignature) frame.server_auth.signature = 'A'.repeat(86);
        listener(encodeJson(frame));
      },
    },
    trust: signer.trust,
    identity: IDENTITY,
    ready: {
      epoch: '7',
      channelNonce: signer.context.channelNonce,
      bootId: 'boot',
    },
    signal: connection.signal,
    assertCurrent,
    now: signer.now,
  });
  const capability = createSessionRegistration({
    ctx: native.ctx,
    ledger,
    registerLocal,
    assertCurrent,
    async onRegistered() {
      registered++;
    },
  });
  await capability.start();
  t.after(async () => {
    capability.close();
    connection.abort();
    await ledger.close();
  });
  return {
    ...native,
    agent,
    store,
    requests,
    reservations,
    starts,
    tools,
    sent,
    connection,
    execution,
    get activeListeners() {
      return activeListeners;
    },
    get registered() {
      return registered;
    },
    retire() {
      current = false;
      capability.close();
    },
    join() {
      return tools
        .get('auriga_network_join')
        .execute({}, { agent, signal: execution.signal });
    },
  };
}

test('production join retries a signed overload within one tool call and binds the exact original reservation', async (t) => {
  const f = await fixture(t);
  assert.deepEqual(await f.join(), {
    status: 'registered',
    address: 'aur1:2.40.1',
  });
  assert.equal(f.requests.length, 2);
  assert.deepEqual(f.requests[1], f.requests[0]);
  assert.deepEqual(f.reservations[1], f.reservations[0]);
  assert.equal(f.reservations[0].state, 'reserved');
  assert.ok(f.starts[1] - f.starts[0] >= 90);
  assert.equal(f.store.snapshot().localHighWater, '1');
  assert.equal(f.store.snapshot().locals['1'].state, 'registered');
  assert.equal(f.store.snapshot().bindings['1'].hostSessionId, 'session-a');
  assert.equal(f.registered, 1);
  assert.equal(f.activeListeners, 0);
});

test('persistent signed overload is bounded and exposes its verified cause without consuming another local', async (t) => {
  const f = await fixture(t, { always: true });
  assert.deepEqual(await f.join(), {
    status: 'unavailable',
    reason: 'network.overloaded',
    retryable: true,
    retryAfter: null,
    attempts: 5,
  });
  assert.equal(f.requests.length, 5);
  for (let i = 1; i < 5; i++) {
    assert.deepEqual(f.requests[i], f.requests[0]);
    assert.deepEqual(f.reservations[i], f.reservations[0]);
  }
  assert.deepEqual(f.store.snapshot().locals['1'], f.reservations[0]);
  assert.deepEqual(f.store.snapshot().bindings, {});
  assert.equal(f.store.snapshot().localHighWater, '1');
  assert.equal(f.registered, 0);
  assert.equal(f.activeListeners, 0);
});

test('signed retry-after is honored; delay beyond the original five-second budget fails without resending', async (t) => {
  const f = await fixture(t, { retryAfter: 1 });
  assert.equal((await f.join()).status, 'registered');
  assert.ok(f.starts[1] - f.starts[0] >= 990);
  const bounded = await fixture(t, { retryAfter: 6 });
  assert.deepEqual(await bounded.join(), {
    status: 'unavailable',
    reason: 'network.overloaded',
    retryable: true,
    retryAfter: 6,
    attempts: 1,
  });
  assert.equal(bounded.requests.length, 1);
  assert.equal(bounded.activeListeners, 0);
});

test('bad signatures, signed wrong audiences and nonretryable responses never authorize a second send', async (t) => {
  for (const options of [
    { badSignature: true },
    {
      context: (context) => {
        context.channelNonce = 'other';
      },
    },
    { change: (frame) => ({ ...frame, scope: 'connection' }) },
    { change: (frame) => ({ ...frame, code: 'network.outcome_unknown' }) },
    { change: (frame) => ({ ...frame, retry_after: 0 }) },
  ]) {
    const f = await fixture(t, options);
    assert.deepEqual(await f.join(), { status: 'unavailable' });
    assert.equal(f.requests.length, 1);
    assert.equal(f.activeListeners, 0);
    assert.deepEqual(f.store.snapshot().bindings, {});
  }
  const denied = await fixture(t, {
    change: (frame) => ({ ...frame, retryable: false }),
  });
  assert.deepEqual(await denied.join(), {
    status: 'unavailable',
    reason: 'network.overloaded',
    retryable: false,
    retryAfter: null,
    attempts: 1,
  });
  assert.equal(denied.requests.length, 1);
});

test('execution abort, connection abort, native retirement and generation replacement stop backoff without binding', async (t) => {
  for (const stop of [
    (f) => f.execution.abort(),
    (f) => f.connection.abort(),
    (f) => f.archivedSessionIds.push('session-a'),
    (f) => f.retire(),
  ]) {
    const f = await fixture(t);
    const pending = f.join();
    await f.sent;
    await delay(20);
    stop(f);
    assert.deepEqual(await pending, { status: 'unavailable' });
    assert.equal(f.requests.length, 1);
    assert.deepEqual(f.store.snapshot().locals['1'], f.reservations[0]);
    assert.deepEqual(f.store.snapshot().bindings, {});
    assert.equal(f.activeListeners, 0);
    assert.equal(f.registered, 0);
  }
});

test('a silent retry cannot reset the original five-second response deadline or leave a listener', async (t) => {
  const f = await fixture(t, { silent: true, retryAfter: 1 });
  const start = Date.now();
  assert.deepEqual(await f.join(), {
    status: 'unavailable',
    reason: 'network.overloaded',
    retryable: true,
    retryAfter: 1,
    attempts: 2,
  });
  assert.ok(Date.now() - start < 5500);
  assert.equal(f.requests.length, 2);
  assert.equal(f.activeListeners, 0);
  assert.deepEqual(f.store.snapshot().bindings, {});
});
