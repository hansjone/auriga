import test from 'node:test';
import assert from 'node:assert/strict';
import { createSourceControl } from '../../../src/network/source-control/controller.js';
import { setup, latch } from './fixtures.js';
import { createMemoryLedgerStore } from '../../../src/network/host/store.js';
import { openLedger } from '../../../src/network/host/ledger.js';
import { IDENTITY } from '../host/fixtures.js';
import { waitFor } from '../transport/support/client.js';
import { setImmediate } from 'node:timers/promises';

test('source issue waits for the same ledger durable operation barrier', async (t) => {
  const gate = latch(),
    entered = latch();
  t.after(() => gate.release());
  const f = await setup(t, {
    beforeCommit: async (draft) => {
      if (Object.keys(draft.sourceControl?.operations ?? {}).length) {
        entered.release();
        await gate.promise;
      }
    },
  });
  const pending = f.control.establish('1');
  await entered.promise;
  await setImmediate();
  assert.equal(f.requests.length, 0);
  gate.release();
  await pending;
  await f.control.idle();
  assert.deepEqual(
    f.requests.map((r) => r.type),
    ['source_register', 'source_get'],
  );
  assert.equal(
    f.store.snapshot().sourceControl.entries['1'].observation.source.state,
    'active',
  );
});

test('old generation fence prevents a delayed Get from committing an observation', async (t) => {
  const gate = latch(),
    entered = latch();
  const f = await setup(t, {
    afterClientReply: async (request) => {
      if (request.type === 'source_get') {
        entered.release();
        await gate.promise;
      }
    },
  });
  await f.control.establish('1');
  await entered.promise;
  f.invalidate();
  gate.release();
  await f.control.idle();
  assert.equal(f.store.snapshot().sourceControl.entries['1'].observation, null);
});

test('source controller is an internal capability', () => {
  assert.equal(typeof createSourceControl, 'function');
});

test('concurrent establishment uses one operation and one regular lane', async (t) => {
  const gate = latch(),
    entered = latch();
  const f = await setup(t, {
    beforeReply: async (request) => {
      if (request.type === 'source_register') {
        entered.release();
        await gate.promise;
      }
    },
  });
  await Promise.all(Array.from({ length: 12 }, () => f.control.establish('1')));
  await entered.promise;
  assert.equal(f.requests.length, 1);
  assert.equal(
    Object.keys(f.store.snapshot().sourceControl.operations).length,
    1,
  );
  gate.release();
  await f.control.idle();
  assert.equal(
    f.requests.filter((r) => r.type === 'source_register').length,
    1,
  );
});

test('lost Register response recovers the same operation and original body on a new epoch before fresh Get', async (t) => {
  const store = createMemoryLedgerStore();
  const f = await setup(t, {
    store,
    drop: (r) => r.type === 'source_register',
  });
  await f.control.establish('1');
  await f.control.idle();
  const op = Object.values(store.snapshot().sourceControl.operations)[0];
  assert.equal(op.receipt, null);
  f.retire();
  await f.ledger.close();
  const g = await setup(t, {
    store,
    native: f,
    peer: f.peer,
    signer: f.signer,
    ready: { epoch: '4', bootId: 'boot-b', channelNonce: 'nonce-b' },
  });
  await g.control.establish('1');
  await g.control.idle();
  const retry = g.requests.filter((r) => r.type === 'source_register').at(-1);
  assert.equal(retry.operation_id, op.id);
  assert.equal(retry.mode, 'reconcile');
  assert.equal(retry.native_witness, null);
  for (const key of Object.keys(op.body))
    assert.deepEqual(retry[key], op.body[key]);
  assert.equal(retry.epoch, '4');
  assert.equal(
    store.snapshot().sourceControl.entries['1'].observation.source.state,
    'active',
  );
});

for (const result of ['not_accepted', 'not_committed'])
  test(
    'only a durable ' +
      result +
      ' receipt permits a new operation; IAM digest stays opaque',
    async (t) => {
      const f = await setup(t, { failure: result });
      await f.control.establish('1');
      await f.control.idle();
      const first = Object.values(
        f.store.snapshot().sourceControl.operations,
      )[0];
      assert.equal(first.receipt.result, result);
      await f.control.refresh('1');
      const ops = Object.values(f.store.snapshot().sourceControl.operations);
      assert.equal(ops.length, 2);
      assert.notEqual(ops[1].id, first.id);
      if (result === 'not_committed')
        assert.equal(
          first.receipt.failure_evidence.request_digest,
          'sha256:' + 'b'.repeat(64),
        );
    },
  );

