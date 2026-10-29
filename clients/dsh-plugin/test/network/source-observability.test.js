/* global structuredClone */
import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, readdir, readFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { setImmediate } from 'node:timers/promises';
import { setup } from './source-control/fixtures.js';
import { createDeviceMessaging } from '../../src/network/durable/capability.js';
import { saveProducerEpoch } from '../../src/network/durable/outbox.js';
import { createAgentSendTool } from '../../src/network/tools/send.js';
import { createSourceControl } from '../../src/network/source-control/controller.js';
import { NatsLink } from '../../src/network/transport/nats-link.js';
import {
  installObservability,
  logDirectory,
  natsTraceparent,
  logEvent,
} from '../../src/observability/index.js';
import { observeTool } from '../../src/observability/source.js';

const input = {
  target: 'aur1:3f.1',
  body: { subtype: 'text', text: 'private-message-canary' },
};
const address = {
  version: 1,
  realm: '2',
  node: '64',
  local: '1',
  type: 'session',
};

// An actual finite file sink, isolated under a temporary fixture directory.
// The second structured sink deliberately throws: neither sink may affect tools.
async function capture(action) {
  const directory = await mkdtemp(join(tmpdir(), 'auriga-source-log-'));
  const env = {
    LOCALAPPDATA: process.env.LOCALAPPDATA,
    XDG_STATE_HOME: process.env.XDG_STATE_HOME,
  };
  process.env.LOCALAPPDATA = directory;
  process.env.XDG_STATE_HOME = directory;
  let dispose;
  installObservability({
    effect(effect, name) {
      if (name !== 'auriga-dsh: structured logs')
        throw new Error('fixture sink failure');
      dispose = effect();
    },
  });
  try {
    const value = await action();
    await dispose();
    const path = logDirectory();
    const text = (
      await Promise.all(
        (await readdir(path))
          .filter((n) => n.endsWith('.jsonl'))
          .map((n) => readFile(join(path, n), 'utf8')),
      )
    ).join('');
    const events = text.trim().split('\n').filter(Boolean).map(JSON.parse);
    assert.ok(events.length);
    for (const e of events) {
      assert.match(e.trace_id, /^[0-9a-f]{32}$/);
      assert.notEqual(e.trace_id, '0'.repeat(32));
      assert.equal(e.service, 'dsh-plugin');
      assert.ok(['INFO', 'WARN', 'ERROR'].includes(e.level));
    }
    assert.ok(!text.includes('private-message-canary'));
    return { value, events, text };
  } finally {
    await dispose?.();
    for (const [key, value] of Object.entries(env)) {
      if (value === undefined) delete process.env[key];
      else process.env[key] = value;
    }
    await rm(directory, { recursive: true, force: true });
  }
}

