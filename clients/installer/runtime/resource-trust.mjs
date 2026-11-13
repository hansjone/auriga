/** Independent Desktop resource trust enforcement. No plugin runtime imports. */
import { createPublicKey, verify, createHash } from 'node:crypto';
import { schemaValid } from './schema.mjs';
import { canonicalize, parseStrictJson } from './json.mjs';
import { guardianProtocol } from './guardian-protocol.mjs';
const clockRules = guardianProtocol.clock;
const canon = canonicalize;
const sha = (value) =>
  'sha256:' + createHash('sha256').update(value).digest('hex');
const fail = (reason) => {
  throw new Error(reason);
};
const requireRule = (condition, reason) => {
  if (!condition) fail(reason);
};
function signed(document, trust, purpose) {
  const { signature, ...body } = document;
  const trusted = trust[purpose];
  if (
    !signature ||
    signature.alg !== 'Ed25519' ||
    signature.kid !== trusted.kid
  )
    return false;
  const bytes = Buffer.from(signature.value, 'base64url');
  if (bytes.length !== 64 || bytes.toString('base64url') !== signature.value)
    return false;
  return verify(
    null,
    Buffer.from(canon(body)),
    createPublicKey({
      key: Buffer.from(trusted.publicKeySpkiBase64, 'base64'),
      format: 'der',
      type: 'spki',
    }),
    bytes,
  );
}
const validWindow = (start, end, now, max) =>
  Date.parse(start) <= Date.parse(now) &&
  Date.parse(now) < Date.parse(end) &&
  Date.parse(end) > Date.parse(start) &&
  Date.parse(end) - Date.parse(start) <= max;
