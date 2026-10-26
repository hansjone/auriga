import { test } from 'node:test';
import assert from 'node:assert/strict';
import { fixture as nativeFixture } from './host/native/fixtures.js';
import { IDENTITY, createFixturePlatform } from './host/fixtures.js';
import { openLedger } from '../../src/network/host/ledger.js';
import { createMemoryLedgerStore } from '../../src/network/host/store.js';
import { createSessionRegistration } from '../../src/network/session-registration.js';

function latch() {
  let release;
  const promise = new Promise((resolve) => {
    release = resolve;
  });
  return { promise, release };
}

async function fixture(t, options = {}) {
  const native = nativeFixture();
  const agent = native.attach('session-a');
  const tools = new Map();
  native.ctx.tools = {
    register(tool) {
      tools.set(tool.name, tool);
      return () => tools.delete(tool.name);
    },
  };
  const store = options.store ?? createMemoryLedgerStore(options.storage);
  const ledger = await openLedger({
    store,
    identity: IDENTITY,
    initialize: true,
  });
  const platform = options.platform ?? createFixturePlatform();
  let current = true;
  const capability = createSessionRegistration({
    ctx: native.ctx,
    ledger,
    registerLocal: options.registerLocal ?? platform.network.registerLocal,
    assertCurrent() {
      if (!current) throw new Error('stale generation');
    },
  });
  await capability.start();
  t.after(async () => {
    capability.close();
    await ledger.close();
  });
  return {
    ...native,
    agent,
    tools,
    store,
    ledger,
    platform,
    capability,
    retire() {
      current = false;
      capability.close();
    },
    join(input = {}, execution = { agent }) {
      return tools.get('auriga_network_join').execute(input, execution);
    },
  };
}

test('join awaits both native barriers and durable binding, is concurrent-idempotent and returns only opaque address', async (t) => {
  const f = await fixture(t);
  const tool = f.tools.get('auriga_network_join');
  assert.deepEqual(tool.parameters, {
    type: 'object',
    properties: {},
    additionalProperties: false,
  });
  const results = await Promise.all([f.join(), f.join()]);
  assert.deepEqual(results, [
    { status: 'registered', address: 'aur1:2.40.1' },
    { status: 'registered', address: 'aur1:2.40.1' },
  ]);
  assert.equal(f.store.snapshot().localHighWater, '1');
  assert.equal(f.store.snapshot().bindings['1'].hostSessionId, 'session-a');
  assert.equal(f.platform.state.registrations.size, 1);
  assert.ok(f.calls.some(([name]) => name === 'flush'));
  assert.ok(f.calls.some(([name]) => name === 'persistence-flush'));
  assert.equal(
    f.calls.some(([name]) => name === 'create' || name === 'prompt'),
    false,
  );
  for (const key of ['recipients', 'relations', 'inbox', 'bodies'])
    assert.deepEqual(f.ledger.peek()[key], {});
});

