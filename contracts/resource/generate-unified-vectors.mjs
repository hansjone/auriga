import {
  createHash,
  createPrivateKey,
  createPublicKey,
  sign,
} from 'node:crypto';
import { readFileSync, writeFileSync } from 'node:fs';
export const canonical = (value) =>
  value === null || typeof value !== 'object'
    ? JSON.stringify(value)
    : Array.isArray(value)
      ? '[' + value.map(canonical).join(',') + ']'
      : '{' +
        Object.keys(value)
          .sort()
          .map((key) => JSON.stringify(key) + ':' + canonical(value[key]))
          .join(',') +
        '}';
export const digest = (bytes) =>
  'sha256:' + createHash('sha256').update(bytes).digest('hex');
const keys = {};
const trust = {};
for (const purpose of ['release', 'revocation', 'downgrade']) {
  // Public deterministic TEST seeds only. Never install these keys into a deployment trust store.
  const seed = createHash('sha256')
    .update('auriga-public-unified-vector-test-only/' + purpose)
    .digest();
  const key = createPrivateKey({
    key: Buffer.concat([
      Buffer.from('302e020100300506032b657004220420', 'hex'),
      seed,
    ]),
    format: 'der',
    type: 'pkcs8',
  });
  keys[purpose] = key;
  trust[purpose] = {
    kid: 'test-only-' + purpose,
    publicKeySpkiBase64: createPublicKey(key)
      .export({ format: 'der', type: 'spki' })
      .toString('base64'),
  };
}
const seal = (value, purpose = 'release') => {
  const { signature, ...body } = value;
  return {
    ...body,
    signature: {
      alg: 'Ed25519',
      kid: trust[purpose].kid,
      value: sign(null, Buffer.from(canonical(body)), keys[purpose]).toString(
        'base64url',
      ),
    },
  };
};
const id = (n) => '019a0000-0000-7000-8000-' + String(n).padStart(12, '0');
const t0 = '2026-11-12T00:00:00Z',
  now = '2026-11-12T01:00:00Z',
  expiry = '2026-11-13T00:00:00Z';
