/* global structuredClone */
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFile, mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { sign, generateKeyPairSync } from 'node:crypto';
import { openFileTrustCache } from '../../../src/network/trust/file-cache.js';
import {
  canonicalize,
  parseStrictJson,
} from '../../../src/network/trust/jcs.js';
import {
  openTestTrust as openTrustStore,
  publicationFixture,
  signedKeyset,
  signedAnnouncement,
  testRoots,
} from './keyset-fixtures.js';
import { openTrustStore as openRootTrustStore } from '../../../src/network/trust/store.js';
import { definitions } from '../../../src/generated/network/keyset-schema.js';
import {
  verifyEnvelope,
  verifyEnvelopeWithRefresh,
  signingInput,
} from '../../../src/network/trust/verifier.js';
import {
  privateKeyFromSeed,
  seal,
} from '../../../../../contracts/test/network-lib.mjs';

const vectors = JSON.parse(
  await readFile(
    new URL(
      '../../../../../contracts/network/fixtures/signature-vectors.json',
      import.meta.url,
    ),
    'utf8',
  ),
);
const now = () => Date.parse('2026-10-25T00:01:00Z');
const context = {
  realm: '2',
  node: '64',
  nodeIncarnation: 'inc-64-1',
  epoch: '42',
  channelNonce: 'nonce-ready-0001',
  requestId: 'req-fetch-0001',
};
function config() {
  return {
    schema_version: 1,
    realm: '2',
    keyset_version: '5',
    issued_at: '2026-10-25T00:00:00Z',
    valid_until: '2026-10-26T00:00:00Z',
    keys: vectors.keys.map((k) => ({
      kid: k.kid,
      purpose: k.purpose,
      public_key_spki: k.publicKeySpki,
      alg: 'Ed25519',
      state: 'active',
      not_before: '2026-10-24T00:00:00Z',
      not_after: '2026-10-27T00:00:00Z',
    })),
  };
}
function cache() {
  let value;
  return {
    async load() {
      return structuredClone(value);
    },
    async commit(v) {
      value = structuredClone(v);
    },
  };
}
function envelope(v) {
  const value = JSON.parse(v.canonical);
  value.server_auth.signature = v.signature;
  return value;
}
const code = (expected) => (e) => e.code === expected;

test('an unknown schema keyword rejects an otherwise valid root-signed keyset publication', async () => {
  const schema = definitions.PublicKey.properties.kid;
  schema.futureConstraint = true;
  try {
    await assert.rejects(
      openTrustStore({ cache: cache(), config: config(), now }),
      code('network.key_unavailable'),
    );
  } finally {
    delete schema.futureConstraint;
  }
  await assert.doesNotReject(
    openTrustStore({ cache: cache(), config: config(), now }),
  );
});

test('failed durable key update can recover on a known-version refresh', async () => {
  const disk = cache();
  let fail = false;
  const trust = await openTrustStore({
    cache: {
      load: disk.load,
      commit: async (v) => {
        if (fail) throw new Error('disk failure');
        await disk.commit(v);
      },
    },
    config: config(),
    now,
    refresh: async () => ({ ...config(), keyset_version: '6' }),
  });
  fail = true;
  await assert.rejects(trust.update({ ...config(), keyset_version: '6' }));
  fail = false;
  await trust.refreshFor(vectors.keys[0].kid, '5');
  assert.doesNotThrow(() =>
    trust.key(vectors.keys[0].kid, vectors.keys[0].purpose, '5'),
  );
  assert.deepEqual(trust.diagnostics, {
    refreshAttempts: 1,
    refreshFailures: 0,
    refreshSuccesses: 1,
  });
});

test('expired known keyset refreshes under cooldown and records unsuccessful refreshes', async () => {
  let clock = now();
  let calls = 0;
  const trust = await openTrustStore({
    cache: cache(),
    config: config(),
    now: () => clock,
    refresh: async () => {
      calls++;
      return config();
    },
  });
  clock = Date.parse(config().valid_until);
  await assert.rejects(
    trust.refreshFor(vectors.keys[0].kid, '5'),
    code('network.key_unavailable'),
  );
  await trust.refreshFor(vectors.keys[0].kid, '5');
  assert.equal(calls, 1);
  assert.equal(trust.diagnostics.refreshFailures, 1);
  assert.throws(
    () => trust.key(vectors.keys[0].kid, vectors.keys[0].purpose, '5'),
    code('network.key_unavailable'),
  );
});

