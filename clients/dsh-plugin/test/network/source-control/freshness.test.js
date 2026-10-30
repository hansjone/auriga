import test from 'node:test';
import assert from 'node:assert/strict';
import { setup, iso } from './fixtures.js';
import { publicationFixture } from '../trust/keyset-fixtures.js';

async function expiryFixture(t, options = {}) {
  const signer = publicationFixture();
  let clock = signer.now() + 900,
    armed = false,
    f;
  const priorWitnesses = new Set();
  signer.now = () => clock;
  f = await setup(t, {
    signer,
    resign: true,
    beforeCommit: (draft) => {
      if (!armed || !options.commitPhase) return;
      const s = draft.sourceControl;
      const selected =
        options.commitPhase === 'witness'
          ? Object.values(s?.witnesses ?? {}).some(
              ({ request }) =>
                request.type === 'source_get' &&
                !priorWitnesses.has(request.native_witness.witness_id),
            )
          : s?.entries['1'].observation?.source.state === 'expired';
      if (selected) {
        armed = false;
        clock += options.delayMs;
        t.mock.timers.tick(options.delayMs);
      }
    },
    beforeReply: async (request, result) => {
      if (request.type === 'source_register' && result.source) {
        const expires = iso(clock + 5000);
        result.source.expires_at = expires;
        f.peer.facts.get(result.source.source_binding).expires_at = expires;
        f.peer.operations.get(request.operation_id).source.expires_at = expires;
      }
      if (request.type === 'source_get' && result.source.state === 'active')
        result.valid_until = iso(
          Math.min(
            Date.parse(result.valid_until),
            Date.parse(result.source.expires_at),
          ),
        );
      if (armed && !options.commitPhase && request.type === options.phase) {
        clock += options.delayMs;
        t.mock.timers.tick(options.delayMs);
        await options.change?.(f);
      }
    },
  });
  return {
    ...f,
    arm() {
      t.mock.timers.enable({ apis: ['setTimeout'] });
      for (const id of Object.keys(
        f.store.snapshot().sourceControl?.witnesses ?? {},
      ))
        priorWitnesses.add(id);
      armed = true;
    },
    disarm() {
      armed = false;
    },
    expire() {
      clock += 6000;
      const head = f.store.snapshot().sourceControl.entries['1'].head;
      assert.ok(clock > Date.parse(f.peer.facts.get(head).expires_at));
      f.peer.facts.get(head).state = 'expired';
      f.peer.facts.get(head).revision = '2';
      return head;
    },
  };
}

test('discarding an unusable observation retains the prior policy revision floor', async (t) => {
  let mode = 'valid';
  const f = await setup(t, {
    resign: true,
    beforeReply: (r, v) => {
      if (r.type !== 'source_get') return;
      if (mode === 'failed') v.witness_id = 'wrong-witness';
      if (mode === 'rollback') v.policy_revision = '6';
    },
  });
  await f.control.establish('1');
  await f.control.idle();
  assert.equal(
    f.store.snapshot().sourceControl.entries['1'].observation.policy_revision,
    '7',
  );
  mode = 'failed';
  await f.control.refresh('1');
  assert.equal(f.store.snapshot().sourceControl.entries['1'].observation, null);
  mode = 'rollback';
  await f.control.refresh('1');
  assert.equal(f.store.snapshot().sourceControl.entries['1'].observation, null);
  assert.equal(
    f.store.snapshot().sourceControl.entries['1'].policyRevision,
    '7',
  );
  mode = 'valid';
  await f.control.refresh('1');
  assert.equal(
    f.store.snapshot().sourceControl.entries['1'].observation.policy_revision,
    '7',
  );
  assert.equal(f.store.snapshot().sourceControl.entries['1'].stopped, false);
});

for (const commitPhase of ['witness', 'observation'])
  test(`pure expiry across ${commitPhase} durability remains retryable without poisoning the ledger`, async (t) => {
    const f = await expiryFixture(t, {
      phase: 'source_get',
      delayMs: 29900,
      commitPhase,
    });
    await f.control.establish('1');
    await f.control.idle();
    f.expire();
    f.arm();
    await f.control.refresh('1');
    f.disarm();
    f.ledger.assertAvailable();
    const e = f.store.snapshot().sourceControl.entries['1'];
    assert.equal(e.stopped, false);
    assert.equal(e.observation, null);
    assert.equal(
      f.requests.filter((r) => r.type === 'source_register').length,
      1,
    );
    await f.control.refresh('1');
    await f.control.refresh('1');
    assert.equal(
      f.requests.filter((r) => r.type === 'source_register').length,
      2,
    );
    assert.equal(
      f.requests.filter((r) => r.type === 'source_revoke').length,
      0,
    );
  });