test('durable source guards retain priority, refresh/ledger reads, result and no submit', async (t) => {
  const f = await setup(t);
  await saveProducerEpoch(f.ledger, '42');
  let tool,
    refreshFailure,
    refreshes = 0,
    submits = 0;
  f.ctx.tools = {
    register(value) {
      tool = value;
      return () => {};
    },
  };
  const messaging = createDeviceMessaging({
    ctx: f.ctx,
    ledger: f.ledger,
    prepared: {
      submit: async () => {
        submits++;
        throw new Error('unused');
      },
    },
    source: {
      refresh: async () => {
        refreshes++;
        if (refreshFailure) throw refreshFailure;
      },
    },
    transport: f.transport,
    ready: f.ready,
    trust: f.trust,
    signal: new AbortController().signal,
    assertCurrent() {},
    now: f.signer.now,
  });
  t.after(async () => {
    messaging.close();
    await messaging.idle();
  });
  await messaging.start();
  await messaging.idle();
  // The fixture alone installs an otherwise valid source entry; no private host state.
  await f.control.establish('1');
  await f.control.idle();
  const baseline = f.store.snapshot();
  for (const reason of [
    'entry_missing',
    'entry_stopped',
    'observation_missing',
    'source_inactive',
    'head_mismatch',
    'observation_expired',
    'session_mismatch',
  ]) {
    await t.test(reason, async () => {
      let changed = false;
      const peek = f.ledger.peek.bind(f.ledger);
      // Read-only structural projection keeps the real ledger/call sequence intact.
      const projected = structuredClone(peek());
      const e = projected.sourceControl.entries['1'];
      if (reason === 'entry_missing')
        delete projected.sourceControl.entries['1'];
      if (reason === 'entry_stopped') {
        e.stopped = true;
        e.observation = null;
      }
      if (reason === 'observation_missing') e.observation = null;
      if (reason === 'source_inactive') e.observation.source.state = 'revoked';
      if (reason === 'head_mismatch') e.head = 'secret-source-binding-canary';
      if (reason === 'observation_expired')
        e.observation.valid_until = '2000-01-01T00:00:00Z';
      if (reason === 'session_mismatch')
        projected.bindings['1'].hostSessionId = 'other-fixture';
      let reads = 0;
      const unmock = t.mock.method(f.ledger, 'peek', () => {
        reads++;
        // sourceAddress uses the first three reads; sourceFor reads only once.
        if (reads === 4) {
          changed = true;
          return projected;
        }
        return peek();
      });
      try {
        const { value, events, text } = await capture(() =>
          tool.execute(input, { agent: f.agent }),
        );
        assert.deepEqual(value, {
          status: 'error',
          code: 'network.unauthorized',
        });
        assert.equal(changed, true);
        assert.equal(reads, 4);
        const rejected = events.find(
          (e) => e.message === 'Source guard rejected',
        );
        assert.equal(rejected.reason, reason);
        assert.equal(rejected.stage, 'source_observation');
        assert.equal(rejected.authorization_round, 'initial');
        assert.ok(!events.some((e) => e.message === 'Source RPC started'));
        assert.ok(!text.includes('secret-source-binding-canary'));
      } finally {
        unmock.mock.restore();
      }
    });
  }
  refreshFailure = new Error('refresh fixture failed', {
    cause: new Error('visible-refresh-cause token=refresh-secret-canary'),
  });
  const refreshRejected = await capture(() =>
    tool.execute(input, { agent: f.agent }),
  );
  assert.deepEqual(refreshRejected.value, {
    status: 'error',
    code: 'network.unauthorized',
  });
  const logged = refreshRejected.events.find(
    (e) => e.message === 'Source authorization rejected',
  );
  assert.equal(logged.stage, 'source_refresh');
  assert.ok(logged.cause.includes('visible-refresh-cause'));
  assert.ok(!refreshRejected.text.includes('refresh-secret-canary'));
  assert.equal(refreshRejected.events.at(-1).outbox, 'not_started');
  assert.equal(refreshes, 8);
  assert.equal(submits, 0);
  assert.deepEqual(f.store.snapshot(), baseline);
});

test('initial/recheck authorization stays in one attempt, rejected recheck never submits', async (t) => {
  const f = await setup(t);
  const calls = [];
  const auth = {
    sourceBinding: 'source-binding-canary',
    producerEpoch: '1',
    senderKind: 'agent',
    address,
    sendAllowed: true,
    expiresAt: f.signer.now() + 60000,
  };
  let count = 0;
  const tool = createAgentSendTool({
    ledger: f.ledger,
    localAddress: () => address,
    sourceFor() {
      calls.push('authorize');
      return { ...auth, sendAllowed: ++count !== 2 };
    },
    resolveTarget(target) {
      calls.push('target');
      return { version: 1, ...target, type: 'session' };
    },
    releaseResult: async () => {
      calls.push('release');
    },
    rateLimit: { messages: 10, windowMs: 60000 },
    now: f.signer.now,
  });
  const { events, value, text } = await capture(() =>
    tool.execute(input, { agent: f.agent }, () => calls.push('submit')),
  );
  assert.deepEqual(value, { status: 'error', code: 'network.unauthorized' });
  assert.deepEqual(calls, ['authorize', 'target', 'authorize', 'release']);
  const passes = events.filter(
    (e) => e.message === 'Source authorization started',
  );
  assert.deepEqual(
    passes.map((e) => e.authorization_round),
    ['initial', 'recheck'],
  );
  assert.equal(new Set(passes.map((e) => e.attempt_id)).size, 1);
  assert.equal(
    events.find((e) => e.reason === 'send_not_allowed').authorization_round,
    'recheck',
  );
  assert.ok(!text.includes('source-binding-canary'));
  const next = await capture(() =>
    tool.execute(input, { agent: f.agent }, () => ({
      status: 'platform_stored',
    })),
  );
  assert.deepEqual(next.value, { status: 'request_submitted' });
  assert.notEqual(next.events[0].attempt_id, events[0].attempt_id);
});