const archive = Buffer.from('synthetic legacy archive bytes');
const permissions = { tools: [], outboundHosts: [], dataAccess: [] };
const base = {
  schemaVersion: 1,
  name: 'example-resource',
  version: '1.2.3',
  description: 'Synthetic public contract fixture',
  author: { displayName: 'Example' },
  license: 'MIT',
  permissions,
  runtimes: [],
  signature: { mode: 'detached', algorithm: 'Ed25519' },
};
function make(kind, bytes, versionId = id(3), sequence = 7) {
  const artifact = {
    id: 'payload',
    role: 'payload',
    digest: digest(bytes),
    sizeBytes: bytes.length,
    mediaType: 'application/gzip',
    os: 'any',
    arch: 'any',
  };
  const manifest = {
    ...base,
    kind,
    artifacts: kind === 'tool' ? [] : [artifact],
    ...(kind === 'plugin'
      ? {
          plugin: {
            packageName: '@example/plugin',
            dshVersionRange: '>=0.2.0',
          },
        }
      : kind === 'skill'
        ? {
            skill: {
              entry: 'SKILL.md',
              contentProfile: 'text-only',
              dshVersionRange: '>=0.2.0',
            },
          }
        : {
            tool: {
              delivery: 'online',
              url: 'https://tools.example.com/mcp',
              protocol: 'mcp',
              authentication: 'none',
            },
          }),
  };
  const rawManifest = JSON.stringify(manifest) + '\n';
  const envelope = seal({
    schemaVersion: 2,
    tenantId: id(1),
    packageId: id(2),
    versionId,
    releaseSequence: sequence,
    notAfter: expiry,
    statement: {
      purpose: 'resource',
      kind,
      name: manifest.name,
      version: manifest.version,
      manifestDigest: digest(rawManifest),
      artifactDigests: manifest.artifacts.map((a) => a.digest).sort(),
      issuedAt: t0,
    },
  });
  return {
    envelope,
    rawManifest,
    artifactBytes: Object.fromEntries(
      manifest.artifacts.map((a) => [a.id, bytes.toString('base64')]),
    ),
    expected: { tenantId: id(1), packageId: id(2), versionId, kind },
    protocol: 2,
    now,
  };
}
const plugin = make('plugin', archive);
const skill = make(
  'skill',
  Buffer.from('# Example skill\n\nPublic fixture.\n'),
  id(4),
);
const online = make('tool', Buffer.alloc(0), id(5));
const legacy = seal({
  schemaVersion: 1,
  tenantId: id(1),
  packageId: id(2),
  versionId: id(3),
  releaseSequence: 7,
  notAfter: expiry,
  statement: {
    purpose: 'plugin',
    name: '@example/plugin',
    version: '1.2.3',
    os: 'any',
    arch: 'any',
    digest: digest(archive),
    issuedAt: t0,
  },
});
const legacyInput = {
  envelope: legacy,
  rawArchiveBase64: archive.toString('base64'),
  expected: {
    tenantId: id(1),
    packageId: id(2),
    versionId: id(3),
    kind: 'plugin',
  },
  protocol: 1,
  now,
};
const marker = seal({
  schemaVersion: 2,
  purpose: 'migration',
  tenantId: id(1),
  packageId: id(2),
  firstVersionId: id(3),
  firstReleaseSequence: 7,
  activatedAt: t0,
});
const proof = {
  schemaVersion: 2,
  legacyEnvelope: legacy,
  unifiedEnvelope: plugin.envelope,
  legacyArtifactId: 'payload',
  activatedAt: t0,
  marker,
};
const migration = {
  proof,
  rawManifest: plugin.rawManifest,
  rawArchiveBase64: archive.toString('base64'),
  artifactBytes: plugin.artifactBytes,
  now,
  previousManifestDigest: plugin.envelope.statement.manifestDigest,
};
const watermark = {
  tenantId: id(1),
  packageId: id(2),
  versionId: id(3),
  releaseSequence: 7,
  verifiedDigestAliases: [legacy.statement.digest],
};
const identity = {
  tenantId: id(1),
  packageId: id(2),
  versionId: id(4),
  kind: 'skill',
  version: '1.2.3',
  manifestDigest: skill.envelope.statement.manifestDigest,
};
const authorization = seal(
  {
    schemaVersion: 2,
    purpose: 'downgrade',
    authorizationId: id(10),
    instanceId: id(11),
    identity,
    fromReleaseSequence: 8,
    toReleaseSequence: 7,
    issuedAt: now,
    expiresAt: '2026-11-12T01:10:00Z',
    reason: 'Synthetic approved rollback',
  },
  'downgrade',
);
const revokedEntry = (d) => ({ packageId: id(2), versionId: id(3), digest: d });
const revocations = (entries) =>
  seal(
    {
      schemaVersion: 1,
      tenantId: id(1),
      revision: 3,
      issuedAt: t0,
      expiresAt: expiry,
      revokedKids: [],
      revokedArtifacts: entries,
    },
    'revocation',
  );
const cases = [];
const add = (id, operation, input, decision = 'accept', reason = 'ok') =>
  cases.push({
    id,
    operation,
    input: structuredClone(input),
    expect: { decision, reason },
  });
