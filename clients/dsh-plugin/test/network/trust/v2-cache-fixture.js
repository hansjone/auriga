import { readFile } from 'node:fs/promises';
import { Buffer } from 'node:buffer';
import { generateKeyPairSync, sign } from 'node:crypto';
import { detach, digest, jcs } from '../../../src/network/trust/v2.js';

const vectors = JSON.parse(
  await readFile(
    new URL(
      '../../../../../contracts/network/fixtures/auto-renewal-v2-vectors.json',
      import.meta.url,
    ),
    'utf8',
  ),
);
const date = (milliseconds) =>
  new Date(milliseconds).toISOString().replace('.000Z', 'Z');
const spki = (key) =>
  key.export({ type: 'spki', format: 'der' }).toString('base64url');
export function fixture() {
  const root = generateKeyPairSync('ed25519'),
    runtime = generateKeyPairSync('ed25519');
  let now = Date.parse('2026-11-14T00:00:00Z');
  const auth = (payload, pair, kid, domain, field) => ({
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
  const rootSign = (payload, domain) =>
    auth(payload, root, 'test-root', domain, 'root_auth');
  const runtimeSign = (payload, domain) =>
    auth(payload, runtime, 'test-runtime', domain, 'runtime_auth');
  const authorityBody = detach(
    vectors.cases[0].input.publication.authority.payload,
  );
  Object.assign(authorityBody, {
    runtime_kid: 'test-runtime',
    public_key_spki: spki(runtime.publicKey),
  });
  const authority = rootSign(
    authorityBody,
    'Auriga.NetworkRuntimeAuthority.v2',
  );
  let control = makeControl(2, false);
  function makeControl(revision, revoked) {
    const body = detach(vectors.cases[0].input.latest_control.payload);
    Object.assign(body, {
      control_revision: String(revision),
      allowed_authorities: revoked
        ? []
        : [
            {
              authority_id: authorityBody.authority_id,
              authority_digest: digest(authority),
            },
          ],
      revoked_authority_ids: revoked ? [authorityBody.authority_id] : [],
      revoked_installation_ids: [],
    });
    return rootSign(body, 'Auriga.NetworkRootControl.v2');
  }
  let counter = 0,
    publication;
  function publish(recover = false) {
    const previous = publication;
    const version = previous
      ? String(BigInt(previous.keyset.payload.keyset_version) + 1n)
      : '1';
    const keys =
      !previous || recover
        ? ['business_delivery', 'protocol_response', 'management_state'].map(
            (purpose) => ({
              kid: purpose + '-' + counter++,
              alg: 'Ed25519',
              purpose,
              state: 'active',
              not_before: date(now),
              not_after: date(now + 7 * 86400000),
              public_key_spki: spki(generateKeyPairSync('ed25519').publicKey),
            }),
          )
        : previous.keyset.payload.keys;
    const payload = {
      ...detach(vectors.cases[0].input.publication.keyset.payload),
      authority_id: authorityBody.authority_id,
      authority_digest: digest(authority),
      control_revision: control.payload.control_revision,
      keyset_version: version,
      issued_at: date(now),
      valid_until: date(now + 86400000),
      mode: previous ? (recover ? 'restart_recovery' : 'refresh') : 'bootstrap',
      keys,
      first_publications:
        !previous || recover
          ? keys.map((k) => ({
              kid: k.kid,
              committed_version: version,
              committed_at: date(now),
            }))
          : previous.keyset.payload.first_publications,
      archive: {
        before: date(now - 360000),
        history_digest: 'sha256:' + '0'.repeat(64),
      },
    };
    publication = {
      keyset: runtimeSign(payload, 'Auriga.NetworkKeyset.v2'),
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
    return publication;
  }
  function announcement(version) {
    const k = publication.keyset.payload;
    const payload = {
      ...detach(
        vectors.boundary_cases.find((x) => x.id === 'announcement-matched-pair')
          .input.announcement.payload,
      ),
      ...Object.fromEntries(
        [
          'tenant_id',
          'realm',
          'authority_id',
          'authority_digest',
          'control_revision',
          'generation',
          'keyset_version',
          'valid_until',
        ].map((f) => [f, k[f]]),
      ),
      keyset_digest: digest(publication.keyset),
    };
    if (version) payload.keyset_version = version;
    return runtimeSign(payload, 'Auriga.NetworkKeysetAnnouncement.v2');
  }
  let state,
    fail = false;
  const cache = {
    load: async () => state && detach(state),
    commit: async (value) => {
      if (fail) throw Error('disk failed');
      state = detach(value);
    },
  };
  const options = {
    cache,
    realm: '1',
    tenant_id: 'synthetic-tenant',
    roots: [{ kid: 'test-root', public_key_spki: spki(root.publicKey) }],
    now: () => now,
  };
  return {
    options,
    cache,
    root,
    rootSign,
    runtimeSign,
    authority,
    publish,
    announcement,
    control: (revision, revoked = false) => {
      control = makeControl(revision, revoked);
      return control;
    },
    advance: (days) => {
      now += days * 86400000;
    },
    fail: (value) => {
      fail = value;
    },
    state: () => state,
  };
}