export function verifyResourceRelease(input, trust) {
  const { envelope: e, protocol, now, expected } = input;
  requireRule(
    schemaValid(
      protocol === 1 ? 'signatures' : 'package',
      protocol === 1 ? 'ResourceEnvelope' : 'Envelope',
      e,
    ),
    'schema',
  );
  requireRule(signed(e, trust, 'release'), 'signature');
  requireRule(
    validWindow(e.statement.issuedAt, e.notAfter, now, 86400000),
    'release_expired',
  );
  requireRule(
    ['tenantId', 'packageId', 'versionId'].every((k) => expected[k] === e[k]) &&
      (protocol === 1
        ? expected.kind === 'plugin'
        : expected.kind === e.statement.kind),
    'identity',
  );
  if (protocol === 1) {
    requireRule(
      sha(Buffer.from(input.rawArchiveBase64, 'base64')) === e.statement.digest,
      'manifest_binding',
    );
    return;
  }
  const manifest = parseStrictJson(Buffer.from(input.rawManifest));
  requireRule(schemaValid('package', 'Manifest', manifest), 'manifest_schema');
  requireRule(
    sha(input.rawManifest) === e.statement.manifestDigest &&
      ['kind', 'name', 'version'].every((k) => manifest[k] === e.statement[k]),
    'manifest_binding',
  );
  requireRule(
    canon([...new Set(manifest.artifacts.map((a) => a.digest))].sort()) ===
      canon(e.statement.artifactDigests),
    'manifest_binding',
  );
  for (const artifact of manifest.artifacts) {
    const bytes = Buffer.from(input.artifactBytes[artifact.id] ?? '', 'base64');
    requireRule(
      bytes.length === artifact.sizeBytes && sha(bytes) === artifact.digest,
      'artifact_binding',
    );
  }
}
function migration(input, trust) {
  const p = input.proof,
    a = p.legacyEnvelope,
    e = p.unifiedEnvelope,
    m = p.marker;
  requireRule(schemaValid('unified', 'UnifiedMigrationProof', p), 'schema');
  requireRule(signed(m, trust, 'release'), 'migration_signature');
  requireRule(
    ['tenantId', 'packageId', 'versionId', 'releaseSequence'].every(
      (k) => a[k] === e[k],
    ) &&
      ['tenantId', 'packageId'].every((k) => m[k] === e[k]) &&
      p.activatedAt === m.activatedAt &&
      Date.parse(m.activatedAt) <= Date.parse(input.now),
    'migration_binding',
  );
  requireRule(
    m.firstReleaseSequence <= e.releaseSequence &&
      (m.firstReleaseSequence !== e.releaseSequence ||
        m.firstVersionId === e.versionId),
    'migration_binding',
  );
  const expected = {
    tenantId: e.tenantId,
    packageId: e.packageId,
    versionId: e.versionId,
    kind: 'plugin',
  };
  verifyResourceRelease(
    {
      protocol: 1,
      envelope: a,
      rawArchiveBase64: input.rawArchiveBase64,
      expected,
      now: input.now,
    },
    trust,
  );
  verifyResourceRelease(
    {
      protocol: 2,
      envelope: e,
      rawManifest: input.rawManifest,
      artifactBytes: input.artifactBytes,
      expected,
      now: input.now,
    },
    trust,
  );
  const manifest = parseStrictJson(Buffer.from(input.rawManifest));
  requireRule(
    e.statement.kind === 'plugin' &&
      manifest.plugin.packageName === a.statement.name &&
      a.statement.version === e.statement.version &&
      input.previousManifestDigest === e.statement.manifestDigest &&
      manifest.artifacts.some(
        (x) =>
          x.id === p.legacyArtifactId &&
          x.role === 'payload' &&
          x.digest === a.statement.digest,
      ),
    'migration_binding',
  );
}
export function verifyTrust(operation, input, trust) {
  switch (operation) {
    case 'release':
      return verifyResourceRelease(input, trust);
    case 'migration':
      return migration(input, trust);
    case 'revocation': {
      verifyResourceRelease(input.release, trust);
      const list = input.list,
        e = input.release.envelope;
      requireRule(schemaValid('signatures', 'RevocationList', list), 'schema');
      requireRule(signed(list, trust, 'revocation'), 'revocation_signature');
      requireRule(list.tenantId === e.tenantId, 'revocation_tenant');
      requireRule(
        validWindow(list.issuedAt, list.expiresAt, input.release.now, 86400000),
        'revocation_expired',
      );
      const digests =
        e.schemaVersion === 1
          ? [e.statement.digest]
          : [e.statement.manifestDigest, ...e.statement.artifactDigests];
      requireRule(
        !list.revokedKids.includes(e.signature.kid) &&
          !list.revokedArtifacts.some((x) =>
            e.schemaVersion === 1
              ? x.packageId === e.packageId &&
                x.versionId === e.versionId &&
                x.digest === e.statement.digest
              : (x.packageId === e.packageId && x.versionId === e.versionId) ||
                digests.includes(x.digest),
          ),
        'revoked',
      );
      return;
    }
    case 'watermark': {
      verifyResourceRelease(input.release, trust);
      const w = input.watermark,
        e = input.release.envelope;
      requireRule(
        w.tenantId === e.tenantId && w.packageId === e.packageId,
        'watermark_tenant',
      );
      requireRule(e.releaseSequence >= w.releaseSequence, 'rollback');
      if (e.releaseSequence > w.releaseSequence) return;
      requireRule(w.versionId === e.versionId, 'watermark_alias');
      const digest =
        e.schemaVersion === 1 ? e.statement.digest : e.statement.manifestDigest;
      if (w.verifiedDigestAliases.includes(digest)) return;
      requireRule(Boolean(input.migration), 'watermark_alias');
      migration(input.migration, trust);
      const proof = input.migration.proof;
      requireRule(
        canon(e) === canon(proof.unifiedEnvelope) &&
          w.verifiedDigestAliases.includes(
            proof.legacyEnvelope.statement.digest,
          ),
        'watermark_alias',
      );
      return;
    }
    case 'renewal': {
      const m = input.marker;
      requireRule(
        schemaValid('unified', 'UnifiedMigrationMarker', m),
        'schema',
      );
      requireRule(signed(m, trust, 'release'), 'migration_signature');
      requireRule(
        m.tenantId === input.envelope.tenantId &&
          m.packageId === input.envelope.packageId &&
          m.activatedAt === input.persistedActivatedAt,
        'migration_binding',
      );
      const cutoff = Date.parse(m.activatedAt) + 86400000;
      requireRule(
        Date.parse(input.now) < cutoff &&
          Date.parse(input.envelope.notAfter) <= cutoff,
        'renewal_cutoff',
      );
      return verifyResourceRelease(input, trust);
    }
    case 'downgrade': {
      const a = input.authorization;
      requireRule(
        schemaValid('unified', 'UnifiedDowngradeAuthorization', a),
        'schema',
      );
      requireRule(signed(a, trust, 'downgrade'), 'downgrade_signature');
      requireRule(
        canon(a.identity) === canon(input.identity) &&
          a.instanceId === input.instanceId &&
          a.fromReleaseSequence === input.fromReleaseSequence &&
          a.toReleaseSequence === input.toReleaseSequence &&
          a.toReleaseSequence < a.fromReleaseSequence,
        'downgrade_scope',
      );
      requireRule(
        validWindow(a.issuedAt, a.expiresAt, input.now, 600000),
        'downgrade_expired',
      );
      requireRule(
        !input.usedAuthorizationIds.includes(a.authorizationId),
        'downgrade_replay',
      );
      return;
    }
    case 'clock': {
      const c = input.clock;
      requireRule(input.bootId === c.bootId, 'clock_reverify');
      requireRule(
        input.monotonicNowMillis >= c.lastMonotonicMillis &&
          Date.parse(input.wallNow) >=
            Date.parse(c.maxObservedTime) - clockRules.rollbackToleranceMillis,
        'clock_rollback',
      );
      const effective = Math.max(
        Date.parse(input.wallNow),
        Date.parse(c.maxObservedTime) +
          input.monotonicNowMillis -
          c.lastMonotonicMillis,
        Date.parse(c.lastTrustedTime),
      );
      requireRule(
        effective < Date.parse(input.leaseExpiresAt) &&
          input.monotonicNowMillis < c.leaseDeadlineMonotonicMillis,
        'lease_expired',
      );
      return;
    }
    default:
      fail('unknown_operation');
  }
}
export function signedRevocations(list, tenantId, trust) {
  return (
    schemaValid('signatures', 'RevocationList', list) &&
    signed(list, trust, 'revocation') &&
    list.tenantId === tenantId
  );
}
export function signedRevocationFact(list, envelope, trust) {
  if (!signedRevocations(list, envelope.tenantId, trust)) return false;
  const digests = [
    envelope.statement.manifestDigest,
    ...envelope.statement.artifactDigests,
  ];
  return (
    list.revokedKids.includes(envelope.signature.kid) ||
    list.revokedArtifacts.some(
      (r) =>
        (r.packageId === envelope.packageId &&
          r.versionId === envelope.versionId) ||
        digests.includes(r.digest),
    )
  );
}

export function evaluateVector(vector, trust) {
  try {
    verifyTrust(vector.operation, vector.input, trust);
    return { decision: 'accept', reason: 'ok' };
  } catch (error) {
    return { decision: 'reject', reason: error.message };
  }
}
