/* global structuredClone */
import test from 'node:test';
import assert from 'node:assert/strict';
import { shellRuntimePeer } from './shell-runtime-peer.js';
import {
  memberName,
  memberAvatar,
  memberProfileBinding,
} from '../src/local/member-profile.js';
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
      avatarUrl: 'https://profile.example.com/alice.png',
    })),
  });
  t.mock.method(globalThis, 'fetch', () => assert.fail('no directory lookup'));
  await nextTurn();
  assert.equal(f.runtime.status(f.request).account.displayName, 'Alice');
  assert.equal(
    f.runtime.status(f.request).account.avatarUrl,
    'https://profile.example.com/alice.png',
  );
  assert.equal(f.runtime.baseTools.getState().displayName, 'Alice');
  assert.equal(f.runtime.vault.snapshot().credential, undefined);
});
test('member avatars accept only bounded absolute HTTP URLs without credential or active content', () => {
  for (const value of [
    'https://profile.example.com/alice.png',
    'http://192.0.2.10/alice.png',
  ])
    assert.equal(memberAvatar(value), value);
  for (const value of [
    null,
    2,
    '',
    '//example.com/avatar',
    'javascript:alert(1)',
    'data:image/png;base64,x',
    'file:///avatar',
    'https://alice:secret@example.com/a',
    'https://example.com/a#fragment',
    'https://example.com:70000/a',
    'https://example.com/<img>',
    'https://example.com/a b',
    'https://example.com/' + 'a'.repeat(2048),
  ])
    assert.equal(memberAvatar(value), undefined);
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

test('profile correlation changes with shell epoch, revision, account and UAC generation', () => {
  const snapshot = {
    state: 'authenticated',
    shellEpoch: randomUUID(),
    revision: 1,
    identity: {
      tenantId: randomUUID(),
      memberId: randomUUID(),
      deviceId: randomUUID(),
      providerId: randomUUID(),
      empNo: '001Ab',
      uacSessionBinding: {
        providerId: randomUUID(),
        sourceSessionId: randomUUID(),
        authGeneration: 1,
      },
    },
  };
  const first = memberProfileBinding(snapshot);
  assert.match(first, /^[0-9a-f]{64}$/);
  assert.equal(first, memberProfileBinding(structuredClone(snapshot)));
  for (const mutate of [
    (s) => {
      s.shellEpoch = randomUUID();
    },
    (s) => {
      s.revision++;
    },
    (s) => {
      s.identity.memberId = randomUUID();
    },
    (s) => {
      s.identity.uacSessionBinding.authGeneration++;
    },
    (s) => {
      s.identity.empNo = '002Cd';
    },
  ]) {
    const next = structuredClone(snapshot);
    mutate(next);
    assert.notEqual(first, memberProfileBinding(next));
  }
  assert.equal(
    memberProfileBinding({ ...snapshot, state: 'signed_out' }),
    undefined,
  );
});

test('runtime emits the same profile binding for shell and local-UAC association from one snapshot', async (t) => {
  const f = await shellRuntimePeer(t, {
    configure(c) {
      const sourceBinding = c.identity.uacSessionBinding;
      delete c.identity.uacSessionBinding;
      c.identity.empNo = '001Ab';
      c.identity.uacSessionBinding = sourceBinding;
      profilePeer((peer) => ({
        memberId: peer.identity.memberId,
        displayName: 'Alice',
      }))(c);
    },
  });
  await nextTurn();
  const initial = f.runtime.status(f.request).account.profileBinding;
  assert.match(initial, /^[0-9a-f]{64}$/);
  assert.deepEqual(f.runtime.platformIdentity(), {
    empNo: '001Ab',
    profileBinding: initial,
  });
  f.connection.transition('authenticated', {
    uacSessionBinding: {
      ...f.connection.identity.uacSessionBinding,
      authGeneration: 2,
    },
  });
  await nextTurn();
  const changed = f.runtime.status(f.request).account.profileBinding;
  assert.notEqual(changed, initial);
  assert.equal(f.runtime.platformIdentity().profileBinding, changed);
  f.connection.logout();
  assert.equal(f.runtime.platformIdentity(), undefined);
});