for (const v of vectors.vectors) {
  test(`authoritative JCS + Ed25519 vector: ${v.name}`, async () => {
    const trust = await openTrustStore({
      cache: cache(),
      config: config(),
      now,
    });
    const value = envelope(v);
    const ctx = { ...context, requestId: value.server_auth.request_id };
    assert.equal(canonicalize(JSON.parse(v.canonical)), v.canonical);
    assert.equal(verifyEnvelope(value, { trust, context: ctx, now }), true);
    for (const mutate of [
      (x) => (x.server_auth.channel_nonce += 'x'),
      (x) => (x.server_auth.audience.node = '99'),
      (x) => (x.extra = 'unsigned'),
      (x) => (x.server_auth.signature = 'A'.repeat(86)),
    ]) {
      const bad = structuredClone(value);
      mutate(bad);
      assert.throws(
        () => verifyEnvelope(bad, { trust, context: ctx, now }),
        code('network.signature_invalid'),
      );
    }
  });
}

test('trust rejects unknown, revoked, expired, inactive and wrong-algorithm keys; rotation accepts both keys', async () => {
  const value = envelope(vectors.vectors[0]);
  for (const change of [
    (c) => (c.keys = []),
    (c) => (c.keys[0].state = 'revoked'),
    (c) => (c.keys[0].not_after = '2026-10-25T00:00:59Z'),
    (c) => (c.keys[0].not_before = '2026-10-25T00:02:01Z'),
  ]) {
    const c = config();
    change(c);
    const trust = await openTrustStore({ cache: cache(), config: c, now });
    assert.throws(
      () => verifyEnvelope(value, { trust, context, now }),
      code('network.key_unavailable'),
    );
  }
  const c = config();
  c.keys[0].alg = 'RSA';
  await assert.rejects(
    openTrustStore({ cache: cache(), config: c, now }),
    code('network.key_unavailable'),
  );
  const trust = await openTrustStore({ cache: cache(), config: config(), now });
  const pair = generateKeyPairSync('ed25519');
  const replacement = {
    ...config().keys[0],
    kid: 'rotated-business',
    public_key_spki: pair.publicKey
      .export({ format: 'der', type: 'spki' })
      .toString('base64url'),
  };
  await trust.update({
    ...config(),
    keyset_version: '6',
    keys: [...config().keys, replacement],
  });
  assert.ok(trust.key('test-r2-business-1', 'business_delivery', '5'));
  assert.ok(trust.key('rotated-business', 'business_delivery', '6'));
  const rotated = structuredClone(value);
  rotated.server_auth.kid = replacement.kid;
  rotated.server_auth.keyset_version = '6';
  rotated.server_auth.signature = sign(
    null,
    signingInput(rotated),
    pair.privateKey,
  ).toString('base64url');
  assert.equal(verifyEnvelope(value, { trust, context, now }), true);
  assert.equal(verifyEnvelope(rotated, { trust, context, now }), true);
});