test('swallowed control failure resolves refresh, has complete cause, and actual wait link without RPC', async () => {
  const root = new Error(
    'root token=secret-token-canary source_binding=secret-binding-canary at source-fixture.js:19',
  );
  const failure = new Error('middle ticket=secret-ticket-canary', {
    cause: root,
  });
  const outer = new Error('source fixture storage unavailable', {
    cause: failure,
  });
  let rpc = 0;
  const control = createSourceControl({
    ledger: {
      assertAvailable() {},
      peek() {
        throw outer;
      },
    },
    getContext: () => null,
    ready: { epoch: '1', bootId: 'fixture' },
    request: async () => {
      rpc++;
    },
    assertCurrent() {},
    now: () => 1000,
  });
  try {
    // pump itself reads the ledger before launch, so use a missing-entry drive instead.
    const state = {
      sourceControl: { entries: {}, operations: {}, revocations: {} },
    };
    let reads = 0;
    control.close();
    const local = createSourceControl({
      ledger: {
        assertAvailable() {},
        peek() {
          if (++reads === 2) throw outer;
          return state;
        },
      },
      getContext: () => null,
      ready: { epoch: '1', bootId: 'fixture' },
      request: async () => {
        rpc++;
      },
      assertCurrent() {},
      now: () => 1000,
    });
    try {
      const { value, events, text } = await capture(() =>
        observeTool(() => local.refresh('1')),
      );
      assert.equal(value, undefined);
      assert.equal(rpc, 0);
      const failed = events.find((e) => e.message === 'Source control failed');
      assert.equal(failed.stage, 'drive');
      assert.equal(failed.direct_rpc_count, 0);
      assert.ok(
        failed.exception.includes('source fixture storage unavailable'),
      );
      assert.ok(failed.cause.includes('middle'));
      assert.ok(failed.cause.includes('source-fixture.js:19'));
      for (const secret of [
        'secret-token-canary',
        'secret-binding-canary',
        'secret-ticket-canary',
      ])
        assert.ok(!text.includes(secret));
      const link = events.find((e) => e.relation === 'waited_for');
      assert.equal(link.source_work_trace_id, failed.trace_id);
      assert.notEqual(link.trace_id, failed.trace_id);
    } finally {
      local.close();
      await local.idle();
    }
  } finally {
    control.close();
  }
});

test('actual Register/Get frames derive RPC traces, waiting is explicit, background has no tool attempt', async (t) => {
  const f = await setup(t);
  const { events } = await capture(async () => {
    await observeTool(async () => {
      await f.control.establish('1');
      await f.control.idle();
    });
    await setImmediate();
    await f.control.refresh('1');
  });
  const rpc = events.filter((e) => e.message === 'Source RPC started');
  assert.deepEqual(
    rpc.map((e) => e.operation),
    ['source_register', 'source_get', 'source_get'],
  );
  for (const e of rpc) {
    const frame = f.requests.find((r) => r.request_id === e.request_id);
    assert.ok(frame);
    assert.equal(
      e.trace_id,
      natsTraceparent(Buffer.from(JSON.stringify(frame))).slice(3, 35),
    );
    assert.ok(
      events.some(
        (other) =>
          other.message === 'Source RPC response_received' &&
          other.request_id === e.request_id,
      ),
    );
  }
  const links = events.filter((e) => e.relation === 'waited_for');
  assert.ok(
    links.some((e) => e.source_work_trace_id === rpc[0].source_work_trace_id),
  );
  assert.ok(
    !links.some((e) => e.source_work_trace_id === rpc[2].source_work_trace_id),
  );
  assert.notEqual(rpc[0].source_work_trace_id, rpc[2].source_work_trace_id);
  assert.ok(rpc.every((e) => e.attempt_id === undefined));
});

