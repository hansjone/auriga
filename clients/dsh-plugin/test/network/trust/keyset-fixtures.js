import { generateKeyPairSync, sign, createHash } from 'node:crypto';
import { Buffer } from 'node:buffer';
import { jcs } from '../../../../../contracts/test/network-lib.mjs';
import { openTrustStore } from '../../../src/network/trust/store.js';
import { signingInput } from '../../../src/network/trust/verifier.js';

const root = generateKeyPairSync('ed25519');
export const testRoots = [
  {
    kid: 'test-offline-root',
    public_key_spki: root.publicKey
      .export({ format: 'der', type: 'spki' })
      .toString('base64url'),
  },
];
export function signedKeyset(payload) {
  return {
    keyset: payload,
    root_auth: {
      alg: 'Ed25519',
      kid: testRoots[0].kid,
      signature: sign(
        null,
        Buffer.from('Auriga.NetworkKeyset.v1\0' + jcs(payload)),
        root.privateKey,
      ).toString('base64url'),
    },
  };
}
export function signedAnnouncement(payload, overrides = {}) {
  const value = {
    keyset_version: payload.keyset_version,
    manifest_digest:
      'sha256:' + createHash('sha256').update(jcs(payload)).digest('hex'),
    valid_until: payload.valid_until,
    root_kid: testRoots[0].kid,
    ...overrides,
  };
  return {
    ...value,
    root_signature: sign(
      null,
      Buffer.from('Auriga.NetworkKeysetAnnouncement.v1\0' + jcs(value)),
      root.privateKey,
    ).toString('base64url'),
  };
}
// Convenience for existing payload-based fixtures; the real store only sees
// the contract's root-signed wrapper, including during refresh and restart.
export async function openTestTrust(options) {
  const store = await openTrustStore({
    ...options,
    roots: testRoots,
    config: options.config && signedKeyset(options.config),
    refresh:
      options.refresh &&
      (async (request) => signedKeyset(await options.refresh(request))),
  });
  return Object.defineProperties(
    { ...store, update: (payload) => store.update(signedKeyset(payload)) },
    {
      diagnostics: { get: () => store.diagnostics },
      refreshRevision: { get: () => store.refreshRevision },
    },
  );
}

export function publicationFixture() {
  const pair = generateKeyPairSync('ed25519');
  const now = Math.floor(Date.now() / 1000) * 1000;
  const iso = (offset) =>
    new Date(now + offset).toISOString().replace('.000Z', 'Z');
  const payload = {
    schema_version: 1,
    realm: '2',
    keyset_version: '5',
    issued_at: iso(-60000),
    valid_until: iso(3600000),
    keys: [
      {
        kid: 'response',
        alg: 'Ed25519',
        purpose: 'protocol_response',
        state: 'active',
        not_before: iso(-60000),
        not_after: iso(3600000),
        public_key_spki: pair.publicKey
          .export({ format: 'der', type: 'spki' })
          .toString('base64url'),
      },
    ],
  };
  return { pair, payload, now: () => now };
}
export function signedReadyResult(
  fixture,
  request,
  { epoch = '1', realm = '2', node = '64', incarnation = 'inc-1' } = {},
) {
  const value = {
    app_version: '1.0',
    type: 'ready_result',
    request_id: request.request_id,
    status: 'ready',
    epoch,
    node_identity: { realm, node, incarnation },
    head_seq: '0',
    committed_cursor: '0',
    retention_low_seq: '0',
    local_high_water: '0',
    registration_revision: '0',
    seen_checkpoint: null,
    required_features: [],
    server_auth: {
      version: 1,
      purpose: 'protocol_response',
      alg: 'Ed25519',
      kid: 'response',
      issuer_realm: realm,
      issued_at: fixture.payload.issued_at,
      not_after: new Date(fixture.now() + 120000)
        .toISOString()
        .replace('.000Z', 'Z'),
      audience: { realm, node, incarnation, epoch },
      request_id: request.request_id,
      channel_nonce: request.channel_nonce,
      keyset_version: fixture.payload.keyset_version,
    },
  };
  value.server_auth.signature = sign(
    null,
    signingInput(value),
    fixture.pair.privateKey,
  ).toString('base64url');
  return value;
}
