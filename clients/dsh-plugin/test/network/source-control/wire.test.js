import test from 'node:test';
import assert from 'node:assert/strict';
import { setup, latch } from './fixtures.js';
import { waitFor } from '../transport/support/client.js';
import { publicationFixture } from '../trust/keyset-fixtures.js';

for (const [name, alter, resign = true] of [
  [
    'operation',
    (_r, v) => {
      v.operation_id = 'other-op';
    },
  ],
  [
    'signature',
    (_r, v) => {
      v.source.source_binding = 'tampered';
    },
    false,
  ],
  [
    'purpose',
    (_r, v) => {
      v.server_auth.purpose = 'business_delivery';
    },
  ],
  [
    'audience',
    (_r, v) => {
      v.server_auth.audience.node = '65';
    },
  ],
  [
    'epoch',
    (_r, v) => {
      v.server_auth.audience.epoch = '2';
    },
  ],
  [
    'incarnation',
    (_r, v) => {
      v.server_auth.audience.incarnation = 'other';
    },
  ],
  [
    'nonce',
    (_r, v) => {
      v.server_auth.channel_nonce = 'other';
    },
  ],
  [
    'request binding',
    (_r, v) => {
      v.server_auth.request_id = 'other';
    },
  ],
  [
    'major',
    (_r, v) => {
      v.app_version = '2.0';
    },
  ],
  [
    'source revision',
    (_r, v) => {
      v.source.revision = '0';
    },
  ],
  [
    'native mapping id',
    (_r, v) => {
      v.source.native_binding.binding_id = 'other';
    },
  ],
  [
    'native mapping revision',
    (_r, v) => {
      v.source.native_binding.binding_revision = '2';
    },
  ],
  ...[
    'registration_id',
    'allocation_op_id',
    'allocation_fingerprint',
    'registration_revision',
    'local',
    'address_type',
  ].map((field) => [
    'typed ' + field,
    (_r, v) => {
      v.source.registration[field] =
        field === 'address_type' ? 'subagent' : 'different';
    },
  ]),
])
  test(
    'authenticated source wire rejects wrong ' +
      name +
      ' and preserves the original operation',
    async (t) => {
      const f = await setup(t, { beforeReply: alter, resign });
      await f.control.establish('1');
      await f.control.idle();
      const s = f.store.snapshot().sourceControl;
      assert.equal(s.entries['1'].head, null);
      assert.equal(Object.values(s.operations)[0].receipt, null);
      assert.equal(s.entries['1'].observation, null);
    },
  );

test('compatible minor and unknown optional fields stay covered by signature and stored intact', async (t) => {
  const f = await setup(t, {
    resign: true,
    beforeReply: (_r, v) => {
      v.app_version = '1.27';
      v.extension = { note: 'future' };
      v.source.extra = true;
    },
  });
  await f.control.establish('1');
  await f.control.idle();
  const s = f.store.snapshot().sourceControl;
  assert.deepEqual(Object.values(s.operations)[0].receipt.extension, {
    note: 'future',
  });
  assert.equal(s.entries['1'].observation.source.extra, true);
  const bytes = JSON.stringify(f.requests);
  assert.equal(bytes.includes('session-a'), false);
  assert.equal(bytes.includes('/work/project'), false);
});

for (const [name, alter] of [
  [
    'witness echo',
    (v) => {
      v.witness_id = 'other';
    },
  ],
  [
    'extended native lifetime',
    (v) => {
      v.valid_until = v.source.expires_at;
    },
  ],
  [
    'missing active validity',
    (v) => {
      delete v.valid_until;
    },
  ],
  [
    'terminal validity',
    (v) => {
      v.source.state = 'expired';
    },
  ],
  [
    'unknown state',
    (v) => {
      v.source.state = 'new_state';
    },
  ],
  [
    'policy revision',
    (v) => {
      v.policy_revision = '0';
    },
  ],
  [
    'time',
    (v) => {
      v.observed_at = '2000-01-01T00:00:00Z';
    },
  ],
])
  test(
    'Get rejects signed wrong ' + name + ' without saving an observation',
    async (t) => {
      const f = await setup(t, {
        resign: true,
        beforeReply: (r, v) => {
          if (r.type === 'source_get') alter(v);
        },
      });
      await f.control.establish('1');
      await f.control.idle();
      assert.equal(
        f.store.snapshot().sourceControl.entries['1'].observation,
        null,
      );
      assert.equal(
        Object.values(f.store.snapshot().sourceControl.operations)[0].receipt
          .result,
        'registered',
      );
    },
  );

test('not_accepted digest mismatch cannot release pending; IAM digest is intentionally opaque', async (t) => {
  const f = await setup(t, {
    failure: 'not_accepted',
    resign: true,
    beforeReply: (_r, v) => {
      v.failure_evidence.request_digest = 'sha256:' + 'b'.repeat(64);
    },
  });
  await f.control.establish('1');
  await f.control.idle();
  await f.control.refresh('1');
  assert.equal(
    Object.keys(f.store.snapshot().sourceControl.operations).length,
    1,
  );
  assert.equal(
    Object.values(f.store.snapshot().sourceControl.operations)[0].receipt,
    null,
  );
});

test('Register receipt durability precedes any Get', async (t) => {
  const entered = latch(),
    gate = latch();
  const f = await setup(t, {
    beforeCommit: async (draft) => {
      if (
        Object.values(draft.sourceControl?.operations ?? {}).some(
          (o) => o.receipt,
        )
      ) {
        entered.release();
        await gate.promise;
      }
    },
  });
  await f.control.establish('1');
  await entered.promise;
  assert.equal(
    f.requests.some((r) => r.type === 'source_get'),
    false,
  );
  assert.equal(
    Object.values(f.ledger.peek().sourceControl.operations)[0].receipt,
    null,
  );
  gate.release();
  await f.control.idle();
  assert.equal(f.requests.at(-1).type, 'source_get');
});

