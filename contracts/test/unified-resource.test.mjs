import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { createPublicKey, verify, createHash } from 'node:crypto';
import Ajv from '../../services/contracts/node_modules/ajv/dist/2020.js';
import addFormats from '../../services/contracts/node_modules/ajv-formats/dist/index.js';
const root = new URL('../', import.meta.url);
const read = (file) => JSON.parse(readFileSync(new URL(file, root), 'utf8'));
const ajv = new Ajv({ strict: true, allErrors: true });
addFormats(ajv);
for (const name of ['signatures', 'package', 'unified']) {
  const file = new URL(`resource/${name}.schema.json`, root);
  ajv.addSchema(JSON.parse(readFileSync(file, 'utf8')), file.href);
}
const check = (file, name, value) =>
  ajv.getSchema(
    new URL(`resource/${file}.schema.json`, root).href + '#/$defs/' + name,
  )(value);
const vectors = read('resource/unified-vectors.json');
const canon = (v) =>
  v === null || typeof v !== 'object'
    ? JSON.stringify(v)
    : Array.isArray(v)
      ? '[' + v.map(canon).join(',') + ']'
      : '{' +
        Object.keys(v)
          .sort()
          .map((k) => JSON.stringify(k) + ':' + canon(v[k]))
          .join(',') +
        '}';
