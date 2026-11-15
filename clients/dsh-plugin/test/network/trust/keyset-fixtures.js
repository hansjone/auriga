import { generateKeyPairSync, sign } from 'node:crypto';
import { Buffer } from 'node:buffer';
import { jcs } from '../../../../../contracts/test/network-lib.mjs';
import { openV2TrustStore } from '../../../src/network/trust/v2-store.js';
import { signingInput } from '../../../src/network/trust/verifier.js';
import { createV2Signer } from './v2-keyset-fixtures.js';

const root = generateKeyPairSync('ed25519');
const v2 = createV2Signer(root, 'test-offline-root');
export const signedV2Keyset = v2.publication;
export const signedV2Announcement = v2.announcement;
export const testRoots = [
  {
    kid: 'test-offline-root',
    public_key_spki: root.publicKey
      .export({ format: 'der', type: 'spki' })
      .toString('base64url'),
  },
];
// Fixture transport signs payloads; every validation, key lookup, fence and
// refresh decision is performed by the production v2 store.
export async function openSignedTrust(options) {
  return openV2TrustStore({
    ...options,
    realm: options.realm ?? options.config?.keyset?.payload.realm ?? '2',
    tenant_id:
      options.tenant_id ??
      options.config?.keyset?.payload.tenant_id ??
      'tenant-fixture',
    requireAnnouncement: options.requireAnnouncement ?? false,
  });
}
export async function openTestTrust(options) {
  const signer = createV2Signer(root, 'test-offline-root');
  let store;
  const install = async (payload) => {
    await store.update(signer.publication(payload));
    await store.announce(signer.announcement(payload));
  };
  store = await openSignedTrust({
    ...options,
    config: undefined,
    roots: testRoots,
    refresh:
      options.refresh &&
      (async (request) => {
        await install(await options.refresh(request));
      }),
  });
  if (options.config) await install(options.config);
  return Object.defineProperties(
    { ...store, update: install },
    {
      diagnostics: { get: () => store.diagnostics },
      refreshRevision: { get: () => store.refreshRevision },
    },
  );
}

export function signedV2Control(payload) {
  return {
    payload,
    root_auth: {
      kid: 'test-offline-root',
      alg: 'Ed25519',
      signature: sign(
        null,
        Buffer.from('Auriga.NetworkRootControl.v2\0' + jcs(payload)),
        root.privateKey,
      ).toString('base64url'),
    },
  };
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