test('native resolver rejection and RPC rejection preserve their distinct stages and fail closed', async (t) => {
  await t.test('native source resolver rejects before any RPC', async (t) => {
    const f = await setup(t);
    f.ctx.sessionController.resolveAgent = async () => ({
      error: 'fixture unavailable',
    });
    const { events } = await capture(async () => {
      await f.control.establish('1');
      await f.control.idle();
    });
    const failure = events.find((e) => e.message === 'Source witness failed');
    assert.equal(failure.stage, 'native_source_resolve');
    assert.ok(failure.exception.includes('network.host_incompatible'));
    assert.equal(f.requests.length, 0);
    assert.equal(f.ledger.peek().sourceControl.entries['1'].stopped, true);
    assert.ok(!events.some((e) => e.message === 'Source RPC started'));
  });
  await t.test(
    'original RPC exception has exact request trace and no response receipt',
    async (t) => {
      const cause = new Error('rpc fixture exception token=rpc-secret-canary', {
        cause: new Error('visible-rpc-inner'),
      });
      const f = await setup(t, {
        beforeReply: async () => {
          throw cause;
        },
      });
      const { events, text } = await capture(async () => {
        await f.control.establish('1');
        await f.control.idle();
      });
      assert.equal(f.requests.length, 1);
      const failure = events.find((e) => e.message === 'Source RPC failed');
      assert.equal(failure.stage, 'register_request');
      assert.equal(failure.request_id, f.requests[0].request_id);
      assert.ok(failure.exception.includes('rpc fixture exception'));
      assert.ok(failure.cause.includes('visible-rpc-inner'));
      assert.ok(!text.includes('rpc-secret-canary'));
      assert.ok(
        !events.some((e) => e.message === 'Source RPC response_received'),
      );
      const entry = f.ledger.peek().sourceControl.entries['1'];
      assert.equal(entry.head, null);
      assert.equal(entry.observation, null);
    },
  );
});

test('SDK publish stages keep exact payload, two-argument PUB and original rejection', async () => {
  for (const fail of [false, true]) {
    const payload = Buffer.from(
      JSON.stringify({
        request_id: 'publication-fixture',
        type: 'source_get',
        source_binding: 'wire-secret-canary',
      }),
    );
    const published = [];
    const cause = new Error('SDK refused token=publish-secret-canary');
    let closed = false,
      finish;
    const done = new Promise((r) => {
      finish = r;
    });
    const link = new NatsLink({
      url: 'wss://example.invalid',
      auth: { token: 'fixture' },
      upSubject: 'up',
      downSubject: 'down',
      inboxPrefix: 'fixture',
      createSocket: () => ({
        bufferedAmount: 0,
        addEventListener() {},
        removeEventListener() {},
        send() {},
        terminate() {},
        close() {},
      }),
      nats: {
        tokenAuthenticator: () => () => ({ auth_token: 'fixture' }),
        async wsconnect(options) {
          await options.wsFactory('wss://example.invalid');
          return {
            closed: () => done,
            isClosed: () => closed,
            async close() {
              closed = true;
              finish();
            },
            subscribe: () => ({ unsubscribe() {} }),
            async flush() {},
            publish(...args) {
              published.push(args);
              if (fail) throw cause;
            },
          };
        },
      },
    });
    await link.open();
    try {
      const { events, text } = await capture(async () => {
        if (fail)
          await assert.rejects(
            link.send(payload),
            (e) => e.code === 'publish_failed' && e.cause === cause,
          );
        else await link.send(payload);
      });
      assert.equal(published.length, 1);
      assert.equal(published[0].length, 2);
      assert.equal(published[0][0], 'up');
      assert.deepEqual(published[0][1], payload);
      assert.deepEqual(
        events.map((e) => e.message),
        [
          'NATS SDK publish attempted',
          fail
            ? 'NATS SDK publish failed'
            : 'NATS SDK publish accepted locally',
        ],
      );
      assert.ok(
        events.every(
          (e) =>
            e.request_id === 'publication-fixture' &&
            e.operation === 'source_get',
        ),
      );
      assert.equal(new Set(events.map((e) => e.trace_id)).size, 1);
      assert.ok(!text.includes('wire-secret-canary'));
      assert.ok(!text.includes('publish-secret-canary'));
      if (fail) assert.ok(events[1].exception.includes('SDK refused'));
    } finally {
      await link.close();
    }
  }
});

test('log masking does not serialize custom cause objects or loop on circular Error causes', async () => {
  const error = new Error('visible business request fixture');
  error.cause = new Error('visible nested stack');
  error.cause.cause = error;
  const { text } = await capture(() => {
    logEvent('ERROR', 'fixture', { trace_id: '1'.repeat(32) }, error);
    logEvent(
      'ERROR',
      'fixture',
      { trace_id: '2'.repeat(32) },
      new Error('custom cause', {
        cause: { body: 'custom-cause-secret-canary' },
      }),
    );
  });
  assert.ok(text.includes('visible nested stack'));
  assert.ok(!text.includes('custom-cause-secret-canary'));
});