for (const [name, change] of [
  ['fake execution', (_f) => ({ agent: { session: { id: 'session-a' } } })],
  ['other agent', (f) => ({ agent: { ...f.agent } })],
  [
    'resolve other live agent',
    (f) => {
      f.seed('other');
      const other = f.attach('other');
      f.ctx.sessionController.resolveAgent = async () => ({ agent: other });
    },
  ],
  [
    'runtime-owned child',
    (f) => {
      f.seed('parent');
      f.attach('parent');
      f.agent.session.header.parentSession = 'parent';
      f.ctx.agents.isOwnedBy = () => true;
    },
  ],
  [
    'archive',
    (f) => {
      f.archivedSessionIds.push('session-a');
    },
  ],
  [
    'closed session',
    (f) => {
      f.sessions.delete('session-a');
    },
  ],
  [
    'subagent',
    (f) => {
      f.agent.session.header.origin = 'subagent';
    },
  ],
  [
    'bad cwd',
    (f) => {
      f.agent.session.header.cwd = '';
    },
  ],
  [
    'flush false',
    (f) => {
      f.state.flush = false;
    },
  ],
  [
    'flush nonboolean',
    (f) => {
      f.state.flush = undefined;
    },
  ],
  [
    'stat failure',
    (f) => {
      f.ctx.sessionPersistence.stat = async () => {
        throw new Error('private');
      };
    },
  ],
  [
    'provider flush failure',
    (f) => {
      f.state.flushError = true;
    },
  ],
  [
    'open failure',
    (f) => {
      f.ctx.sessionPersistence.open = async () => {
        throw new Error('private');
      };
    },
  ],
  [
    'provider swap',
    (f) => {
      f.ctx.sessions.flush = async () => {
        f.ctx.sessionPersistence = {
          ...f.ctx.sessionPersistence,
          identity: Symbol(),
        };
        return true;
      };
    },
  ],
])
  test('join rejects ' + name + ' before allocation', async (t) => {
    const f = await fixture(t);
    const execution = change(f);
    const result = await f.join({}, execution ?? { agent: f.agent });
    assert.deepEqual(result, { status: 'unavailable' });
    assert.equal(f.store.snapshot().localHighWater, '0');
  });

test('join rejects every argument and nonobject input without host calls', async (t) => {
  const f = await fixture(t);
  for (const input of [
    null,
    [],
    1,
    'x',
    { sessionId: 'session-a' },
    { local: '1' },
    { cwd: '/x' },
    { owner: 'x' },
    { source_binding: 'x' },
  ]) {
    assert.deepEqual(await f.join(input), { status: 'unavailable' });
  }
  assert.equal(f.calls.length, 0);
});

test('long opaque native id uses a bounded opaque stable operation and is never sent as an operation id', async (t) => {
  const f = await fixture(t);
  const id = 'private-native-' + 'x'.repeat(300);
  f.seed(id);
  const agent = f.attach(id);
  assert.deepEqual(await f.join({}, { agent }), {
    status: 'registered',
    address: 'aur1:2.40.1',
  });
  const op = f.store.snapshot().locals['1'].opId;
  assert.equal(op.length, 69);
  assert.equal(op.includes(id), false);
  assert.deepEqual(await f.join({}, { agent }), {
    status: 'registered',
    address: 'aur1:2.40.1',
  });
});

test('durability barrier is awaited before reservation and logout fences its continuation', async (t) => {
  const f = await fixture(t),
    entered = latch(),
    gate = latch();
  f.ctx.sessions.flush = async () => {
    entered.release();
    await gate.promise;
    return true;
  };
  const tool = f.tools.get('auriga_network_join');
  const pending = tool.execute({}, { agent: f.agent });
  await entered.promise;
  assert.equal(f.store.snapshot().localHighWater, '0');
  f.retire();
  assert.equal(f.tools.size, 0);
  gate.release();
  assert.deepEqual(await pending, { status: 'unavailable' });
  assert.equal(f.store.snapshot().localHighWater, '0');
  assert.deepEqual(await tool.execute({}, { agent: f.agent }), {
    status: 'unavailable',
  });
});

test('old generation register response cannot commit credentials or bind; original pending survives', async (t) => {
  const entered = latch(),
    gate = latch();
  const platform = createFixturePlatform();
  const f = await fixture(t, {
    platform,
    registerLocal: async (entry) => {
      const result = await platform.network.registerLocal(entry);
      entered.release();
      await gate.promise;
      return result;
    },
  });
  const pending = f.join();
  await entered.promise;
  const original = f.store.snapshot().locals['1'];
  f.retire();
  gate.release();
  assert.deepEqual(await pending, { status: 'unavailable' });
  assert.deepEqual(f.store.snapshot().locals['1'], original);
  assert.deepEqual(f.store.snapshot().bindings, {});
});