const key = createPublicKey({
  key: Buffer.from(vectors.publicKey, 'base64'),
  format: 'der',
  type: 'spki',
});
const signed = (doc) => {
  const { signature, ...body } = doc;
  return verify(
    null,
    Buffer.from(canon(body)),
    key,
    Buffer.from(signature.value, 'base64url'),
  );
};
const sha = (raw) => 'sha256:' + createHash('sha256').update(raw).digest('hex');
// Executable contract oracle. Consumer implementations must exercise the same vectors independently.
function proofValid(
  proof,
  raw,
  previousM = proof.unifiedEnvelope.statement.manifestDigest,
) {
  const a = proof.legacyEnvelope,
    m = proof.unifiedEnvelope,
    manifest = JSON.parse(raw);
  return (
    check('signatures', 'ResourceEnvelope', a) &&
    check('package', 'Envelope', m) &&
    signed(a) &&
    signed(m) &&
    ['tenantId', 'packageId', 'versionId', 'releaseSequence'].every(
      (k) => a[k] === m[k],
    ) &&
    sha(Buffer.from(vectors.legacyArchiveBase64, 'base64')) ===
      a.statement.digest &&
    m.statement.kind === 'plugin' &&
    m.statement.version === a.statement.version &&
    manifest.kind === 'plugin' &&
    manifest.plugin.packageName === a.statement.name &&
    manifest.version === m.statement.version &&
    sha(raw) === m.statement.manifestDigest &&
    previousM === m.statement.manifestDigest &&
    manifest.artifacts.some(
      (x) =>
        x.id === proof.legacyArtifactId &&
        x.role === 'payload' &&
        x.digest === a.statement.digest,
    ) &&
    JSON.stringify(
      [...new Set(manifest.artifacts.map((x) => x.digest))].sort(),
    ) === JSON.stringify(m.statement.artifactDigests)
  );
}
function revoked(envelope, entries) {
  return entries.some(
    (x) =>
      (x.packageId === envelope.packageId &&
        x.versionId === envelope.versionId) ||
      [
        envelope.statement.manifestDigest,
        ...envelope.statement.artifactDigests,
      ].includes(x.digest),
  );
}
function accept(watermark, envelope, proof, raw) {
  const digest =
    envelope.schemaVersion === 1
      ? envelope.statement.digest
      : envelope.statement.manifestDigest;
  if (
    watermark.tenantId !== envelope.tenantId ||
    watermark.packageId !== envelope.packageId
  )
    return false;
  if (envelope.releaseSequence < watermark.releaseSequence) return false;
  if (envelope.releaseSequence > watermark.releaseSequence) return true;
  return (
    watermark.versionId === envelope.versionId &&
    (watermark.verifiedDigestAliases.includes(digest) ||
      (proofValid(proof, raw) &&
        watermark.verifiedDigestAliases.includes(
          proof.legacyEnvelope.statement.digest,
        ) &&
        digest === proof.unifiedEnvelope.statement.manifestDigest))
  );
}
test('v1/v2 schema mutual rejection, signatures and raw manifest binding', () => {
  const { legacyEnvelope: a, unifiedEnvelope: m } = vectors.proof;
  assert(check('signatures', 'ResourceEnvelope', a));
  assert(check('package', 'Envelope', m));
  assert(!check('signatures', 'ResourceEnvelope', m));
  assert(!check('package', 'Envelope', a));
  assert(proofValid(vectors.proof, vectors.rawManifest));
  for (const mutate of [
    (p) => (p.unifiedEnvelope.statement.manifestDigest = sha('replacement')),
    (p) => (p.unifiedEnvelope.tenantId = p.unifiedEnvelope.packageId),
    (p) => (p.legacyArtifactId = 'missing'),
  ]) {
    const p = structuredClone(vectors.proof);
    mutate(p);
    assert(!proofValid(p, vectors.rawManifest));
  }
  assert(!proofValid(vectors.proof, vectors.rawManifest + ' '));
  assert(!proofValid(vectors.proof, vectors.rawManifest, sha('previous M')));
});
test('dual digest revocation, either alias disables version and artifact reuse disables another version', () => {
  const { legacyEnvelope: a, unifiedEnvelope: m } = vectors.proof;
  const entries = [a.statement.digest, m.statement.manifestDigest].map(
    (digest) => ({ packageId: a.packageId, versionId: a.versionId, digest }),
  );
  for (const list of [entries, [entries[0]], [entries[1]]])
    assert(revoked(m, list));
  const other = structuredClone(m);
  other.versionId = other.packageId;
  assert(revoked(other, [entries[0]]));
  assert(!revoked(other, []));
});
test('global watermark accepts proved equal-sequence aliases and rejects cross-protocol rollback or version substitution', () => {
  const p = vectors.proof,
    a = p.legacyEnvelope,
    m = p.unifiedEnvelope;
  const w = {
    tenantId: a.tenantId,
    packageId: a.packageId,
    versionId: a.versionId,
    releaseSequence: a.releaseSequence,
    verifiedDigestAliases: [a.statement.digest],
  };
  assert(accept(w, m, p, vectors.rawManifest));
  for (const e of [a, m]) {
    const low = structuredClone(e);
    low.releaseSequence--;
    assert(!accept(w, low, p, vectors.rawManifest));
  }
  for (const field of ['tenantId', 'versionId']) {
    const bad = structuredClone(m);
    bad[field] = m.packageId;
    assert(!accept(w, bad, p, vectors.rawManifest));
  }
});
test('v1 renewal fixed cutoff and downgrade authorization exact version', () => {
  const t0 = Date.parse(vectors.proof.activatedAt),
    cutoff = t0 + 86400000;
  const renewal = (now, notAfter) =>
    now < cutoff && notAfter <= cutoff && notAfter > now;
  assert(renewal(t0 + 1, cutoff));
  assert(!renewal(cutoff, cutoff + 1));
  assert(!renewal(t0 + 1, cutoff + 1));
  const target = vectors.proof.legacyEnvelope;
  const authorization = {
    ...target,
    instanceId: target.tenantId,
    digest: target.statement.digest,
    fromReleaseSequence: 8,
    toReleaseSequence: 7,
  };
  const valid = (e) =>
    ['tenantId', 'packageId', 'versionId'].every(
      (k) => e[k] === authorization[k],
    ) &&
    e.statement.digest === authorization.digest &&
    e.releaseSequence === authorization.toReleaseSequence;
  assert(valid(target));
  assert(!valid({ ...target, versionId: target.packageId }));
});
test('fixed mount configuration preserves preset roots, expressions and unrelated configuration', () => {
  const f = read('resource/cordis-upgrade-fixture.json');
  const before = structuredClone(f.before);
  before.plugins['skill-filesystem'].customSkillDirs.push(
    '<DSH_HOME>/auriga/skills-active/current/',
  );
  assert.deepEqual(before, f.after);
  assert.equal(f.identityInStaticConfig, false);
  const roots = f.after.plugins['skill-filesystem'].customSkillDirs;
  assert.equal(new Set(roots).size, roots.length);
});
test('public schema registry names and every definition compile', () => {
  const registry = read('resource/unified-registry.json');
  for (const name of Object.keys(registry.schemas))
    assert(name.startsWith('Unified'));
  for (const name of Object.keys(read('resource/unified.schema.json').$defs))
    assert(
      ajv.getSchema(
        new URL('resource/unified.schema.json', root).href + '#/$defs/' + name,
      ),
    );
});

