import { clockBootId } from '../../src/resources/host-lock.js';
import { generateKeyPairSync, sign } from 'node:crypto';
import {
  mkdtempSync,
  mkdirSync,
  writeFileSync,
  rmSync,
  realpathSync,
} from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { canonicalize } from '../../src/resources/json.js';
import { packSkill } from '../../src/resources/skill-package.js';
import { sha, desktopSkills } from '../../src/resources/desktop-skills.js';
export function catalogFixture(t, temporaryRoot = tmpdir(), config = {}) {
  // Windows runner TEMP can contain an 8.3 user-directory alias. Production
  // deliberately rejects aliases, so use the physical spelling in the fixture.
  const home = realpathSync.native(
    mkdtempSync(join(temporaryRoot, 'auriga-catalog-')),
  );
  t.after(() => rmSync(home, { recursive: true, force: true }));
  const who = {
    tenantId: '019a0000-0000-7000-8000-000000000001',
    ownerId: '019a0000-0000-7000-8000-000000000012',
    hostId: '019a0000-0000-7000-8000-000000000011',
  };
  const packageId = config.packageId ?? '019a0000-0000-7000-8000-000000000002',
    versionId = config.versionId ?? '019a0000-0000-7000-8000-000000000004';
  const release = config.release ?? generateKeyPairSync('ed25519'),
    revocation = config.revocation ?? generateKeyPairSync('ed25519');
  const roots = async () => ({
    releaseKeys: new Map([['release-test', release.publicKey]]),
    revocationKeys: new Map([['revoke-test', revocation.publicKey]]),
  });
  const signed = (body, key, kid) => ({
    ...body,
    signature: {
      alg: 'Ed25519',
      kid,
      value: sign(
        null,
        Buffer.from(canonicalize(body)),
        key.privateKey,
      ).toString('base64url'),
    },
  });
  const sampleNow = config.nowMillis ?? Date.now();
  const now = new Date(sampleNow).toISOString(),
    issuedAt = new Date(sampleNow - 1000).toISOString(),
    notAfter = new Date(sampleNow + 7200000).toISOString(),
    expiresAt = new Date(sampleNow + 3600000).toISOString();
  const source =
    '---\nname: ' +
    (config.name ?? 'example') +
    '\ndescription: "Public fixture"\n---\nVerified body.\n';
  const archive = packSkill(
    { 'SKILL.md': source, ...config.files },
    { name: config.name ?? 'example', description: 'Public fixture' },
  );
  const manifest = {
    schemaVersion: 1,
    kind: 'skill',
    name: config.name ?? 'example',
    version: '1.0.0',
    description: 'Public fixture',
    author: { displayName: 'Example' },
    license: 'MIT',
    permissions: { tools: [], outboundHosts: [], dataAccess: [] },
    runtimes: [],
    signature: { mode: 'detached', algorithm: 'Ed25519' },
    artifacts: [
      {
        id: 'payload',
        role: 'payload',
        digest: sha(archive),
        sizeBytes: archive.length,
        mediaType: 'application/gzip',
        os: 'any',
        arch: 'any',
      },
    ],
    skill: {
      entry: 'SKILL.md',
      contentProfile: 'text-only',
      dshVersionRange: '>=0.2.1-rc.3',
    },
  };
  const raw = Buffer.from(JSON.stringify(manifest) + '\n');
  const id = {
    tenantId: who.tenantId,
    kind: 'skill',
    packageId,
    versionId,
    version: '1.0.0',
    manifestDigest: sha(raw),
  };
  const envelope = signed(
    {
      schemaVersion: 2,
      tenantId: who.tenantId,
      packageId,
      versionId,
      releaseSequence: 7,
      notAfter,
      statement: {
        purpose: 'resource',
        kind: 'skill',
        name: config.name ?? 'example',
        version: '1.0.0',
        manifestDigest: sha(raw),
        artifactDigests: [sha(archive)],
        issuedAt,
      },
    },
    release,
    'release-test',
  );
  const revoked = signed(
    {
      schemaVersion: 1,
      tenantId: who.tenantId,
      revision: 1,
      issuedAt,
      expiresAt,
      revokedKids: [],
      revokedArtifacts: [],
    },
    revocation,
    'revoke-test',
  );
  const base = '/api/resource/v1/catalog',
    vp = `${base}/packages/${packageId}/versions/${versionId}`;
  const resource = {
    identity: id,
    envelope,
    manifestDownloadPath: vp + '/manifest',
    artifacts: [
      {
        artifactId: 'payload',
        digest: sha(archive),
        sizeBytes: archive.length,
        downloadPath: vp + '/artifacts/payload',
      },
    ],
    lease: {
      envelopeNotAfter: notAfter,
      revocationsExpiresAt: expiresAt,
      expiresAt,
      revocationRevision: 1,
    },
    releaseSequence: 7,
    enabled: true,
    desiredState: 'ENABLED',
    installationRevision: 1,
  };
  let row = {
    clientId: who.hostId,
    resource,
    installationRevision: 1,
    selectionRevision: 7,
    generation: 9,
    state: 'applying',
    reportSource: 'member',
  };
  const runtime = {
    config: { platformOrigin: 'http://platform.example.test' },
    vault: {
      snapshot: () => ({
        credential: {
          claims: { tid: who.tenantId, sub: who.ownerId, did: who.hostId },
        },
      }),
    },
    getAccess: async () => 'fixture-token',
    resolve: () => who,
    checkCsrf: () => false,
  };
  let current = runtime;
  const requests = [];
  const fetchImpl = async (url, options) => {
    const path = url.pathname;
    requests.push({ path, options });
    if (
      options.credentials !== 'omit' ||
      options.redirect !== 'error' ||
      options.headers.cookie
    )
      throw new Error('credential boundary');
    if (path === vp)
      return Response.json({
        id: versionId,
        packageId,
        kind: 'skill',
        state: 'published',
        digest: id.manifestDigest,
        manifest,
        version: '1.0.0',
      });
    if (path === base + '/member/installations' && options.method === 'POST') {
      const input = JSON.parse(options.body);
      const enabled = ['install', 'enable'].includes(input.selection.action);
      if (options.headers['if-match'] !== '"0"')
        row = {
          ...row,
          installationRevision: row.installationRevision + 1,
          selectionRevision: row.selectionRevision + 1,
          generation: row.generation + 1,
        };
      row = {
        ...row,
        resource: {
          ...resource,
          enabled,
          desiredState: enabled
            ? 'ENABLED'
            : input.selection.action === 'remove'
              ? 'REMOVED'
              : 'DISABLED',
          installationRevision: row.installationRevision,
        },
        state: 'applying',
      };
      return Response.json(row, { status: 202 });
    }
    if (path === base + '/member/installations')
      return Response.json({ items: [row] });
    if (path === base + '/member/download-tickets')
      return Response.json({
        clientId: who.hostId,
        ticket: 'test-ticket',
        expiresAt: new Date(Date.now() + 60000).toISOString(),
        resource: row.resource,
        revocations: revoked,
      });
    if (path === vp + '/manifest')
      return new Response(raw, {
        headers: { 'content-type': 'application/json' },
      });
    if (path === base + '/member/downloads')
      return new Response(archive, {
        headers: { 'content-type': 'application/gzip' },
      });
    if (path === base + '/member/installation-reports') {
      const report = JSON.parse(options.body);
      row = { ...row, state: report.state, report };
      return Response.json(row);
    }
    throw new Error('unexpected request: ' + path);
  };
  const guardianDir = join(home, 'auriga/guardian');
  mkdirSync(guardianDir, { recursive: true });
  const state = {
    schemaVersion: 1,
    revision: 1,
    guardian: {
      ...who,
      selectionRevision: 7,
      generation: 9,
      state: 'verifying',
      reason: 'startup',
      lease: resource.lease,
      processStopped: false,
      viewCleared: true,
      newSessionRequired: true,
    },
    clock: {
      bootId: clockBootId('2026-11-12T00:00:00.000Z'),
      maxObservedTime: now,
      lastTrustedTime: now,
      lastTrustedTimeSource: 'authenticated_server',
      lastMonotonicMillis: 1000,
      leaseDeadlineMonotonicMillis: 3601000,
    },
  };
  const writeState = () =>
    writeFileSync(join(guardianDir, 'state.json'), JSON.stringify(state));
  writeState();
  const water = {
    schemaVersion: 1,
    revision: 1,
    entries: [
      {
        tenantId: who.tenantId,
        packageId,
        releaseSequence: 7,
        versionId,
        verifiedDigestAliases: [id.manifestDigest],
      },
    ],
    migrationMarkers: [],
    consumedDowngradeAuthorizationIds: [],
  };
  const allow = () => {
    state.guardian.selectionRevision = row.selectionRevision;
    state.guardian.generation = row.generation;
    state.guardian.lease = row.resource.lease;
    state.guardian.state = 'active';
    writeFileSync(
      join(guardianDir, 'high-watermarks.json'),
      JSON.stringify(water),
    );
    writeState();
  };
  const desktop = desktopSkills({
    home,
    time: () => ({ bootId: '2026-11-12T00:00:00.000Z', monotonicMillis: 1000 }),
  });
  return {
    home,
    who,
    packageId,
    versionId,
    manifest,
    raw,
    archive,
    resource,
    revoked,
    runtime,
    roots,
    desktop,
    state,
    allow,
    requests,
    fetchImpl,
    current: () => current,
    setCurrent: (r) => {
      current = r;
    },
    getRow: () => row,
    setRow: (r) => {
      row = r;
    },
    directory: join(
      home,
      'auriga/resources',
      who.tenantId,
      who.ownerId,
      who.hostId,
      'skills',
    ),
    active: join(home, 'auriga/skills-active/current'),
  };
}