test('ordinary signed ERROR preserves pending and can never cause operation rollover', async (t) => {
  const f = await setup(t, {
    resign: true,
    beforeReply: (_r, result) => {
      result.type = 'error';
      result.code = 'network.conflict';
    },
  });
  await f.control.establish('1');
  await f.control.idle();
  const first = f.requests[0];
  await f.control.refresh('1');
  assert.equal(f.requests[1].operation_id, first.operation_id);
  assert.equal(f.requests[1].mode, 'reconcile');
  assert.equal(
    Object.keys(f.store.snapshot().sourceControl.operations).length,
    1,
  );
  assert.equal(
    Object.values(f.store.snapshot().sourceControl.operations)[0].receipt,
    null,
  );
});

test('natural expired head renews with observed revision rather than a guessed constant', async (t) => {
  const f = await setup(t);
  await f.control.establish('1');
  await f.control.idle();
  const head = f.store.snapshot().sourceControl.entries['1'].head;
  f.peer.facts.get(head).state = 'expired';
  f.peer.facts.get(head).revision = '17';
  await f.control.refresh('1');
  const observed = f.store.snapshot().sourceControl.entries['1'].observation;
  assert.equal(observed.source.revision, '17');
  assert.equal('valid_until' in observed, false);
  await f.control.refresh('1');
  const successor = f.requests
    .filter((r) => r.type === 'source_register')
    .at(-1);
  assert.equal(successor.predecessor_binding, head);
  assert.equal(successor.expected_revision, '17');
  assert.notEqual(successor.operation_id, f.requests[0].operation_id);
});

for (const state of ['revoked', 'stopped'])
  test(
    state + ' is saved with current revision and never renews',
    async (t) => {
      const f = await setup(t);
      await f.control.establish('1');
      await f.control.idle();
      const head = f.store.snapshot().sourceControl.entries['1'].head;
      f.peer.facts.get(head).state = state;
      f.peer.facts.get(head).revision = '9';
      await f.control.refresh('1');
      await f.control.refresh('1');
      assert.equal(f.store.snapshot().sourceControl.entries['1'].stopped, true);
      assert.equal(
        f.requests.filter((r) => r.type === 'source_register').length,
        1,
      );
    },
  );

test('local stop wins over delayed Register and Revoke needs no live host or fresh witness', async (t) => {
  const gate = latch(),
    entered = latch();
  const f = await setup(t, {
    beforeReply: async (r) => {
      if (r.type === 'source_register') {
        entered.release();
        await gate.promise;
      }
    },
  });
  await f.control.establish('1');
  await entered.promise;
  f.sessions.delete('session-a');
  f.agents.delete('session-a');
  await f.control.stop('1', 'host_closed');
  assert.equal(
    f.store.snapshot().sourceControl.entries['1'].bindingRevision,
    '2',
  );
  gate.release();
  await f.control.idle();
  const s = f.store.snapshot().sourceControl;
  assert.equal(s.entries['1'].stopped, true);
  assert.equal(s.entries['1'].observation, null);
  assert.equal(Object.values(s.revocations)[0].receipt.lineage_stopped, true);
  const revoke = f.requests.find((r) => r.type === 'source_revoke');
  assert.equal('native_witness' in revoke, false);
  assert.equal('expected_revision' in revoke, false);
});

test('reserved revocation lane overtakes an in-flight Get and blocks its late activation', async (t) => {
  let armed = false;
  const gate = latch(),
    entered = latch();
  const f = await setup(t, {
    beforeReply: async (r) => {
      if (armed && r.type === 'source_get') {
        entered.release();
        await gate.promise;
      }
    },
  });
  await f.control.establish('1');
  await f.control.idle();
  armed = true;
  const pending = f.control.refresh('1');
  await entered.promise;
  await f.control.stop('1');
  await waitFor(
    () => f.requests.some((r) => r.type === 'source_revoke'),
    'revocation overtakes Get',
  );
  assert.equal(f.store.snapshot().sourceControl.entries['1'].observation, null);
  gate.release();
  await pending;
  assert.equal(f.store.snapshot().sourceControl.entries['1'].observation, null);
});

for (const [name, mutate] of [
  ['archive', (f) => f.archivedSessionIds.push('session-a')],
  ['close', (f) => f.sessions.delete('session-a')],
  ['agent replacement', (f) => f.attach('session-a')],
  [
    'provider replacement',
    (f) => {
      f.ctx.sessionPersistence = {
        ...f.ctx.sessionPersistence,
        identity: Symbol(),
      };
    },
  ],
  [
    'cwd',
    (f) => {
      f.agent.session.header.cwd = '/other';
    },
  ],
])
  test(
    'native ' + name + ' durably stops and revokes without another resolver',
    async (t) => {
      const f = await setup(t);
      await f.control.establish('1');
      await f.control.idle();
      const calls = f.calls.filter(([name]) => name === 'resume').length;
      mutate(f);
      await f.control.sweep();
      await f.control.idle();
      assert.equal(f.store.snapshot().sourceControl.entries['1'].stopped, true);
      assert.ok(
        Object.values(f.store.snapshot().sourceControl.revocations)[0].receipt,
      );
      assert.equal(f.calls.filter(([name]) => name === 'resume').length, calls);
    },
  );

