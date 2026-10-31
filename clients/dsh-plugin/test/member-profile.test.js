import test from 'node:test';
import assert from 'node:assert/strict';
import { setTimeout as delay } from 'node:timers/promises';
import { runtimePeer } from './local-runtime-peer.js';
import {
  loginMemberName,
  deviceMemberName,
  memberName,
} from '../src/local/member-profile.js';

test('member profile: restored native owner reads the current name and persists it for offline display', async (t) => {
  const subject = '00000000-0000-4000-8000-000000000002';
  const { runtime, request } = await runtimePeer(t, {
    claims: { sub: subject },
    memberDirectory: {
      items: [{ ownerUserId: subject, ownerDisplayName: 'Alice' }],
    },
  });
  runtime.status(request);
  const deadline = Date.now() + 3000;
  while (!runtime.vault.snapshot().credential.displayName) {
    assert.ok(
      Date.now() < deadline,
      'authenticated member name was not persisted',
    );
    await delay(5);
  }
  assert.equal(runtime.status(request).account.displayName, 'Alice');
  assert.equal(runtime.vault.snapshot().credential.displayName, 'Alice');
});

test('member profile: IAM /me is bound to the verified local login subject', async () => {
  const jar = {};
  const signal = new AbortController().signal;
  const platform = {
    async request(path, options) {
      assert.equal(path, '/api/iam/v1/me');
      assert.equal(options.jar, jar);
      assert.equal(options.signal, signal);
      return { account: { id: 'alice', displayName: ' Alice ' } };
    },
  };
  assert.equal(await loginMemberName(platform, jar, 'alice', signal), 'Alice');
  await assert.rejects(
    loginMemberName(platform, jar, 'bob', signal),
    /identity changed/,
  );
});

test('member profile: directory pagination uses device auth and selects only the owner', async () => {
  let calls = 0;
  const platform = {
    async request(path, options) {
      assert.equal(options.token, 'device');
      assert.equal(options.jar, undefined);
      if (++calls === 1)
        return {
          items: [{ ownerUserId: 'bob', ownerDisplayName: 'Bob' }],
          nextCursor: 'next',
        };
      assert.match(path, /cursor=next$/);
      return { items: [{ ownerUserId: 'alice', ownerDisplayName: 'Alice' }] };
    },
  };
  assert.equal(
    await deviceMemberName(
      platform,
      'device',
      'alice',
      new AbortController().signal,
    ),
    'Alice',
  );
  assert.equal(calls, 2);
});

test('member profile: absent, malformed, cyclic and unavailable directory never fabricate a name', async () => {
  const signal = new AbortController().signal;
  assert.equal(
    await deviceMemberName(
      { request: async () => ({ items: [] }) },
      'device',
      'alice',
      signal,
    ),
    undefined,
  );
  await assert.rejects(
    deviceMemberName(
      { request: async () => ({ items: [], nextCursor: 'same' }) },
      'device',
      'alice',
      signal,
    ),
    /cursor/,
  );
  await assert.rejects(
    deviceMemberName(
      {
        request: async () => {
          throw Error('unavailable');
        },
      },
      'device',
      'alice',
      signal,
    ),
    /unavailable/,
  );
  for (const value of ['', '  ', null, 3, 'x'.repeat(257), 'Alice\nBob'])
    assert.equal(memberName(value), undefined);
});