test('unknown registration retries same op/local/fingerprint before another session allocates', async (t) => {
  const f = await fixture(t);
  f.platform.drop.register = 1;
  assert.deepEqual(await f.join(), { status: 'unavailable' });
  const original = f.store.snapshot().locals['1'];
  f.seed('session-b');
  const other = f.attach('session-b');
  assert.deepEqual(await f.join({}, { agent: other }), {
    status: 'registered',
    address: 'aur1:2.40.2',
  });
  assert.deepEqual(await f.join(), {
    status: 'registered',
    address: 'aur1:2.40.1',
  });
  const restored = f.store.snapshot().locals['1'];
  for (const key of ['opId', 'local', 'fingerprint', 'mappingDigest'])
    assert.equal(restored[key], original[key]);
  assert.equal(f.platform.state.registrations.size, 2);
});

test('registered but unbound mapping recovers only with original host mapping', async (t) => {
  const f = await fixture(t);
  f.platform.drop.register = 1;
  await f.join();
  f.agent.session.header.cwd = '/changed';
  assert.deepEqual(await f.join(), { status: 'unavailable' });
  assert.equal(f.store.snapshot().localHighWater, '1');
  assert.deepEqual(f.store.snapshot().bindings, {});
});

for (const phase of ['credentials', 'binding'])
  test(
    'final ' +
      phase +
      ' commit remains fenced and cannot return old generation success',
    async (t) => {
      const entered = latch(),
        gate = latch();
      let armed = false;
      const f = await fixture(t, {
        storage: {
          beforeCommit: async (draft) => {
            if (
              armed &&
              (phase === 'credentials'
                ? draft.locals['1']?.state === 'registered'
                : draft.bindings['1']?.state === 'bound')
            ) {
              armed = false;
              entered.release();
              await gate.promise;
            }
          },
        },
      });
      armed = true;
      const pending = f.join();
      await entered.promise;
      assert.deepEqual(f.ledger.peek().bindings, {});
      f.retire();
      gate.release();
      assert.deepEqual(await pending, { status: 'unavailable' });
      assert.deepEqual(f.ledger.peek().bindings, {});
      assert.throws(() => f.ledger.assertAvailable(), {
        code: 'host.ledger_unavailable',
      });
    },
  );

test('binding storage failure reopens original registration and recovers mapping without new allocation', async (t) => {
  let fail = true;
  const store = createMemoryLedgerStore({
    beforeCommit: (draft) => {
      if (fail && draft.bindings['1']) throw new Error('binding disk failure');
    },
  });
  const f = await fixture(t, { store });
  assert.deepEqual(await f.join(), { status: 'unavailable' });
  const original = store.snapshot().locals['1'];
  assert.equal(original.state, 'registered');
  assert.deepEqual(store.snapshot().bindings, {});
  f.capability.close();
  await f.ledger.close();
  fail = false;
  const restored = await fixture(t, { store, platform: f.platform });
  assert.deepEqual(await restored.join(), {
    status: 'registered',
    address: 'aur1:2.40.1',
  });
  assert.deepEqual(store.snapshot().locals['1'], original);
  assert.equal(f.platform.state.registrations.size, 1);
});

test('credential commit failure keeps the pending original and reopens by the same operation', async (t) => {
  let fail = true;
  const store = createMemoryLedgerStore({
    beforeCommit: (draft) => {
      if (fail && draft.locals['1']?.state === 'registered')
        throw new Error('credential disk failure');
    },
  });
  const f = await fixture(t, { store });
  assert.deepEqual(await f.join(), { status: 'unavailable' });
  const original = store.snapshot().locals['1'];
  assert.equal(original.state, 'reserved');
  assert.equal(f.platform.state.registrations.size, 1);
  f.capability.close();
  await f.ledger.close();
  fail = false;
  const restored = await fixture(t, { store, platform: f.platform });
  assert.deepEqual(await restored.join(), {
    status: 'registered',
    address: 'aur1:2.40.1',
  });
  for (const key of ['opId', 'local', 'fingerprint', 'mappingDigest'])
    assert.equal(store.snapshot().locals['1'][key], original[key]);
  assert.equal(f.platform.state.registrations.size, 1);
});
