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
    ...(state === 'signed_out' ? { reason: 'user_logout' } : {}),
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

test('startup before shell initialization and transient platform states preserve an active shared view', async (t) => {
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
  const options = {
    now: Date.now,
    time: () => ({ bootId: id(9), monotonicMillis: Date.now() }),
  };
  const first = new Supervisor(f.home, tree, f.trust, options);
  await first.prepare();
  assert.equal(first.guardian.read().guardian.state, 'active');
  // Matches launch(): prepare precedes creation of shellAuth. Absence is not a
  // signed_out event and must not generate a persistent disable receipt.
  const supervisor = new Supervisor(f.home, tree, f.trust, options);
  let shell;
  supervisor.shellSnapshot = () => shell;
  await supervisor.prepare();
  assert.equal(supervisor.guardian.read().guardian.state, 'active');
  assert.equal(supervisor.platformPaused, true);
  stopped = false;
  for (const state of [
    'unreachable',
    'reauth_required',
    'authenticating',
    'signed_out',
  ]) {
    shell = { state };
    const result = await supervisor.reconcile();
    assert.ok(!result.restart, state);
    assert.equal(supervisor.guardian.read().guardian.state, 'active', state);
    assert.equal(supervisor.guardian.cleared(), false, state);
    assert.equal(supervisor.platformPaused, true);
    assert.equal(stops, 0);
  }
  shell = {
    state: 'authenticated',
    identity: { tenantId: id(1), memberId: id(5), deviceId: id(3) },
    expiresAt: new Date(Date.now() + 30000).toISOString(),
  };
  assert.ok(!(await supervisor.reconcile()).restart);
  assert.equal(supervisor.platformPaused, false);
  assert.equal(stops, 0);
});

for (const state of ['revoked', 'identity_changed'])
  test(`shell ${state} still disables the shared view and requests a new DSH session`, async (t) => {
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
      time: () => ({ bootId: id(9), monotonicMillis: Date.now() }),
    });
    await supervisor.prepare();
    stopped = false;
    supervisor.shellSnapshot = () =>
      state === 'revoked'
        ? { state: 'revoked' }
        : {
            state: 'authenticated',
            identity: { tenantId: id(1), memberId: id(8), deviceId: id(3) },
            expiresAt: new Date(Date.now() + 30000).toISOString(),
          };
    assert.equal((await supervisor.reconcile()).restart, true);
    assert.equal(stops, 1);
    assert.equal(supervisor.guardian.read().guardian.state, 'private_only');
    assert.ok(supervisor.guardian.cleared());
  });
