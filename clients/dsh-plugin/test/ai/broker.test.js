import test from 'node:test';
import assert from 'node:assert/strict';
import { createRefreshBroker } from '../../src/ai/refresh-broker.js';
/* global structuredClone */

function fixture() {
  let now = 1000000;
  let state = {
    credential: {
      accessToken: 'platform-old',
      refreshToken: 'r0',
      claims: { tid: 'tenant', sub: 'owner', did: 'device', exp: 1001 },
    },
  };
  const requests = [];
  let failSave = false,
    failExchange;
  const vault = {
    snapshot: () => structuredClone(state),
    async update(change) {
      const next = structuredClone(state);
      await change(next);
      if (
        failSave &&
        next.credential?.refreshToken !== state.credential?.refreshToken
      )
        throw Error('disk secret');
      state = next;
    },
  };
  const platform = {
    async request(_path, { body }) {
      requests.push(body);
      if (failExchange) throw failExchange;
      assert.equal(body.refresh_token, `r${requests.length - 1}`);
      return {
        token_type: 'Bearer',
        scope: 'auriga.connect',
        refresh_token: `r${requests.length}`,
        access_token: body.resource ? 'ai-new' : 'platform-new',
        expires_in: 120,
      };
    },
    async verifyDevice() {
      return { ...state.credential.claims, exp: now / 1000 + 120 };
    },
    async verifyAi() {
      return { ...state.credential.claims, exp: now / 1000 + 120 };
    },
  };
  const broker = createRefreshBroker({
    vault,
    platform,
    config: {
      platformOrigin: 'https://portal.example.com',
      issuer: 'https://portal.example.com',
      clientId: 'auriga-dsh',
    },
    now: () => now,
  });
  return {
    broker,
    vault,
    platform,
    requests,
    advance: (ms) => {
      now += ms;
    },
    failSave: () => {
      failSave = true;
    },
    failExchange: (error) => {
      failExchange = error;
    },
  };
}

test('broker: cross-resource serial rotation and same-resource single flight', async () => {
  const { broker, vault, requests } = fixture();
  const results = await Promise.all([
    broker.getAiAccess(),
    broker.getPlatformAccess(),
    broker.getAiAccess(),
    broker.getPlatformAccess(),
  ]);
  assert.equal(requests.length, 2);
  assert.equal(requests[0].resource, 'urn:auriga:ai-gateway');
  assert.equal(requests[1].refresh_token, 'r1');
  assert.equal(results[0], results[2]);
  assert.equal(results[1], 'platform-new');
  assert.equal(vault.snapshot().credential.refreshToken, 'r2');
  assert.equal(vault.snapshot().credential.accessToken, 'platform-new');
  assert.doesNotMatch(JSON.stringify(vault.snapshot()), /ai-new/);
});

test('broker: offline probe preserves refresh token then automatically recovers after expiry', async () => {
  const f = fixture();
  let online = false;
  f.platform.probe = async () => {
    if (!online) throw Error('offline');
  };
  await assert.rejects(f.broker.getPlatformAccess(), /UPSTREAM_UNAVAILABLE/);
  assert.equal(f.vault.snapshot().credential.refreshPending, undefined);
  assert.equal(f.broker.status.reauthenticationRequired, false);
  assert.equal(f.requests.length, 0);
  online = true;
  assert.equal(await f.broker.getPlatformAccess(), 'platform-new');
  assert.equal(f.vault.snapshot().credential.refreshToken, 'r1');
});

test('broker: refused connection is retryable but ambiguous reset remains fenced', async () => {
  const f = fixture();
  f.failExchange(
    Object.assign(Error('connect refused'), {
      cause: { code: 'ECONNREFUSED' },
    }),
  );
  await assert.rejects(f.broker.getPlatformAccess(), /UPSTREAM_UNAVAILABLE/);
  assert.equal(f.vault.snapshot().credential.refreshPending, false);
  assert.equal(f.broker.status.reauthenticationRequired, false);
  f.failExchange(
    Object.assign(Error('response lost'), { cause: { code: 'ECONNRESET' } }),
  );
  await assert.rejects(f.broker.getPlatformAccess(), /REAUTH_REQUIRED/);
  assert.equal(f.vault.snapshot().credential.refreshPending, true);
});

test('broker: AI rotation preserves valid platform access and freezes old lease deadline', async () => {
  const { broker, vault, advance, requests } = fixture();
  await vault.update((s) => {
    s.credential.claims.exp = 9999;
  });
  const first = await broker.getAiAccess();
  assert.equal(await broker.getPlatformAccess(), 'platform-old');
  advance(100000);
  const next = await broker.getAiAccess();
  assert.equal(next.expiresAt - first.expiresAt, 100000);
  assert.equal(first.signal.aborted, false);
  assert.equal(requests.length, 2);
  broker.invalidate('signed_out');
  assert.equal(first.signal.aborted, true);
  assert.equal(next.signal.aborted, true);
});