test('binding lifecycle mutation advances the source fence in the same durable transaction', async (t) => {
  const f = await setup(t);
  await f.control.establish('1');
  await f.control.idle();
  await f.adapter.updateBinding('1', { lifecycle: 'closed' });
  const s = f.store.snapshot().sourceControl;
  assert.equal(s.entries['1'].stopped, true);
  assert.equal(s.entries['1'].bindingRevision, '2');
  assert.equal(Object.keys(s.revocations).length, 1);
  await f.control.sweep();
  await f.control.idle();
  assert.ok(
    Object.values(f.store.snapshot().sourceControl.revocations)[0].receipt,
  );
});

test('uncertain source operation commit sends no frame and poisons the writer', async (t) => {
  const f = await setup(t, {
    beforeCommit: (draft) => {
      if (Object.keys(draft.sourceControl?.operations ?? {}).length)
        throw new Error('uncertain disk');
    },
  });
  await f.control.establish('1');
  await f.control.idle();
  assert.equal(f.requests.length, 0);
  assert.throws(() => f.ledger.assertAvailable());
});

test('lost revoke response reopens durable outbox and retries original operation after host closure', async (t) => {
  const store = createMemoryLedgerStore();
  const f = await setup(t, { store, drop: (r) => r.type === 'source_revoke' });
  await f.control.establish('1');
  await f.control.idle();
  await f.control.stop('1');
  await f.control.idle();
  const original = Object.values(store.snapshot().sourceControl.revocations)[0];
  assert.equal(original.receipt, null);
  f.retire();
  await f.ledger.close();
  const g = await setup(t, {
    store,
    peer: f.peer,
    signer: f.signer,
    native: f,
  });
  g.sessions.delete('session-a');
  g.agents.delete('session-a');
  await g.control.sweep();
  await g.control.idle();
  assert.equal(g.requests.at(-1).operation_id, original.id);
  assert.equal(
    Object.values(store.snapshot().sourceControl.revocations)[0].receipt
      .lineage_stopped,
    true,
  );
});

test('incomplete source recovery maps fail closed rather than allocating another source', async (t) => {
  const f = await setup(t);
  await f.control.establish('1');
  await f.control.idle();
  f.retire();
  await f.ledger.close();
  const bad = f.store.snapshot();
  delete bad.sourceControl.operations;
  f.store.restore(bad);
  await assert.rejects(openLedger({ store: f.store, identity: IDENTITY }));
});

test('a source-enabled ledger cannot lose the entire source section and silently migrate again', async (t) => {
  const f = await setup(t);
  await f.control.establish('1');
  await f.control.idle();
  f.retire();
  await f.ledger.close();
  const bad = f.store.snapshot();
  assert.equal(bad.version, 3);
  delete bad.sourceControl;
  f.store.restore(bad);
  await assert.rejects(openLedger({ store: f.store, identity: IDENTITY }));
});

test('immutable operation history and saved witnesses cannot be rebound or removed', async (t) => {
  const f = await setup(t);
  await f.control.establish('1');
  await f.control.idle();
  const operationId = Object.keys(
    f.store.snapshot().sourceControl.operations,
  )[0];
  await assert.rejects(
    f.ledger.update((draft) => {
      draft.sourceControl.operations[operationId].body.expected_revision = '7';
    }),
  );
  const witness = Object.keys(f.store.snapshot().sourceControl.witnesses)[0];
  await assert.rejects(
    f.ledger.update((draft) => {
      delete draft.sourceControl.witnesses[witness];
    }),
  );
  assert.equal(
    f.store.snapshot().sourceControl.operations[operationId].body
      .expected_revision,
    '0',
  );
});

test('a hung revocation uses its reserved lane without starving another registered session', async (t) => {
  const gate = latch(),
    entered = latch();
  t.after(() => gate.release());
  const f = await setup(t, {
    beforeReply: async (r) => {
      if (r.type === 'source_revoke') {
        entered.release();
        await gate.promise;
      }
    },
  });
  await f.control.establish('1');
  await f.control.idle();
  await f.control.stop('1');
  await entered.promise;
  f.seed('session-b');
  const agent = f.attach('session-b');
  const local = await f.adapter.bindExistingSession({
    opId: 'join-2',
    hostSessionId: agent.id,
    cwd: agent.session.header.cwd,
    kind: 'session',
  });
  await f.control.establish(local);
  await waitFor(
    () =>
      f.requests.some(
        (r) => r.type === 'source_register' && r.registration.local === local,
      ),
    'independent regular lane',
    1000,
  );
  gate.release();
  await f.control.idle();
  assert.equal(
    f.store.snapshot().sourceControl.entries[local].observation.source.state,
    'active',
  );
});