for (const delayMs of [0, 29900, 30000])
  test(`healthy natural-expiry Get at +900ms with ${delayMs}ms response retries fresh and renews`, async (t) => {
    const f = await expiryFixture(t, { phase: 'source_get', delayMs });
    await f.control.establish('1');
    await f.control.idle();
    const head = f.expire();
    const witnessCount = f.requests.filter(
      (r) => r.type === 'source_get',
    ).length;
    const resolverCount = f.calls.filter(([name]) => name === 'resume').length;
    f.arm();
    await f.control.refresh('1');
    f.disarm();
    const after = f.store.snapshot().sourceControl.entries['1'];
    assert.equal(after.stopped, false);
    assert.equal(after.bindingRevision, '1');
    assert.equal(after.stopReason, null);
    assert.equal(f.ctx.agents.get('session-a'), f.agent);
    assert.equal(f.ctx.sessions.get('session-a'), f.agent.session);
    if (delayMs) {
      assert.equal(
        after.observation,
        null,
        'expired/timeout observation is unusable',
      );
      assert.equal(
        after.facts[head].revision,
        '1',
        'discard the late terminal revision',
      );
      assert.equal(
        f.requests.filter((r) => r.type === 'source_register').length,
        1,
      );
    } else assert.equal(after.observation.source.revision, '2');
    await f.control.refresh('1');
    await f.control.refresh('1');
    await f.control.establish('1');
    await f.control.idle();
    assert.equal(
      f.requests.filter((r) => r.type === 'source_register').length,
      2,
    );
    assert.equal(
      f.requests.filter((r) => r.type === 'source_revoke').length,
      0,
    );
    const gets = f.requests.filter((r) => r.type === 'source_get');
    assert.equal(
      new Set(gets.map((r) => r.native_witness.witness_id)).size,
      gets.length,
    );
    assert.ok(gets.length > witnessCount);
    assert.ok(
      f.calls.filter(([name]) => name === 'resume').length > resolverCount,
    );
    const successor = f.requests.filter((r) => r.type === 'source_register')[1];
    assert.equal(successor.predecessor_binding, head);
    assert.equal(successor.expected_revision, '2');
    assert.equal(
      f.store.snapshot().sourceControl.entries['1'].observation.source.state,
      'active',
    );
  });

for (const phase of ['source_register', 'source_get']) {
  test(`${phase} freshness expiry preserves history without stopping a healthy native`, async (t) => {
    const f = await expiryFixture(t, { phase, delayMs: 29900 });
    if (phase === 'source_get') {
      await f.control.establish('1');
      await f.control.idle();
      f.expire();
    }
    f.arm();
    if (phase === 'source_register') {
      await f.control.establish('1');
      await f.control.idle();
    } else await f.control.refresh('1');
    f.disarm();
    const s = f.store.snapshot().sourceControl,
      op = Object.values(s.operations)[0];
    assert.equal(s.entries['1'].stopped, false);
    assert.equal(s.entries['1'].observation, null);
    assert.equal(
      op.receipt.result,
      'registered',
      'Register stays a durable historical receipt',
    );
    assert.equal(
      f.requests.filter((r) => r.type === 'source_revoke').length,
      0,
    );
    if (phase === 'source_register') {
      assert.equal(
        f.requests.filter((r) => r.type === 'source_get').length,
        0,
        'old receipt must not immediately activate',
      );
      f.peer.facts.get(s.entries['1'].head).state = 'expired';
      f.peer.facts.get(s.entries['1'].head).revision = '2';
    }
    await f.control.refresh('1');
    await f.control.refresh('1');
    assert.equal(
      f.requests.filter((r) => r.type === 'source_register').length,
      2,
    );
    assert.equal(
      Object.values(f.store.snapshot().sourceControl.operations)[0].id,
      op.id,
    );
    assert.equal(f.store.snapshot().sourceControl.entries['1'].stopped, false);
  });

  for (const [name, change] of [
    ['agent replaced', (f) => f.attach('session-a')],
    [
      'mapping fence changed',
      (f) =>
        f.ledger.update((draft) => {
          draft.bindings['1'].lifecycle = 'closed';
        }),
    ],
    ['local stop', (f) => f.control.stop('1', 'explicit_stop')],
  ])
    test(`${phase} with expired freshness and ${name} still stops and revokes`, async (t) => {
      const f = await expiryFixture(t, { phase, delayMs: 29900, change });
      if (phase === 'source_get') {
        await f.control.establish('1');
        await f.control.idle();
        f.expire();
      }
      f.arm();
      if (phase === 'source_register') {
        await f.control.establish('1');
        await f.control.idle();
      } else await f.control.refresh('1');
      f.disarm();
      await f.control.idle();
      const state = f.store.snapshot().sourceControl;
      assert.equal(state.entries['1'].stopped, true);
      assert.equal(state.entries['1'].bindingRevision, '2');
      assert.equal(state.entries['1'].observation, null);
      assert.ok(Object.values(state.revocations)[0]?.receipt?.lineage_stopped);
      assert.equal(
        f.requests.filter((r) => r.type === 'source_register').length,
        1,
      );
    });
}
