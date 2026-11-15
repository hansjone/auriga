import test from 'node:test';
import assert from 'node:assert/strict';
import { generateKeyPairSync, sign } from 'node:crypto';
import { readFile } from 'node:fs/promises';
import { openTrustStore } from '../../clients/dsh-plugin/src/network/trust/store.js';
import { digest, jcs } from '../../clients/dsh-plugin/src/network/trust/v2.js';

const template = JSON.parse(
  await readFile(
    new URL(
      '../../contracts/network/fixtures/auto-renewal-v2-vectors.json',
      import.meta.url,
    ),
    'utf8',
  ),
).cases[0].input.publication;

const iso = (n) => new Date(n).toISOString().replace('.000Z', 'Z');
const day = 86400000;
function fixture() {
  // Test Clock only: the signed runtime authority and all business keys are
  // generated here; the frozen public fixture supplies v2 structure/limits.
  const now = Date.parse('2026-11-14T00:00:00Z');
  const root = generateKeyPairSync('ed25519');
  const runtime = generateKeyPairSync('ed25519');
  const spki = (key) =>
    key.export({ format: 'der', type: 'spki' }).toString('base64url');
  const signed = (payload, pair, kid, domain, field) => ({
    payload,
    [field]: {
      alg: 'Ed25519',
      kid,
      signature: sign(
        null,
        Buffer.from(domain + '\0' + jcs(payload)),
        pair.privateKey,
      ).toString('base64url'),
    },
  });
  const authority = signed(
    {
      ...structuredClone(template.authority.payload),
      runtime_kid: 'recovery-runtime',
      public_key_spki: spki(runtime.publicKey),
      not_before: iso(now - 10 * day),
    },
    root,
    'offline-root',
    'Auriga.NetworkRuntimeAuthority.v2',
    'root_auth',
  );
  const control = signed(
    {
      ...structuredClone(template.control.payload),
      issued_at: iso(now - 10 * day),
      allowed_authorities: [
        {
          authority_id: authority.payload.authority_id,
          authority_digest: digest(authority),
        },
      ],
    },
    root,
    'offline-root',
    'Auriga.NetworkRootControl.v2',
    'root_auth',
  );
  const oldKeys = [
    'business_delivery',
    'protocol_response',
    'management_state',
  ].map((purpose, i) => ({
    kid: `old-${i}`,
    alg: 'Ed25519',
    purpose,
    state: 'active',
    public_key_spki: spki(generateKeyPairSync('ed25519').publicKey),
    not_before: iso(now - 26 * 3600000),
    not_after: iso(now - 2 * 3600000),
  }));
  function publication(version, issued, keys, previous) {
    const payload = {
      ...structuredClone(template.keyset.payload),
      authority_id: authority.payload.authority_id,
      authority_digest: digest(authority),
      control_revision: control.payload.control_revision,
      keyset_version: version,
      issued_at: iso(issued),
      valid_until: iso(issued + day),
      mode: previous ? 'restart_recovery' : 'bootstrap',
      keys,
      first_publications: keys.map(
        (key) =>
          previous?.keyset.payload.first_publications.find(
            (p) => p.kid === key.kid,
          ) ?? {
            kid: key.kid,
            committed_version: version,
            committed_at: iso(issued),
          },
      ),
      archive: {
        before: iso(issued - 360000),
        history_digest: 'sha256:' + '0'.repeat(64),
      },
    };
    return {
      keyset: signed(
        payload,
        runtime,
        'recovery-runtime',
        'Auriga.NetworkKeyset.v2',
        'runtime_auth',
      ),
      authority,
      control,
      ...(previous
        ? {
            predecessor: {
              keyset: previous.keyset,
              authority: previous.authority,
              control: previous.control,
              keyset_digest: digest(previous.keyset),
            },
          }
        : {}),
    };
  }
  const old = publication('1', now - 26 * 3600000, oldKeys);
  const recovered = publication(
    '2',
    now,
    [
      ...oldKeys.map((key) => ({ ...key, state: 'retired' })),
      ...oldKeys.map((key, i) => ({
        ...key,
        kid: `recovery-${i}`,
        state: 'active',
        public_key_spki: spki(generateKeyPairSync('ed25519').publicKey),
        not_before: iso(now),
        not_after: iso(now + 7 * day),
      })),
    ],
    old,
  );
  const announcement = ({ keyset }) =>
    signed(
      {
        ...Object.fromEntries(
          [
            'schema_version',
            'tenant_id',
            'realm',
            'authority_id',
            'authority_digest',
            'control_revision',
            'generation',
            'keyset_version',
            'valid_until',
          ].map((field) => [field, keyset.payload[field]]),
        ),
        keyset_digest: digest(keyset),
      },
      runtime,
      'recovery-runtime',
      'Auriga.NetworkKeysetAnnouncement.v2',
      'runtime_auth',
    );
  return {
    now,
    old,
    recovered,
    announcement,
    tenant_id: authority.payload.tenant_id,
    realm: authority.payload.realm,
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
    realm: f.realm,
    tenant_id: f.tenant_id,
    now: () => f.now - 25 * 3600000,
    requireAnnouncement: true,
  });
  await initial.update(f.old);
  await initial.announce(f.announcement(f.old));
  initial.assertUsable();
  const current = await openTrustStore({
    cache,
    roots: f.roots,
    realm: f.realm,
    tenant_id: f.tenant_id,
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
  await current.update(f.recovered);
  current.assertUsable();
  assert.equal(current.version, '2');
  assert.equal(value.history.length, 6);
  for (const key of f.old.keyset.payload.keys) {
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
  const initial = await openTrustStore({
    cache,
    roots: f.roots,
    realm: f.realm,
    tenant_id: f.tenant_id,
    now: () => f.now - 25 * 3600000,
  });
  await initial.update(f.old);
  await initial.announce(f.announcement(f.old));
  const current = await openTrustStore({
    cache,
    roots: f.roots,
    realm: f.realm,
    tenant_id: f.tenant_id,
    now: () => f.now,
    requireAnnouncement: true,
  });
  await assert.rejects(
    openTrustStore({
      cache,
      roots: f.roots,
      realm: f.realm,
      now: () => f.now,
    }),
    /installation scope required/,
  );
  await assert.rejects(async () => {
    const otherTenant = await openTrustStore({
      cache,
      roots: f.roots,
      realm: f.realm,
      tenant_id: 'other-tenant',
      now: () => f.now,
    });
    await otherTenant.update(f.recovered);
  }, /scope_denied/);
  const beforeWrongRoot = structuredClone(value);
  const wrong = fixture();
  await assert.rejects(current.update(wrong.recovered), /invalid_chain/);
  assert.deepEqual(value, beforeWrongRoot);
  await current.update(f.recovered);
  await current.announce(f.announcement(f.recovered));
  current.assertUsable();
  await assert.rejects(current.update(f.old), /version_rollback/);
  assert.equal(value.publication.keyset.payload.keyset_version, '2');
});