test('file cache persists revoked keys/version across close and process-style reopen', async () => {
  const dir = await mkdtemp(join(tmpdir(), 'auriga-trust-'));
  try {
    const file = await openFileTrustCache({ dir });
    try {
      const trust = await openTrustStore({
        cache: file,
        config: config(),
        now,
      });
      await trust.update({
        ...config(),
        keyset_version: '6',
        keys: config().keys.map((k) => ({ ...k, state: 'revoked' })),
      });
      await assert.rejects(openFileTrustCache({ dir }));
    } finally {
      await file.close();
    }
    const reopened = await openFileTrustCache({ dir });
    try {
      const trust = await openTrustStore({ cache: reopened, now });
      await assert.rejects(
        trust.update(config()),
        code('network.key_unavailable'),
      );
      assert.throws(
        () => trust.key(vectors.keys[0].kid, 'business_delivery', '5'),
        code('network.key_unavailable'),
      );
    } finally {
      await reopened.close();
    }
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
});

test('persistent cache survives reopen and refuses rollback, same-version changes, expiry and failed writes', async () => {
  const disk = cache();
  const trust = await openTrustStore({ cache: disk, config: config(), now });
  const newer = {
    ...config(),
    keyset_version: '6',
    keys: config().keys.map((k) => ({ ...k, state: 'revoked' })),
  };
  await trust.update(newer);
  const restored = await openTrustStore({ cache: disk, now });
  assert.throws(
    () => restored.key(vectors.keys[0].kid, 'business_delivery', '5'),
    code('network.key_unavailable'),
  );
  await assert.rejects(
    restored.update(config()),
    code('network.key_unavailable'),
  );
  await assert.rejects(
    restored.update({ ...config(), keyset_version: '6' }),
    code('network.key_unavailable'),
  );
  const expired = await openTrustStore({
    cache: disk,
    now: () => Date.parse('2026-10-26T00:00:01Z'),
  });
  assert.throws(
    () => expired.key(vectors.keys[0].kid, 'business_delivery', '5'),
    code('network.key_unavailable'),
  );
  const failing = await openTrustStore({
    cache: {
      load: disk.load,
      async commit() {
        throw Error('disk full');
      },
    },
    now,
  });
  await assert.rejects(failing.update({ ...config(), keyset_version: '7' }));
  assert.throws(
    () => failing.key(vectors.keys[0].kid, 'business_delivery', '5'),
    code('network.key_unavailable'),
  );
});

test('genuine signatures still reject wrong audience/request/nonce and expired time', async () => {
  const trust = await openTrustStore({ cache: cache(), config: config(), now });
  const value = envelope(vectors.vectors[0]);
  for (const changed of [
    { node: '65' },
    { realm: '3' },
    { nodeIncarnation: 'old' },
    { epoch: '41' },
    { requestId: 'other' },
    { channelNonce: 'other' },
  ]) {
    assert.throws(
      () =>
        verifyEnvelope(value, {
          trust,
          context: { ...context, ...changed },
          now,
        }),
      code('network.signature_invalid'),
    );
  }
  assert.throws(
    () =>
      verifyEnvelope(value, {
        trust,
        context,
        now: () => Date.parse('2026-10-25T00:05:01Z'),
      }),
    code('network.signature_invalid'),
  );
  const unsigned = structuredClone(value);
  delete unsigned.server_auth;
  assert.throws(
    () => verifyEnvelope(unsigned, { trust, context, now }),
    code('network.signature_required'),
  );
});

test('bounded strict JSON rejects duplicate escaped keys, invalid Unicode, unsafe numbers and trailing data', () => {
  for (const text of [
    '{"a":1,"\\u0061":2}',
    '{"x":"\\ud800"}',
    '{"x":1e0}',
    '{"x":9007199254740992}',
    '{}{}',
    '['.repeat(17) + '0' + ']'.repeat(17),
  ]) {
    assert.throws(
      () => parseStrictJson(new TextEncoder().encode(text)),
      code('network.protocol_error'),
    );
  }
  const value = parseStrictJson(
    new TextEncoder().encode('{"__proto__":1,"😀":"é","a":[null,true]}'),
  );
  assert.equal(Object.hasOwn(value, '__proto__'), true);
  assert.equal(canonicalize(value), '{"__proto__":1,"a":[null,true],"😀":"é"}');
});

test('cache hard limit is 24h and same-version reload cannot extend it; missing cache fails closed', async () => {
  let clock = now();
  const c = config();
  const trust = await openTrustStore({
    cache: cache(),
    config: c,
    now: () => clock,
  });
  assert.ok(trust.key(vectors.keys[0].kid, 'business_delivery', '5'));
  clock = Date.parse('2026-10-26T00:00:00Z');
  await trust.update(c);
  assert.throws(
    () => trust.key(vectors.keys[0].kid, 'business_delivery', '5'),
    code('network.key_unavailable'),
  );
  const empty = await openTrustStore({ cache: cache(), now });
  assert.throws(
    () => empty.key(vectors.keys[0].kid, 'business_delivery', '5'),
    code('network.key_unavailable'),
  );
});

function refreshedEnvelope(version = '6', kid = vectors.keys[0].kid) {
  const value = envelope(vectors.vectors[0]);
  value.server_auth.keyset_version = version;
  value.server_auth.kid = kid;
  return seal(value, () => privateKeyFromSeed(vectors.keys[0].testOnlySeedHex));
}

test('higher keyset version refreshes once for concurrent verification and persists before accepting', async () => {
  let calls = 0;
  let request;
  let release;
  const gate = new Promise((resolve) => {
    release = resolve;
  });
  const disk = cache();
  const trust = await openTrustStore({
    cache: disk,
    config: config(),
    now,
    refresh: async (input) => {
      calls++;
      request = input;
      await gate;
      return { ...config(), keyset_version: '6' };
    },
  });
  const value = refreshedEnvelope();
  const checks = Array.from({ length: 12 }, () =>
    verifyEnvelopeWithRefresh(value, { trust, context, now }),
  );
  await Promise.resolve();
  release();
  assert.deepEqual(await Promise.all(checks), Array(12).fill(true));
  assert.equal(calls, 1);
  assert.deepEqual(request, {
    realm: '2',
    minimumVersion: '6',
    kid: vectors.keys[0].kid,
  });
  const restored = await openTrustStore({ cache: disk, now });
  assert.equal(verifyEnvelope(value, { trust: restored, context, now }), true);
});

test('unknown kid refreshes trusted configuration and retries the original signature', async () => {
  let calls = 0;
  const c = config();
  const pair = generateKeyPairSync('ed25519');
  const introduced = {
    ...c.keys[0],
    kid: 'introduced',
    public_key_spki: pair.publicKey
      .export({ format: 'der', type: 'spki' })
      .toString('base64url'),
  };
  const trust = await openTrustStore({
    cache: cache(),
    config: c,
    now,
    refresh: async () => {
      calls++;
      return {
        ...c,
        keyset_version: '6',
        keys: [...c.keys, introduced],
      };
    },
  });
  const value = refreshedEnvelope('5', 'introduced');
  value.server_auth.signature = sign(
    null,
    signingInput(value),
    pair.privateKey,
  ).toString('base64url');
  assert.equal(
    await verifyEnvelopeWithRefresh(value, {
      trust,
      context,
      now,
    }),
    true,
  );
  assert.equal(calls, 1);
});

test('failed, stale or missing refresh stays key_unavailable; repeated failures are bounded and known keys remain usable', async () => {
  for (const refresh of [
    undefined,
    async () => {
      throw Error('unavailable');
    },
    async () => config(),
    async () => ({ ...config(), keyset_version: '4' }),
  ]) {
    let calls = 0;
    const trust = await openTrustStore({
      cache: cache(),
      config: config(),
      now,
      refresh:
        refresh &&
        (async (input) => {
          calls++;
          return refresh(input);
        }),
    });
    for (let n = 0; n < 2; n++) {
      await assert.rejects(
        verifyEnvelopeWithRefresh(refreshedEnvelope(), { trust, context, now }),
        code('network.key_unavailable'),
      );
    }
    assert.equal(calls, refresh ? 1 : 0);
    assert.equal(
      verifyEnvelope(envelope(vectors.vectors[0]), { trust, context, now }),
      true,
    );
  }
});

test('bad signatures do not trigger trust refresh', async () => {
  let calls = 0;
  const trust = await openTrustStore({
    cache: cache(),
    config: config(),
    now,
    refresh: async () => {
      calls++;
      return { ...config(), keyset_version: '6' };
    },
  });
  const value = envelope(vectors.vectors[0]);
  value.message.body.text = 'tampered';
  await assert.rejects(
    verifyEnvelopeWithRefresh(value, { trust, context, now }),
    code('network.signature_invalid'),
  );
  assert.equal(calls, 0);
});

test('successive genuine rotations can refresh immediately without a cooldown failure', async () => {
  let version = 5;
  const trust = await openTrustStore({
    cache: cache(),
    config: config(),
    now,
    refresh: async () => ({ ...config(), keyset_version: String(++version) }),
  });
  for (const v of ['6', '7'])
    assert.equal(
      await verifyEnvelopeWithRefresh(refreshedEnvelope(v), {
        trust,
        context,
        now,
      }),
      true,
    );
  assert.equal(version, 7);
});

test('official announcement persists a blocking high-water before its full publication', async () => {
  const { payload, now } = publicationFixture();
  let saved;
  const cache = {
    async load() {
      return saved;
    },
    async commit(v) {
      saved = structuredClone(v);
    },
  };
  const trust = await openRootTrustStore({
    cache,
    roots: testRoots,
    config: signedKeyset(payload),
    realm: '2',
    requireAnnouncement: true,
    now,
  });
  assert.throws(() => trust.assertUsable(), /announcement/);
  await trust.announce(signedAnnouncement(payload));
  trust.assertUsable();
  const next = { ...payload, keyset_version: '6' };
  await trust.announce(signedAnnouncement(next));
  assert.throws(
    () => trust.key('response', 'protocol_response', '5'),
    /announcement/,
  );
  const restarted = await openRootTrustStore({
    cache,
    roots: testRoots,
    realm: '2',
    requireAnnouncement: true,
    now,
  });
  assert.throws(() => restarted.assertUsable(), /announcement/);
  await assert.rejects(
    restarted.announce(signedAnnouncement(payload)),
    /rollback/,
  );
  await restarted.update(signedKeyset(next));
  restarted.assertUsable();
});