const change = (value, fn) => {
  const copy = structuredClone(value);
  fn(copy);
  return copy;
};
add('skill-v2-valid', 'release', skill);
add('online-empty-artifacts-valid', 'release', online);
add('plugin-v1-valid', 'release', legacyInput);
add(
  'v1-rejected-by-v2',
  'release',
  { ...legacyInput, protocol: 2 },
  'reject',
  'schema',
);
add(
  'v2-rejected-by-v1',
  'release',
  { ...skill, protocol: 1 },
  'reject',
  'schema',
);
add(
  'skill-tampered-kind',
  'release',
  change(skill, (x) => (x.envelope.statement.kind = 'tool')),
  'reject',
  'signature',
);
add(
  'skill-tampered-artifact-digest',
  'release',
  change(
    skill,
    (x) => (x.envelope.statement.artifactDigests = [digest('tampered')]),
  ),
  'reject',
  'signature',
);
add(
  'skill-signed-wrong-artifact-set',
  'release',
  change(
    skill,
    (x) =>
      (x.envelope = seal({
        ...x.envelope,
        statement: { ...x.envelope.statement, artifactDigests: [] },
      })),
  ),
  'reject',
  'manifest_binding',
);
add(
  'skill-raw-byte-change',
  'release',
  { ...skill, rawManifest: skill.rawManifest + ' ' },
  'reject',
  'manifest_binding',
);
add('migration-proof-valid', 'migration', migration);
add(
  'migration-forged-mapping',
  'migration',
  change(migration, (x) => (x.proof.legacyArtifactId = 'missing')),
  'reject',
  'migration_binding',
);
add(
  'migration-replaced-M',
  'migration',
  change(migration, (x) => (x.previousManifestDigest = digest('different-M'))),
  'reject',
  'migration_binding',
);
add(
  'migration-cross-tenant',
  'migration',
  change(
    migration,
    (x) =>
      (x.proof.unifiedEnvelope = seal({
        ...x.proof.unifiedEnvelope,
        tenantId: id(99),
      })),
  ),
  'reject',
  'migration_binding',
);
add(
  'migration-t0-tampered',
  'migration',
  change(migration, (x) => (x.proof.marker.activatedAt = now)),
  'reject',
  'migration_signature',
);
add(
  'migration-t0-unmatched',
  'migration',
  change(migration, (x) => (x.proof.activatedAt = now)),
  'reject',
  'migration_binding',
);
const a = revokedEntry(legacy.statement.digest),
  m = revokedEntry(plugin.envelope.statement.manifestDigest);
for (const [id, entries] of [
  ['dual-digest', [a, m]],
  ['only-A', [a]],
  ['only-M', [m]],
])
  add(
    'revoked-' + id,
    'revocation',
    { release: plugin, list: revocations(entries) },
    'reject',
    'revoked',
  );
