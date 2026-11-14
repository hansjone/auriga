import { OWNER, OTHER } from './fixtures.mjs';
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { PlatformError } from '../platform.mjs';
import { valid } from '../schema.mjs';

import { fixture, command, login } from './fixtures.mjs';

test('device login opens only base HTTPS verification page, stores secrets privately and pushes snapshot', async () => {
  const f = fixture(),
    changes = [];
  f.broker.on('changed', (s) => changes.push(s));
  await f.broker.initialize();
  await login(f);
  assert.equal(f.opened[0], 'https://example.com/device');
  assert.equal(f.broker.snapshot().state, 'authenticated');
  assert.ok(changes.every((s) => valid('ShellAuthSnapshot', s)));
  assert.ok(!JSON.stringify(changes).includes(f.tokens.access_token));
  assert.ok(!JSON.stringify(f.logs).includes(f.tokens.refresh_token));
  assert.equal(f.saved.at(-1).tokens.refresh_token, f.tokens.refresh_token);
  await f.broker.close();
});

test('device authorization rejects absent or untrusted verification URI and never falls back to the complete URI', async () => {
  for (const verification_uri of [
    undefined,
    'file:///device',
    'javascript:void(0)',
    'https://other.example.com/device',
    'https://example.com/device?code=test',
    'https://example.com/device#code',
    'https://user@example.com/device',
  ]) {
    const f = fixture();
    const begin = f.platform.begin;
    f.platform.begin = async () => ({
      ...(await begin()),
      verification_uri,
      verification_uri_complete: 'https://example.com/device?user_code=test',
    });
    await login(f);
    assert.equal(f.broker.state, 'unreachable');
    assert.deepEqual(f.opened, []);
    assert.equal(f.broker.record.tokens, undefined);
    await f.broker.close();
  }
  const f = fixture();
  const begin = f.platform.begin;
  f.platform.begin = async () => ({
    ...(await begin()),
    verification_uri_complete: 'https://other.example.com/device',
  });
  await login(f);
  assert.deepEqual(f.opened, ['https://example.com/device']);
  await f.broker.close();
});
test('pending and slow_down are bounded, no workspace endpoint is called', async () => {
  let attempts = 0;
  const f = fixture({
    poll: async () => {
      if (++attempts === 1)
        throw new PlatformError(400, 'authorization_pending');
      if (attempts === 2) throw new PlatformError(400, 'slow_down');
      return f.tokens;
    },
  });
  const waits = [];
  f.broker.wait = async (ms) => {
    waits.push(ms);
  };
  await login(f);
  assert.deepEqual(waits, [5000, 5000, 10000]);
  assert.equal(f.broker.state, 'authenticated');
  await f.broker.close();
});
test('cancel after token request discards and revokes late credential', async () => {
  let release, entered;
  const polled = new Promise((resolve) => {
    entered = resolve;
  });
  const f = fixture({
    poll: () => {
      entered();
      return new Promise((resolve) => {
        release = resolve;
      });
    },
  });
  f.broker.begin(OWNER);
  await polled;
  f.broker.cancel();
  release(f.tokens);
  await Promise.all([...f.broker.inflight]);
  assert.equal(f.broker.state, 'signed_out');
  assert.equal(f.broker.record.tokens, undefined);
  assert.deepEqual(
    new Set(f.revoked),
    new Set([f.tokens.access_token, f.tokens.refresh_token]),
  );
  await f.broker.close();
});
test('cancel cannot affect another window; skip cannot revoke current authenticated identity', async () => {
  const f = fixture();
  f.broker.begin(OWNER);
  await assert.rejects(
    f.broker.command(command(f.broker, 'cancel_login'), OTHER),
    /common.conflict/,
  );
  await Promise.all([...f.broker.inflight]);
  await assert.rejects(
    f.broker.pageCommand(command(f.broker, 'skip_login'), OWNER),
    /common.conflict/,
  );
  assert.equal(f.broker.state, 'authenticated');
  await f.broker.close();
});
test('logout stops local access before remote revoke; durable outbox survives failure and requires manual login', async () => {
  const f = fixture();
  await login(f);
  f.platform.revoke = async () => {
    throw new Error('offline');
  };
  const logout = f.broker.logout();
  assert.throws(() => f.broker.guard(), /common.unauthenticated/);
  await logout;
  await Promise.all([...f.broker.inflight]);
  assert.equal(f.saved.at(-1).manualLoginRequired, true);
  assert.equal(f.saved.at(-1).revocations.length, 2);
  assert.equal(f.broker.snapshot().reason, 'user_logout');
  const revocations = f.logs.filter(
    (event) => event.stage === 'session_revoke',
  );
  assert.ok(revocations.some((event) => event.result === 'committed'));
  assert.ok(
    revocations.every((event) => event.tenantId === f.identity.tenantId),
  );
  f.platform.revoke = async (token) => {
    f.revoked.push(token);
  };
  await f.broker.check();
  assert.equal(f.broker.record.revocations.length, 0);
  assert.equal(f.broker.snapshot().state, 'signed_out');
  assert.ok(
    f.logs.some(
      (event) =>
        event.stage === 'session_revoke' && event.result === 'propagated',
    ),
  );
  await f.broker.close();
});
test('transient platform failure becomes unreachable; explicit source disable revokes and clears identity', async () => {
  const f = fixture();
  await login(f);
  f.platform.profile = async () => {
    throw new PlatformError(503, 'common.unavailable');
  };
  await f.broker.check();
  assert.equal(f.broker.snapshot().state, 'unreachable');
  assert.throws(() => f.broker.guard(), /common.unauthenticated/);
  f.platform.refresh = async () => {
    throw new PlatformError(403, 'iam.provider_disabled');
  };
  await f.broker.check();
  assert.equal(f.broker.snapshot().state, 'revoked');
  assert.equal(f.broker.snapshot().reason, 'source_disabled');
  assert.equal(f.broker.snapshot().identity, undefined);
  await f.broker.close();
});
test('informational department absence does not revoke; latest enterprise material is read each time and fenced on logout', async () => {
  const f = fixture();
  await login(f);
  const first = await f.broker.enterprise(),
    second = await f.broker.enterprise();
  assert.notEqual(first.token, second.token);
  let release;
  f.platform.enterprise = () =>
    new Promise((resolve) => {
      release = resolve;
    });
  const pending = f.broker.enterprise();
  await f.broker.logout();
  release(second);
  await assert.rejects(pending);
  await f.broker.close();
});
test('command ids are idempotent, owner/content conflicts fail, snapshot discovery is read-only', async () => {
  const f = fixture();
  const cmd = command(f.broker, 'snapshot');
  assert.deepEqual(
    await f.broker.command(cmd, OWNER),
    await f.broker.command(cmd, OWNER),
  );
  await assert.rejects(
    f.broker.command({ ...cmd, action: 'logout' }, OWNER),
    /common.conflict/,
  );
  await assert.rejects(f.broker.command(cmd, OTHER), /common.conflict/);
  await assert.rejects(
    f.broker.command(
      {
        ...command(f.broker, 'logout'),
        shellEpoch: '00000000-0000-0000-0000-000000000000',
      },
      OWNER,
    ),
    /common.conflict/,
  );
  await f.broker.close();
});
test('expired monotonic credential cannot become usable by rolling wall clock back', async () => {
  const f = fixture();
  let mono = 0;
  f.broker.mono = () => mono;
  await login(f);
  const until = f.broker.tokenMonoUntil;
  f.broker.now = () => Date.now() - 86400000;
  mono = until;
  assert.equal(f.broker.snapshot().state, 'reauth_required');
  assert.throws(() => f.broker.guard());
  await f.broker.close();
});
