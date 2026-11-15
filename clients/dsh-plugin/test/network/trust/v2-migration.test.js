import test from 'node:test';
import assert from 'node:assert/strict';
import { Buffer } from 'node:buffer';
import { generateKeyPairSync, sign } from 'node:crypto';
import { openV2TrustStore } from '../../../src/network/trust/v2-store.js';
import { detach, digest, jcs } from '../../../src/network/trust/v2.js';
import { createV2Signer } from './v2-keyset-fixtures.js';

test('v1 cache imports once for audit; only a root permit enables v2 migration preserving original first publication', async () => {
  const root = generateKeyPairSync('ed25519'),
    business = generateKeyPairSync('ed25519');
  const time = Date.parse('2026-11-14T00:00:00Z');
  const payload = {
    schema_version: 1,
    realm: '2',
    keyset_version: '1',
    issued_at: '2026-11-14T00:00:00Z',
    valid_until: '2026-11-15T00:00:00Z',
    keys: ['business_delivery', 'protocol_response', 'management_state'].map(
      (purpose, i) => ({
        kid: purpose,
        purpose,
        alg: 'Ed25519',
        state: 'active',
        not_before: '2026-11-13T00:00:00Z',
        not_after: '2026-11-20T00:00:00Z',
        public_key_spki: (i === 0
          ? business
          : generateKeyPairSync('ed25519')
        ).publicKey
          .export({ format: 'der', type: 'spki' })
          .toString('base64url'),
      }),
    ),
  };
  const rootAuth = (body, domain) => ({
    alg: 'Ed25519',
    kid: 'root',
    signature: sign(
      null,
      Buffer.from(domain + '\0' + jcs(body)),
      root.privateKey,
    ).toString('base64url'),
  });
  const legacy = {
    keyset: payload,
    root_auth: rootAuth(payload, 'Auriga.NetworkKeyset.v1'),
  };
  let state = { publication: legacy, history: payload.keys };
  const oldCache = detach(state);
  const options = {
    cache: {
      load: async () => detach(state),
      commit: async (value) => {
        state = detach(value);
      },
    },
    roots: [
      {
        kid: 'root',
        public_key_spki: root.publicKey
          .export({ format: 'der', type: 'spki' })
          .toString('base64url'),
      },
    ],
    realm: '2',
    tenant_id: 'tenant-fixture',
    now: () => time,
  };
  let store = await openV2TrustStore(options);
  assert.equal(state.schema_version, 2);
  assert.equal(state.v1_checkpoint.version, '1');
  assert.equal(digest(state.v1_checkpoint.publication), digest(legacy));
  assert.throws(() => store.assertUsable(), /unavailable/);
  store = await openV2TrustStore(options);
  assert.equal(state.history.length, 3);
  const signer = createV2Signer(root, 'root'),
    current = { ...payload, keyset_version: '2' };
  const publication = signer.publication(current);
  publication.keyset.payload.mode = 'migration';
  publication.keyset.payload.first_publications = payload.keys.map((key) => ({
    kid: key.kid,
    committed_version: '1',
    committed_at: payload.issued_at,
  }));
  publication.keyset = signer.signKeyset(publication.keyset.payload);
  await assert.rejects(store.update(publication), /invalid_transition/);
  const permitPayload = {
    schema_version: 2,
    tenant_id: 'tenant-fixture',
    realm: '2',
    permit_id: 'permit-1',
    ancestor_digest: digest(legacy),
    queue_commitment_digest: 'sha256:' + '0'.repeat(64),
    maximum_committed_v1_version: '1',
    minimum_v2_version: '2',
    minimum_generation: '1',
  };
  await store.installMigrationPermit({
    payload: permitPayload,
    root_auth: rootAuth(permitPayload, 'Auriga.NetworkMigrationPermit.v2'),
  });
  await store.update(publication);
  await store.announce(
    signer.announcement(current, { keyset_digest: digest(publication.keyset) }),
  );
  store.assertUsable();
  assert.equal(state.first_publication_ledger.length, 3);
  assert.ok(
    state.first_publication_ledger.every(
      (key) => key.committed_version === '1',
    ),
  );
  const reopened = await openV2TrustStore(options);
  reopened.assertUsable();
  await assert.rejects(reopened.update(legacy), /invalid_chain/);

  // A v1 consumer may return years after the first migration snapshot has
  // left transport. It must verify the current v2 proof without clearing its
  // old ledger or requiring that first migration version to remain current.
  const later = time + 750 * 86400000;
  const iso = (offset) =>
    new Date(later + offset).toISOString().replace('.000Z', 'Z');
  const keys = payload.keys.map((key) => ({
    ...key,
    kid: 'after-offline-' + key.kid,
    public_key_spki: generateKeyPairSync('ed25519')
      .publicKey.export({ type: 'spki', format: 'der' })
      .toString('base64url'),
    not_before: iso(-86400000),
    not_after: iso(6 * 86400000),
  }));
  const previous = {
    ...detach(publication.keyset.payload),
    mode: 'refresh',
    keyset_version: '900',
    issued_at: iso(-3600000),
    valid_until: iso(82800000),
    keys,
    first_publications: keys.map((key) => ({
      kid: key.kid,
      committed_version: '850',
      committed_at: iso(-86400000),
    })),
    archive: {
      before: iso(-4000000),
      history_digest: 'sha256:' + '0'.repeat(64),
    },
  };
  const previousSigned = signer.signKeyset(previous);
  const currentProof = {
    authority: publication.authority,
    control: publication.control,
    keyset: signer.signKeyset({
      ...previous,
      keyset_version: '901',
      issued_at: iso(0),
      valid_until: iso(86400000),
    }),
    predecessor: {
      authority: publication.authority,
      control: publication.control,
      keyset: previousSigned,
      keyset_digest: digest(previousSigned),
    },
  };
  let offlineState = oldCache;
  const offlineOptions = {
    ...options,
    now: () => later,
    migrationPermit: {
      payload: permitPayload,
      root_auth: rootAuth(permitPayload, 'Auriga.NetworkMigrationPermit.v2'),
    },
    cache: {
      load: async () => detach(offlineState),
      commit: async (value) => {
        offlineState = detach(value);
      },
    },
  };
  const offline = await openV2TrustStore(offlineOptions);
  await offline.update(currentProof);
  await offline.announce(
    signer.announcement(
      {
        ...payload,
        keyset_version: '901',
        issued_at: iso(0),
        valid_until: iso(86400000),
        keys,
      },
      { keyset_digest: digest(currentProof.keyset) },
    ),
  );
  offline.assertUsable();
  const offlineReopened = await openV2TrustStore(offlineOptions);
  offlineReopened.assertUsable();
  assert.equal(offlineState.history.length, 6);
  assert.equal(
    offlineState.history.filter((key) => key.state === 'retired').length,
    3,
  );
});