const reused = make('plugin', archive, id(8), 8);
add(
  'revoked-artifact-reuse',
  'revocation',
  { release: reused, list: revocations([a]) },
  'reject',
  'revoked',
);
add('revocations-empty-valid', 'revocation', {
  release: skill,
  list: revocations([]),
});
add(
  'revocations-tampered',
  'revocation',
  { release: skill, list: change(revocations([]), (x) => x.revision++) },
  'reject',
  'revocation_signature',
);
add(
  'revocations-cross-tenant',
  'revocation',
  {
    release: skill,
    list: seal({ ...revocations([]), tenantId: id(99) }, 'revocation'),
  },
  'reject',
  'revocation_tenant',
);
add('same-sequence-proved-alias', 'watermark', {
  release: plugin,
  watermark,
  migration,
});
add(
  'same-sequence-unproved-alias',
  'watermark',
  { release: plugin, watermark },
  'reject',
  'watermark_alias',
);
add(
  'same-sequence-version-swap',
  'watermark',
  { release: reused, watermark: { ...watermark, releaseSequence: 8 } },
  'reject',
  'watermark_alias',
);
add(
  'cross-protocol-rollback-v1',
  'watermark',
  { release: legacyInput, watermark: { ...watermark, releaseSequence: 8 } },
  'reject',
  'rollback',
);
add(
  'cross-protocol-rollback-v2',
  'watermark',
  { release: plugin, watermark: { ...watermark, releaseSequence: 8 } },
  'reject',
  'rollback',
);
const renewal = { ...legacyInput, marker, persistedActivatedAt: t0 };
add('v1-renewal-at-fixed-deadline', 'renewal', renewal);
add(
  'v1-renewal-past-fixed-deadline',
  'renewal',
  {
    ...renewal,
    envelope: seal({
      ...legacy,
      notAfter: '2026-11-13T00:00:01Z',
      statement: { ...legacy.statement, issuedAt: now },
    }),
  },
  'reject',
  'renewal_cutoff',
);
add(
  'v1-renewal-after-cutoff',
  'renewal',
  { ...renewal, now: expiry },
  'reject',
  'renewal_cutoff',
);
add(
  'v1-renewal-cannot-reset-t0',
  'renewal',
  { ...renewal, marker: seal({ ...marker, activatedAt: now }) },
  'reject',
  'migration_binding',
);
const down = {
  authorization,
  identity,
  instanceId: id(11),
  fromReleaseSequence: 8,
  toReleaseSequence: 7,
  now: '2026-11-12T01:05:00Z',
  usedAuthorizationIds: [],
};
add('downgrade-signed-valid', 'downgrade', down);
add(
  'downgrade-other-version',
  'downgrade',
  { ...down, identity: { ...identity, versionId: id(99) } },
  'reject',
  'downgrade_scope',
);
add(
  'downgrade-other-instance',
  'downgrade',
  { ...down, instanceId: id(12) },
  'reject',
  'downgrade_scope',
);
add(
  'downgrade-expired',
  'downgrade',
  { ...down, now: '2026-11-12T01:10:00Z' },
  'reject',
  'downgrade_expired',
);
add(
  'downgrade-replayed',
  'downgrade',
  { ...down, usedAuthorizationIds: [authorization.authorizationId] },
  'reject',
  'downgrade_replay',
);
add(
  'downgrade-tampered',
  'downgrade',
  change(down, (x) => (x.authorization.reason = 'tampered')),
  'reject',
  'downgrade_signature',
);
add(
  'downgrade-wrong-purpose',
  'downgrade',
  change(
    down,
    (x) =>
      (x.authorization = seal(
        { ...x.authorization, purpose: 'resource' },
        'downgrade',
      )),
  ),
  'reject',
  'schema',
);

const clock = {
  bootId: id(14),
  maxObservedTime: now,
  lastTrustedTime: now,
  lastTrustedTimeSource: 'authenticated_server',
  lastMonotonicMillis: 1000,
  leaseDeadlineMonotonicMillis: 82801000,
};
const timing = {
  clock,
  bootId: id(14),
  wallNow: '2026-11-12T01:00:01Z',
  monotonicNowMillis: 2000,
  leaseExpiresAt: expiry,
};
add('guardian-clock-valid', 'clock', timing);
add(
  'guardian-clock-wall-rollback',
  'clock',
  { ...timing, wallNow: t0 },
  'reject',
  'clock_rollback',
);
add(
  'guardian-clock-monotonic-rollback',
  'clock',
  { ...timing, monotonicNowMillis: 999 },
  'reject',
  'clock_rollback',
);
add(
  'guardian-clock-new-boot-offline',
  'clock',
  { ...timing, bootId: id(99) },
  'reject',
  'clock_reverify',
);
add(
  'guardian-active-lease-expired',
  'clock',
  { ...timing, wallNow: expiry, monotonicNowMillis: 82801000 },
  'reject',
  'lease_expired',
);
const data = {
  schemaVersion: 3,
  description:
    'Self-contained public TEST vectors. A/C/D/E must run every case id without reconstructing negative inputs. Never trust these test keys in deployments.',
  generation: {
    command: 'node contracts/resource/generate-unified-vectors.mjs',
    method:
      'Ed25519 from SHA-256 of public test-only domain-separated seed, separate release/revocation/downgrade keys. RFC8785 JCS, original UTF-8 manifest bytes and base64 artifact bytes; all expected results are explicit data.',
  },
  trust,
  cases,
};
const destination = new URL('./unified-vectors.json', import.meta.url);
const output = JSON.stringify(data, null, 2) + '\n';
if (process.argv.includes('--check')) {
  if (readFileSync(destination, 'utf8') !== output)
    throw new Error(
      'Unified vectors differ; regenerate using the documented command',
    );
} else writeFileSync(destination, output);