for (const mode of ['unknown', 'save'])
  test(`broker: ${mode} failure never publishes or replays after restart`, async () => {
    const f = fixture();
    if (mode === 'save') f.failSave();
    else f.failExchange(Error('secret response lost'));
    await assert.rejects(f.broker.getAiAccess(), /REAUTH_REQUIRED/);
    assert.equal(f.vault.snapshot().credential.refreshPending, true);
    await assert.rejects(f.broker.getPlatformAccess(), /REAUTH_REQUIRED/);
    await assert.rejects(f.broker.getAiAccess(), /REAUTH_REQUIRED/);
    const restarted = createRefreshBroker({
      vault: f.vault,
      platform: {},
      config: {},
    });
    await assert.rejects(restarted.getPlatformAccess(), /REAUTH_REQUIRED/);
    assert.equal(f.requests.length, 1);
  });

test('broker: explicit AI permission denial preserves Team credential and blocks repeated AI exchange', async () => {
  const f = fixture();
  await f.vault.update((s) => {
    s.credential.claims.exp = 9999;
  });
  f.failExchange(
    Object.assign(Error('secret'), { status: 400, code: 'invalid_grant' }),
  );
  await assert.rejects(f.broker.getAiAccess(), /AI_NOT_AUTHORIZED/);
  assert.equal(await f.broker.getPlatformAccess(), 'platform-old');
  await assert.rejects(f.broker.getAiAccess(), /AI_NOT_AUTHORIZED/);
  assert.equal(f.vault.snapshot().credential.refreshPending, false);
  assert.equal(f.requests.length, 1);
});

test('broker: sleep expires cache; sign-out closes pending and queued acquisition', async () => {
  const f = fixture();
  const old = await f.broker.getAiAccess();
  f.advance(300000);
  const fresh = await f.broker.getAiAccess();
  assert.ok(fresh.expiresAt > old.expiresAt);
  const pending = f.broker.getPlatformAccess();
  f.broker.invalidate('signed_out');
  await assert.rejects(pending, /REAUTH_REQUIRED/);
  assert.equal(fresh.signal.aborted, true);
  assert.equal(f.requests.length, 2);
});

test('broker: an in-flight response cannot publish after sign-out; actual native login clears fence and old leases', async () => {
  const f = fixture();
  const old = await f.broker.getAiAccess();
  const entered = Promise.withResolvers(),
    release = Promise.withResolvers();
  f.platform.request = async () => {
    entered.resolve();
    return release.promise;
  };
  const pending = f.broker.getPlatformAccess();
  await entered.promise;
  f.broker.invalidate('signed_out');
  release.resolve({
    token_type: 'Bearer',
    scope: 'auriga.connect',
    access_token: 'late',
    refresh_token: 'late-refresh',
  });
  await assert.rejects(pending, { code: 'REAUTH_REQUIRED' });
  assert.equal(f.vault.snapshot().credential.refreshToken, 'r1');
  assert.equal(old.signal.aborted, true);
  await f.broker.commitLogin(() =>
    f.vault.update((s) => {
      s.credential = {
        ...s.credential,
        refreshPending: false,
        refreshToken: 'fresh-login',
        accessToken: 'fresh-platform',
        claims: { ...s.credential.claims, exp: 9999 },
      };
    }),
  );
  assert.equal(await f.broker.getPlatformAccess(), 'fresh-platform');
  assert.equal(f.broker.status.reauthenticationRequired, false);
});

test('broker: pending-fence persistence failure sends no exchange', async () => {
  const f = fixture();
  f.vault.update = async () => {
    throw Error('disk failed');
  };
  await assert.rejects(f.broker.getAiAccess(), { code: 'REAUTH_REQUIRED' });
  assert.equal(f.requests.length, 0);
});

for (const field of [
  'sub',
  'tid',
  'did',
  'platformOrigin',
  'issuer',
  'clientId',
])
  test(`broker: ${field} change invalidates cached AI lease before transport`, async () => {
    const f = fixture();
    const old = await f.broker.getAiAccess();
    await f.vault.update((s) => {
      if (['sub', 'tid', 'did'].includes(field))
        s.credential.claims[field] = 'other';
      else s.credential[field] = 'https://other.example.com';
    });
    await assert.rejects(f.broker.getAiAccess(), { code: 'REAUTH_REQUIRED' });
    assert.equal(old.signal.aborted, true);
    assert.equal(f.requests.length, 1);
  });