test('risk floor, quorum and review role have machine-checked rejection cases', () => {
  const spec = read('openapi/resource.yaml');
  const uri = 'https://example.com/unified-openapi';
  function refs(v) {
    if (Array.isArray(v)) return v.map(refs);
    if (!v || typeof v !== 'object') return v;
    return Object.fromEntries(
      Object.entries(v).map(([k, x]) => [
        k,
        k === '$ref'
          ? x
              .replace('#/components/schemas/', '#/$defs/')
              .replace('../resource/', new URL('resource/', root).href)
          : refs(x),
      ]),
    );
  }
  ajv.addSchema({ $id: uri, $defs: refs(spec.components.schemas) });
  const risk = ajv.getSchema(uri + '#/$defs/UnifiedRiskAssessment');
  assert(
    risk({
      minimumLevel: 'r2',
      level: 'r2',
      requiredReviewers: 2,
      requiredSecurityReviewers: 1,
    }),
  );
  for (const bad of [
    {
      minimumLevel: 'r2',
      level: 'r1',
      requiredReviewers: 1,
      requiredSecurityReviewers: 0,
    },
    {
      minimumLevel: 'r3',
      level: 'r2',
      requiredReviewers: 2,
      requiredSecurityReviewers: 1,
    },
    {
      minimumLevel: 'r2',
      level: 'r2',
      requiredReviewers: 1,
      requiredSecurityReviewers: 0,
    },
  ])
    assert(!risk(bad));
  const decision = ajv.getSchema(uri + '#/$defs/UnifiedReviewDecision');
  const body = {
    decision: 'approve',
    reason: 'Reviewed',
    versionRevision: 1,
    digest: vectors.proof.unifiedEnvelope.statement.manifestDigest,
    role: 'security',
  };
  assert(decision(body));
  assert(!decision({ ...body, role: 'administrator' }));
  const quorum = (author, decisions) => {
    const reviewers = new Map();
    for (const d of decisions)
      if (d.id !== author && (d.role !== 'security' || d.qualified))
        reviewers.set(d.id, d);
    return (
      reviewers.size >= 2 &&
      [...reviewers.values()].some((d) => d.role === 'security' && d.qualified)
    );
  };
  const general = { id: 'reviewer-1', role: 'general' },
    security = { id: 'reviewer-2', role: 'security', qualified: true };
  assert(quorum('author', [general, security]));
  assert(!quorum('author', [general, { ...security, id: 'author' }]));
  assert(!quorum('author', [general, { ...security, id: general.id }]));
  assert(!quorum('author', [general, { ...security, qualified: false }]));
});
test('manifest ACL is rechecked on every read and old/new event digest fields cannot mix', () => {
  const spec = read('openapi/resource.yaml');
  const op =
    spec.paths[
      '/api/resource/v1/catalog/packages/{packageId}/versions/{versionId}/manifest'
    ].get;
  assert.deepEqual(op['x-authorization'], {
    tenant: 'authenticated',
    visibility: 'package-version',
    resolveKind: true,
    allowedKinds: ['skill', 'tool'],
    pluginStatus: 404,
    locatorGrantsAccess: false,
    checkEveryRead: true,
  });
  const allowed = (c) =>
    c.sameTenant &&
    c.visibleVersion &&
    c.permission &&
    ['skill', 'tool'].includes(c.kind);
  const good = {
    sameTenant: true,
    visibleVersion: true,
    permission: true,
    kind: 'skill',
  };
  assert(allowed(good));
  for (const field of ['sameTenant', 'visibleVersion', 'permission'])
    assert(
      !allowed({
        ...good,
        [field]: false,
        knowsDigest: true,
        hasLocator: true,
      }),
    );
  assert(!allowed({ ...good, kind: 'plugin' }));
  const eventFile = new URL('events/resource.schema.json', root);
  ajv.addSchema(read('events/resource.schema.json'), eventFile.href);
  const old = ajv.getSchema(
      eventFile.href + '#/$defs/ResourceVersionPublished',
    ),
    unified = ajv.getSchema(
      eventFile.href + '#/$defs/UnifiedResourceVersionPublished',
    );
  const e = {
    packageId: vectors.proof.legacyEnvelope.packageId,
    versionId: vectors.proof.legacyEnvelope.versionId,
    version: '1.2.3',
    digest: vectors.proof.legacyEnvelope.statement.digest,
    revision: 1,
  };
  assert(old(e));
  assert(!unified(e));
  const { digest, ...common } = e;
  const next = {
    ...common,
    kind: 'skill',
    signatureVersion: 2,
    manifestDigest: vectors.proof.unifiedEnvelope.statement.manifestDigest,
    artifactDigests: [digest],
  };
  assert(unified(next));
  assert(!old(next));
  assert(!unified({ ...next, digest }));
});
test('guardian private-only state requires stopped process, cleared view and new session', () => {
  const p = vectors.proof.legacyEnvelope;
  const value = {
    tenantId: p.tenantId,
    ownerId: p.packageId,
    hostId: p.versionId,
    selectionRevision: 1,
    generation: 1,
    state: 'private_only',
    reason: 'lease_expired',
    lease: {
      envelopeNotAfter: p.notAfter,
      revocationsExpiresAt: p.notAfter,
      expiresAt: p.notAfter,
      revocationRevision: 1,
    },
    processStopped: true,
    viewCleared: true,
    newSessionRequired: true,
  };
  assert(check('unified', 'UnifiedGuardianState', value));
  for (const field of ['processStopped', 'viewCleared', 'newSessionRequired'])
    assert(
      !check('unified', 'UnifiedGuardianState', { ...value, [field]: false }),
    );
  assert(
    !check('unified', 'UnifiedGuardianState', { ...value, state: 'unknown' }),
  );
});

