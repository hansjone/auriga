import { generateKeyPairSync, sign } from 'node:crypto';
import { Buffer } from 'node:buffer';
import { readFile } from 'node:fs/promises';
import { detach, digest, jcs } from '../../../src/network/trust/v2.js';
const template = JSON.parse(
  await readFile(
    new URL(
      '../../../../../contracts/network/fixtures/auto-renewal-v2-vectors.json',
      import.meta.url,
    ),
    'utf8',
  ),
).cases[0].input.publication;

/** Runtime-generated test keys; this helper only signs, production decides. */
export function createV2Signer(root, rootKid) {
  const runtime = generateKeyPairSync('ed25519'),
    fillers = new Map(),
    previous = new Map(),
    authorities = new Map();
  const signed = (payload, key, kid, domain, field) => ({
    payload,
    [field]: {
      kid,
      alg: 'Ed25519',
      signature: sign(
        null,
        Buffer.from(domain + '\0' + jcs(payload)),
        key,
      ).toString('base64url'),
    },
  });
  function publication(
    payload,
    tenant = 'tenant-fixture',
    rootOverride = root,
  ) {
    const scope = tenant + ':' + payload.realm;
    let authority = authorities.get(scope);
    if (!authority) {
      authority = signed(
        {
          ...detach(template.authority.payload),
          tenant_id: tenant,
          realm: payload.realm,
          runtime_kid: 'test-runtime',
          public_key_spki: runtime.publicKey
            .export({ type: 'spki', format: 'der' })
            .toString('base64url'),
          not_before: new Date(Date.parse(payload.issued_at) - 60000)
            .toISOString()
            .replace('.000Z', 'Z'),
        },
        rootOverride.privateKey,
        rootKid,
        'Auriga.NetworkRuntimeAuthority.v2',
        'root_auth',
      );
      authorities.set(scope, authority);
    }
    const control = signed(
      {
        ...detach(template.control.payload),
        tenant_id: tenant,
        realm: payload.realm,
        control_revision: '2',
        issued_at: authority.payload.not_before,
        allowed_authorities: [
          {
            authority_id: authority.payload.authority_id,
            authority_digest: digest(authority),
          },
        ],
      },
      rootOverride.privateKey,
      rootKid,
      'Auriga.NetworkRootControl.v2',
      'root_auth',
    );
    const lineage = scope + ':' + (payload.keys[0]?.public_key_spki ?? 'empty');
    const prior = previous.get(lineage);
    const keys = detach(payload.keys);
    for (const purpose of [
      'protocol_response',
      'management_state',
      'business_delivery',
    ]) {
      if (keys.some((k) => k.purpose === purpose && k.state === 'active'))
        continue;
      const id = lineage + purpose;
      if (!fillers.has(id))
        fillers.set(id, {
          ...detach(
            payload.keys[0] ?? {
              alg: 'Ed25519',
              not_before: new Date(Date.parse(payload.issued_at) - 60000)
                .toISOString()
                .replace('.000Z', 'Z'),
              not_after: payload.valid_until,
            },
          ),
          purpose,
          kid: 'filler-' + purpose,
          public_key_spki: generateKeyPairSync('ed25519')
            .publicKey.export({ type: 'spki', format: 'der' })
            .toString('base64url'),
          state: 'active',
        });
      keys.push(fillers.get(id));
    }
    const firsts = keys.map(
      (key) =>
        prior?.keyset.payload.first_publications.find(
          (f) => f.kid === key.kid,
        ) ?? {
          kid: key.kid,
          committed_version: payload.keyset_version,
          committed_at: payload.issued_at,
        },
    );
    const advances =
      prior &&
      BigInt(prior.keyset.payload.keyset_version) <
        BigInt(payload.keyset_version);
    const p = {
      authority,
      control,
      keyset: signed(
        {
          ...detach(template.keyset.payload),
          ...detach(payload),
          schema_version: 2,
          tenant_id: tenant,
          authority_id: authority.payload.authority_id,
          authority_digest: digest(authority),
          control_revision: '2',
          generation: '1',
          mode: payload.mode ?? (advances ? 'refresh' : 'bootstrap'),
          keys,
          first_publications: firsts,
          archive: {
            before: new Date(Date.parse(payload.issued_at) - 360000)
              .toISOString()
              .replace('.000Z', 'Z'),
            history_digest: 'sha256:' + '0'.repeat(64),
          },
        },
        runtime.privateKey,
        'test-runtime',
        'Auriga.NetworkKeyset.v2',
        'runtime_auth',
      ),
    };
    if (advances)
      p.predecessor = {
        keyset: prior.keyset,
        authority: prior.authority,
        control: prior.control,
        keyset_digest: digest(prior.keyset),
      };
    if (
      prior &&
      !advances &&
      prior.keyset.payload.keyset_version === payload.keyset_version &&
      prior.predecessor
    ) {
      p.keyset.payload.mode = prior.keyset.payload.mode;
      p.predecessor = prior.predecessor;
      p.keyset = signed(
        p.keyset.payload,
        runtime.privateKey,
        'test-runtime',
        'Auriga.NetworkKeyset.v2',
        'runtime_auth',
      );
    }
    previous.set(lineage, p);
    return p;
  }
  function announcement(payload, overrides = {}, tenant = 'tenant-fixture') {
    const p = publication(payload, tenant),
      k = p.keyset.payload;
    const body = {
      schema_version: 2,
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
      keyset_digest: digest(p.keyset),
      ...overrides,
    };
    return signed(
      body,
      runtime.privateKey,
      'test-runtime',
      'Auriga.NetworkKeysetAnnouncement.v2',
      'runtime_auth',
    );
  }
  return {
    publication,
    announcement,
    signKeyset: (payload) =>
      signed(
        payload,
        runtime.privateKey,
        'test-runtime',
        'Auriga.NetworkKeyset.v2',
        'runtime_auth',
      ),
  };
}
