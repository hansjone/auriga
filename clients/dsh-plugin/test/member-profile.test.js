import test from 'node:test';
import assert from 'node:assert/strict';
import { shellRuntimePeer } from './shell-runtime-peer.js';
import { memberName } from '../src/local/member-profile.js';
import { setImmediate as nextTurn } from 'node:timers/promises';
import { randomUUID } from 'node:crypto';
function profilePeer(reply) {
  return (c) => {
    const original = c.ipc.call.bind(c.ipc);
    c.ipc.call = async (message) =>
      message.call?.operation === 'profile_read'
        ? {
            requestId: message.requestId,
            operation: 'profile_read',
            output: { statusCode: 200, body: await reply(c) },
          }
        : original(message);
  };
}
test('member name uses the fixed profile operation, never a plugin credential or directory fetch', async (t) => {
  const f = await shellRuntimePeer(t, {
    configure: profilePeer((c) => ({
      memberId: c.identity.memberId,
      displayName: ' Alice ',
    })),
  });
  t.mock.method(globalThis, 'fetch', () => assert.fail('no directory lookup'));
  await nextTurn();
  assert.equal(f.runtime.status(f.request).account.displayName, 'Alice');
  assert.equal(f.runtime.baseTools.getState().displayName, 'Alice');
  assert.equal(f.runtime.vault.snapshot().credential, undefined);
});
test('unadmitted requests cannot read the authenticated profile, and foreign member names are rejected', async (t) => {
  const f = await shellRuntimePeer(t, {
    configure: profilePeer(() => ({
      memberId: randomUUID(),
      displayName: 'Other member',
    })),
  });
  await nextTurn();
  assert.equal(f.runtime.status({ headers: {} }).account, null);
  assert.equal(f.runtime.status(f.request).account.displayName, undefined);
  assert.ok(f.runtime.resolve(f.request));
});
test('logout or identity change fences a late profile response', async (t) => {
  const gate = Promise.withResolvers(),
    entered = Promise.withResolvers();
  const f = await shellRuntimePeer(t, {
    configure: profilePeer(async (c) => {
      entered.resolve();
      const memberId = c.identity.memberId;
      await gate.promise;
      return { memberId, displayName: 'Old member' };
    }),
  });
  await entered.promise;
  f.connection.logout();
  gate.resolve();
  await nextTurn();
  assert.equal(f.runtime.status(f.request).account, null);
  assert.equal(f.runtime.baseTools.getState().displayName, undefined);
});
test('unsafe or unavailable profile data never invents a name or changes verified identity', async (t) => {
  for (const value of ['', '  ', null, 3, 'x'.repeat(257), 'Alice\nBob'])
    assert.equal(memberName(value), undefined);
  const f = await shellRuntimePeer(t, {
    configure: profilePeer((c) => ({
      memberId: c.identity.memberId,
      displayName: 'Alice\nBob',
    })),
  });
  await nextTurn();
  assert.equal(f.runtime.status(f.request).account.displayName, undefined);
  assert.equal(
    f.runtime.status(f.request).account.subject,
    f.connection.identity.memberId,
  );
});