test('kind/delivery matrix fails closed for tool, plugin and unknown kinds at unified P1 entry', () => {
  const matrix = read('resource/unified-authorization.json');
  const allowed = (kind, delivery, permission) =>
    matrix.rules.some(
      (r) =>
        r.kind === kind &&
        r.delivery === delivery &&
        r.entry === 'unified' &&
        r.p1Enabled === true &&
        permission === r.basePermission,
    );
  assert(allowed('skill', 'text-only', 'resource.packages.upload'));
  for (const rule of matrix.rules.filter((r) => r.kind !== 'skill'))
    assert(!allowed(rule.kind, rule.delivery, 'resource.packages.upload'));
  assert(!allowed('unknown', 'text-only', 'resource.packages.upload'));
  assert(!allowed('skill', 'text-only', 'resource.packages.read'));
  assert.equal(matrix.default, 'deny');
  assert.equal(matrix.securityReviewRoleRequiresQualification, true);
});

test('applied observations require current immutable identity and all three revision counters', () => {
  const identity = {
    tenantId: 'tenant',
    packageId: 'package',
    versionId: 'version',
    kind: 'skill',
    version: '1.0.0',
    manifestDigest: 'digest',
  };
  const current = {
    identity,
    installationRevision: 3,
    selectionRevision: 4,
    generation: 5,
  };
  const valid = (report, channel) =>
    report.state === 'applied' &&
    report.reportSource === channel &&
    ['installationRevision', 'selectionRevision', 'generation'].every(
      (k) => report[k] === current[k],
    ) &&
    report.observedGeneration === current.generation &&
    canon(report.identity) === canon(current.identity);
  const good = {
    ...current,
    observedGeneration: 5,
    state: 'applied',
    reportSource: 'node',
  };
  assert(valid(good, 'node'));
  assert(!valid(good, 'member'));
  for (const field of [
    'installationRevision',
    'selectionRevision',
    'generation',
    'observedGeneration',
  ])
    assert(!valid({ ...good, [field]: 1 }, 'node'));
  for (const state of [
    'applying',
    'pending_restart',
    'superseded',
    'overridden',
    'expired',
    'revoked',
    'failed',
  ])
    assert(!valid({ ...good, state }, 'node'));
  assert(
    !valid({ ...good, identity: { ...identity, versionId: 'other' } }, 'node'),
  );
});

test('migration fixture artifact length matches original archive bytes', () => {
  const manifest = JSON.parse(vectors.rawManifest);
  assert.equal(
    manifest.artifacts[0].sizeBytes,
    Buffer.from(vectors.legacyArchiveBase64, 'base64').length,
  );
});
