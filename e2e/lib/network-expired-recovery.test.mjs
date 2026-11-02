import test from 'node:test';
import assert from 'node:assert/strict';
import { createHash, generateKeyPairSync, sign } from 'node:crypto';
import { openTrustStore } from '../../clients/dsh-plugin/src/network/trust/store.js';
import { canonicalize } from '../../clients/dsh-plugin/src/network/trust/jcs.js';

const iso = (n) => new Date(n).toISOString().replace('.000Z', 'Z');
const day = 86400000;
function fixture() {
  const now = Math.floor(Date.now() / 1000) * 1000;
  const root = generateKeyPairSync('ed25519');
  const spki = (key) =>
    key.export({ format: 'der', type: 'spki' }).toString('base64url');
  const old = {
    schema_version: 1,
    realm: '1',
    keyset_version: '1',
    issued_at: iso(now - 26 * 3600000),
    valid_until: iso(now - 2 * 3600000),
    keys: ['business_delivery', 'protocol_response', 'management_state'].map(
      (purpose, i) => ({
        kid: `old-${i}`,
        alg: 'Ed25519',
        purpose,
        state: 'active',
        public_key_spki: spki(generateKeyPairSync('ed25519').publicKey),
        not_before: iso(now - 26 * 3600000),
        not_after: iso(now - 2 * 3600000),
      }),
    ),
  };
  const recovered = {
    ...old,
    keyset_version: '2',
    issued_at: iso(now),
    valid_until: iso(now + day),
    keys: [
      ...old.keys.map((key) => ({ ...key, state: 'retired' })),
      ...old.keys.map((key, i) => ({
        ...key,
        kid: `recovery-${i}`,
        state: 'active',
        public_key_spki: spki(generateKeyPairSync('ed25519').publicKey),
        not_before: iso(now),
        not_after: iso(now + 7 * day),
      })),
    ],
  };
  const signed = (payload) => ({
    keyset: payload,
    root_auth: {
      alg: 'Ed25519',
      kid: 'offline-root',
      signature: sign(
        null,
        Buffer.from('Auriga.NetworkKeyset.v1\0' + canonicalize(payload)),
        root.privateKey,
      ).toString('base64url'),
    },
  });
  const announcement = (payload) => {
    const body = {
      keyset_version: payload.keyset_version,
      valid_until: payload.valid_until,
      manifest_digest:
        'sha256:' +
        createHash('sha256').update(canonicalize(payload)).digest('hex'),
      root_kid: 'offline-root',
    };
    return {
      ...body,
      root_signature: sign(
        null,
        Buffer.from(
          'Auriga.NetworkKeysetAnnouncement.v1\0' + canonicalize(body),
        ),
        root.privateKey,
      ).toString('base64url'),
    };
  };
  return {
    now,
    old,
    recovered,
    signed,
    announcement,
    roots: [{ kid: 'offline-root', public_key_spki: spki(root.publicKey) }],
  };
}

test('expired recovery requires complete refresh and retains durable old trust history', async () => {
  const f = fixture();
  let value;
  const cache = {
    async load() {
      return structuredClone(value);
    },
    async commit(next) {
      value = structuredClone(next);
    },
  };
  const initial = await openTrustStore({
    cache,
    roots: f.roots,
    realm: '1',
    now: () => f.now - 25 * 3600000,
    requireAnnouncement: true,
  });
  await initial.announce(f.announcement(f.old));
  await initial.update(f.signed(f.old));
  initial.assertUsable();
  const current = await openTrustStore({
    cache,
    roots: f.roots,
    realm: '1',
    now: () => f.now,
    requireAnnouncement: true,
  });
  assert.throws(() => current.assertUsable(), {
    code: 'network.key_unavailable',
  });
  await current.announce(f.announcement(f.recovered));
  assert.throws(() => current.assertUsable(), {
    code: 'network.key_unavailable',
  });
  await current.update(f.signed(f.recovered));
  current.assertUsable();
  assert.equal(current.version, '2');
  assert.equal(value.history.length, 6);
  for (const key of f.old.keys) {
    assert.deepEqual(
      value.history.find((row) => row.kid === key.kid),
      { ...key, state: 'retired' },
    );
  }
});

test('expired cached trust cannot accept a changed root or rollback after recovery', async () => {
  const f = fixture();
  let value;
  const cache = {
    async load() {
      return structuredClone(value);
    },
    async commit(next) {
      value = structuredClone(next);
    },
  };
  const current = await openTrustStore({
    cache,
    roots: f.roots,
    realm: '1',
    now: () => f.now,
    requireAnnouncement: true,
  });
  const wrong = fixture();
  await assert.rejects(current.update(wrong.signed(wrong.recovered)), {
    code: 'network.key_unavailable',
  });
  await current.announce(f.announcement(f.recovered));
  await current.update(f.signed(f.recovered));
  current.assertUsable();
  await assert.rejects(current.update(f.signed(f.old)), {
    code: 'network.key_unavailable',
  });
  assert.equal(value.publication.keyset.keyset_version, '2');
});
