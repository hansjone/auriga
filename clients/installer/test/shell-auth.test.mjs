import { test } from 'node:test';
import assert from 'node:assert/strict';
import { resourceFixture, id } from './resource-fixture.mjs';
import { tempBase } from './helpers.mjs';
import { Supervisor } from '../runtime/supervisor.mjs';
test('shell logout is reconciled by the existing single guardian writer before shared content can be reused', async (t) => {
  const f = resourceFixture(tempBase(t));
  let stopped = true,
    stops = 0;
  const tree = {
    record: { launcher: null, root: null, children: [] },
    isStopped: async () => stopped,
    stopTree: async () => {
      stopped = true;
      stops++;
    },
  };
  const supervisor = new Supervisor(f.home, tree, f.trust, {
    now: Date.now,
    time: () => ({ bootId: id(9), monotonicMillis: Date.now() }),
  });
  let state = 'authenticated';
  supervisor.shellSnapshot = () => ({
    state,
    identity: { tenantId: id(1), memberId: id(5), deviceId: id(3) },
    expiresAt: new Date(Date.now() + 30000).toISOString(),
  });
  await supervisor.prepare();
  assert.equal(supervisor.guardian.read().guardian.state, 'active');
  stopped = false;
  state = 'signed_out';
  const result = await supervisor.reconcile();
  assert.equal(result.restart, true);
  assert.equal(stops, 1);
  assert.equal(supervisor.guardian.read().guardian.state, 'private_only');
  assert.ok(supervisor.guardian.cleared());
  await supervisor.reconcile();
  assert.equal(stops, 1);
});