test('provider swap across actual resolver await stops before issuing a witness', async (t) => {
  const f = await setup(t),
    entered = latch(),
    gate = latch();
  f.ctx.sessionController.resolveAgent = async () => {
    entered.release();
    await gate.promise;
    return { agent: f.agent };
  };
  await f.control.establish('1');
  await entered.promise;
  f.ctx.sessionPersistence = {
    ...f.ctx.sessionPersistence,
    identity: Symbol(),
  };
  gate.release();
  await f.control.idle();
  assert.equal(f.requests.length, 0);
  assert.equal(f.store.snapshot().sourceControl.entries['1'].stopped, true);
});

test('30 second timeout releases regular lane; late reply cannot activate, recovery keeps original key', async (t) => {
  const gate = latch(),
    entered = latch();
  let hold = true;
  const f = await setup(t, {
    beforeReply: async (r) => {
      if (hold && r.type === 'source_register') {
        entered.release();
        await gate.promise;
      }
    },
  });
  t.mock.timers.enable({ apis: ['setTimeout'] });
  await f.control.establish('1');
  await entered.promise;
  t.mock.timers.tick(30000);
  await f.control.idle();
  const op = Object.values(f.store.snapshot().sourceControl.operations)[0];
  assert.equal(op.receipt, null);
  hold = false;
  await f.control.refresh('1');
  assert.equal(f.requests[1].operation_id, op.id);
  assert.equal(f.requests[1].mode, 'reconcile');
  gate.release();
  assert.equal(
    Object.keys(f.store.snapshot().sourceControl.operations).length,
    1,
  );
});

test('30 second Revoke timeout preserves local stop and durable outbox for original-key retry', async (t) => {
  const gate = latch(),
    entered = latch();
  let hold = true;
  const f = await setup(t, {
    beforeReply: async (r) => {
      if (hold && r.type === 'source_revoke') {
        entered.release();
        await gate.promise;
      }
    },
  });
  await f.control.establish('1');
  await f.control.idle();
  t.mock.timers.enable({ apis: ['setTimeout'] });
  await f.control.stop('1');
  await entered.promise;
  t.mock.timers.tick(30000);
  await f.control.idle();
  assert.equal(f.store.snapshot().sourceControl.entries['1'].stopped, true);
  const original = Object.values(
    f.store.snapshot().sourceControl.revocations,
  )[0];
  assert.equal(original.receipt, null);
  // A new connection recreates scheduler backoff; same op/body stays in ledger.
  gate.release();
  hold = false;
  assert.equal(original.id, f.requests.at(-1).operation_id);
});

test('Revoke cannot be confirmed by an outbox acknowledgment without IAM lineage_stopped', async (t) => {
  const f = await setup(t, {
    resign: true,
    beforeReply: (r, v) => {
      if (r.type === 'source_revoke') v.lineage_stopped = false;
    },
  });
  await f.control.establish('1');
  await f.control.idle();
  await f.control.stop('1');
  await f.control.idle();
  assert.equal(
    Object.values(f.store.snapshot().sourceControl.revocations)[0].receipt,
    null,
  );
});

test('two lane ceiling holds when refreshes race a revocation', async (t) => {
  let armed = false,
    active = 0,
    maximum = 0;
  const gate = latch(),
    entered = latch();
  const f = await setup(t, {
    beforeReply: async (r) => {
      if (!armed) return;
      active++;
      maximum = Math.max(maximum, active);
      if (r.type === 'source_get') entered.release();
      await gate.promise;
      active--;
    },
  });
  await f.control.establish('1');
  await f.control.idle();
  armed = true;
  const regular = f.control.refresh('1');
  await entered.promise;
  await f.control.stop('1');
  const duplicates = Promise.all(
    Array.from({ length: 20 }, () => f.control.refresh('1')),
  );
  // Test timeout APIs remain real here; observe both requests at the peer.
  await waitFor(() => active === 2, 'both reserved lanes');
  assert.equal(maximum, 2);
  gate.release();
  await regular;
  await duplicates;
  assert.equal(f.store.snapshot().sourceControl.entries['1'].observation, null);
});

test('terminal Get crossing the native witness deadline cannot seed successor issuance', async (t) => {
  const signer = publicationFixture();
  let clock = signer.now(),
    armed = false;
  signer.now = () => clock;
  const f = await setup(t, {
    signer,
    beforeReply: (r) => {
      if (armed && r.type === 'source_get') clock += 30000;
    },
  });
  await f.control.establish('1');
  await f.control.idle();
  const head = f.store.snapshot().sourceControl.entries['1'].head;
  f.peer.facts.get(head).state = 'expired';
  f.peer.facts.get(head).revision = '8';
  armed = true;
  await f.control.refresh('1');
  assert.equal(f.store.snapshot().sourceControl.entries['1'].stopped, false);
  assert.equal(f.store.snapshot().sourceControl.entries['1'].observation, null);
  assert.equal(
    f.requests.filter((r) => r.type === 'source_register').length,
    1,
  );
  armed = false;
  await f.control.refresh('1');
  assert.equal(
    f.store.snapshot().sourceControl.entries['1'].observation.source.state,
    'expired',
  );
  assert.equal(f.requests.filter((r) => r.type === 'source_revoke').length, 0);
});
