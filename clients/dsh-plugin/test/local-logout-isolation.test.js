import test from 'node:test';
import assert from 'node:assert/strict';
import { shellRuntimePeer } from './shell-runtime-peer.js';
import { teamRuntimePeer } from './team-runtime-peer.js';

test('another admitted owner cancelling shell login does not revoke shared identity or Team', async (t) => {
  const f = await teamRuntimePeer(t);
  await f.settled();
  const second = f.runtime.nativeRequest({ principal: { kind: 'owner' } });
  await f.runtime.command(second, 'cancel_login');
  assert.equal(f.runtime.status(f.request).loggedIn, true);
  assert.equal(f.tools.size, 4);
  assert.equal(
    f.connection.calls.some((m) => m.action === 'logout'),
    false,
  );
  await f.runtime.command(second, 'logout');
  assert.equal(f.runtime.resolve(f.request), undefined);
  assert.equal(f.tools.size, 0);
});
test('a forged browser request cannot logout the shared shell identity', async (t) => {
  const f = await shellRuntimePeer(t);
  const before = f.connection.calls.length;
  await assert.rejects(
    f.runtime.command({ headers: { 'x-auriga-native': 'true' } }, 'logout'),
    { code: 'common.forbidden' },
  );
  assert.equal(f.connection.calls.length, before);
  assert.equal(f.runtime.status(f.request).loggedIn, true);
});
test('only admitted authenticated status exposes durable Team delivery and source counts', async (t) => {
  const f = await teamRuntimePeer(t, {
    saved(s) {
      s.deliveries.failed = {
        accepted: false,
        status: 'unsupported',
        reason: 'subagent_origin',
      };
      s.pendingOpens = {
        source: { sessionId: 'trusted-source', expiresAt: Date.now() + 60000 },
      };
      s.openOutcomes = { expired: { status: 'result_unknown' } };
    },
  });
  await f.settled();
  assert.deepEqual(f.runtime.status(f.request).connection, {
    unsupported: 1,
    pendingSourceOpens: 1,
    unknownSourceOpens: 1,
    received: 0,
  });
  assert.equal(f.runtime.status({ headers: {} }).connection, null);
  await f.runtime.command(f.request, 'logout');
  assert.equal(f.runtime.status(f.request).connection, null);
});
