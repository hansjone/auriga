import test from 'node:test';
import assert from 'node:assert/strict';
import { generateKeyPairSync, randomUUID, sign } from 'node:crypto';
import { Buffer } from 'node:buffer';
import { canonical } from '../../src/shared/protocol.js';
import { createSharedRuntime } from '../../src/shared/runtime.js';

function fixture() {
  const key = generateKeyPairSync('ed25519');
  const keys = new Map([['test', key.publicKey]]);
  const physicalInstanceId = randomUUID();
  const binding = {
    tenantId: randomUUID(),
    memberId: randomUUID(),
    placementId: randomUUID(),
    physicalInstanceId,
    mode: 'shared',
    epoch: 1,
    revision: 1,
  };
  const platformSessionId = randomUUID();
  const authGeneration = 1;
  const rows = new Map();
  let resolves = 0;
  let lookups = 0;
  let clock = 1000;
  let ms = 0;
  const ports = {
    async resolve() {
      resolves++;
      await new Promise((r) => setTimeout(r, 5));
      return {
        active: true,
        binding: { ...binding },
        platformSessionId,
        authGeneration,
        authorizationSetDigest: 'sha256:' + 'a'.repeat(64),
      };
    },
    async lookup(id) {
      lookups++;
      await new Promise((r) => setTimeout(r, 5));
      const row = rows.get(id);
      if (!row) {
        const error = new Error('missing');
        error.code = 'common.not_found';
        throw error;
      }
      return { ...row };
    },
    async register(b) {
      rows.set(b.sessionId, b);
      return b;
    },
    async authorizeTask() {
      return { active: true, credentialVersion: 1 };
    },
    async resourceView(b) {
      return {
        schemaVersion: 1,
        binding: b,
        source: 'platform',
        viewId: randomUUID(),
        authorizationSetDigest: 'sha256:' + 'a'.repeat(64),
        resources: [],
      };
    },
  };
  const runtime = createSharedRuntime({
    ports,
    identityKeys: keys,
    capabilityKeys: keys,
    physicalInstanceId,
    now: () => clock,
    clockMs: () => ms,
  });
  const signed = (c) => {
    const input = [
      { alg: 'EdDSA', kid: 'test', typ: 'auriga-shared-identity+jwt' },
      c,
    ]
      .map((v) => Buffer.from(canonical(v)).toString('base64url'))
      .join('.');
    return (
      input +
      '.' +
      sign(null, Buffer.from(input), key.privateKey).toString('base64url')
    );
  };
  async function peer() {
    return runtime.admit(
      signed({
        schemaVersion: 2,
        issuer: 'auriga.edge',
        audience: 'auriga.shared.ingress',
        binding,
        platformSessionId,
        method: 'POST',
        canonicalPath: '/shared',
        issuedAt: clock,
        expiresAt: clock + 60,
        jti: randomUUID(),
      }),
      { method: 'POST', canonicalPath: '/shared' },
      (principal) => ({ principal }),
    );
  }
  return {
    runtime,
    binding,
    peer,
    rows,
    platformSessionId,
    authGeneration,
    counts: () => ({ resolves, lookups }),
    advanceMs: (delta) => {
      ms += delta;
    },
  };
}

test('N concurrent refresh/authorize coalesce resolve to ≤2 in one second', async () => {
  const f = fixture();
  const peer = await f.peer();
  const sessionId = randomUUID();
  f.rows.set(sessionId, {
    ...f.binding,
    sessionId,
    platformSessionId: f.platformSessionId,
    authGeneration: f.authGeneration,
  });
  const before = f.counts().resolves;
  await Promise.all(Array.from({ length: 5 }, () => f.runtime.refresh(peer)));
  await Promise.all(
    Array.from({ length: 5 }, () =>
      f.runtime.authorize(peer, 'session/page', sessionId),
    ),
  );
  const after = f.counts();
  assert.ok(
    after.resolves - before <= 2,
    `resolve calls ${after.resolves - before} exceeded 2`,
  );
  // Concurrent authorize coalesces lookup; sequential waves may look up again.
  assert.ok(after.lookups <= 5, `lookup calls ${after.lookups} exceeded 5`);
});

test('revoke invalidates reuse so the next check fails immediately', async () => {
  const f = fixture();
  const peer = await f.peer();
  await f.runtime.refresh(peer);
  const afterWarm = f.counts().resolves;
  assert.ok(afterWarm >= 1);
  f.advanceMs(10);
  await f.runtime.refresh(peer);
  assert.equal(f.counts().resolves, afterWarm, 'TTL reuse must skip resolve');
  f.runtime.revoke({
    schemaVersion: 1,
    tenantId: f.binding.tenantId,
    memberId: f.binding.memberId,
    placementId: f.binding.placementId,
    epoch: f.binding.epoch,
    revision: f.binding.revision,
    authGeneration: f.authGeneration + 1,
    reason: 'logout',
    revokedAt: 1000,
  });
  await assert.rejects(() => f.runtime.refresh(peer));
});

test('two peers of the same member both see a created session', async () => {
  const f = fixture();
  const a = await f.peer();
  const b = await f.peer();
  const host = {
    prepare: async (id) => ({ id }),
    publish: async () => {},
    discard: async () => {},
  };
  const created = await f.runtime.create(a, host, {});
  const seen = await f.runtime.authorize(b, 'session/page', created.sessionId);
  assert.equal(seen.sessionId, created.sessionId);
  assert.deepEqual(
    f.runtime
      .bindings(b.principal)
      .filter((row) => row.resource.kind === 'session')
      .map((row) => row.resource.id),
    [created.sessionId],
  );
});
