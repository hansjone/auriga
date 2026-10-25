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
import { openTrustStore } from '../../../src/network/trust/store.js';
import {
  verifyEnvelope,
  signingInput,
} from '../../../src/network/trust/verifier.js';

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
    realm: '2',
    version: '5',
    issuedAt: '2026-10-25T00:00:00Z',
    validUntil: '2026-10-26T00:00:00Z',
    keys: vectors.keys.map((k) => ({
      ...k,
      alg: 'Ed25519',
      status: 'active',
      notBefore: '2026-10-24T00:00:00Z',
      notAfter: '2026-10-27T00:00:00Z',
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
    (c) => (c.keys[0].status = 'revoked'),
    (c) => (c.keys[0].notAfter = '2026-10-25T00:00:59Z'),
    (c) => (c.keys[0].notBefore = '2026-10-25T00:02:00Z'),
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
    publicKeySpki: pair.publicKey
      .export({ format: 'der', type: 'spki' })
      .toString('base64url'),
  };
  await trust.update({
    ...config(),
    version: '6',
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
        version: '6',
        keys: config().keys.map((k) => ({ ...k, status: 'revoked' })),
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
    version: '6',
    keys: config().keys.map((k) => ({ ...k, status: 'revoked' })),
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
    restored.update({ ...config(), version: '6' }),
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
  await assert.rejects(failing.update({ ...config(), version: '7' }));
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
  const c = { ...config(), validUntil: '2026-10-28T00:00:00Z' };
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
