import { resourceBroker } from './broker-fixture.js';
import { generateKeyPairSync, sign, createHash } from 'node:crypto';
import { gzipSync } from 'node:zlib';
import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { canonicalize } from '../../src/resources/json.js';
import { openMemberResources } from './broker-fixture.js';
export const id = (n) =>
  `01900000-0000-7000-8000-${String(n).padStart(12, '0')}`;
export function tarball(manifest, extra = {}) {
  const files = {
    'package/package.json': JSON.stringify(manifest),
    'package/cordis.patch.yml': 'plugins: {}\n',
    ...extra,
  };
  const parts = [];
  for (const [name, body] of Object.entries(files)) {
    const bytes = Buffer.from(body),
      h = Buffer.alloc(512);
    h.write(name);
    h.write('0000644\0', 100);
    h.write('0000000\0', 108);
    h.write('0000000\0', 116);
    h.write(bytes.length.toString(8).padStart(11, '0') + '\0', 124);
    h.write('00000000000\0', 136);
    h.fill(32, 148, 156);
    h[156] = 48;
    h.write('ustar\0', 257);
    h.write('00', 263);
    const sum = h.reduce((a, b) => a + b, 0);
    h.write(sum.toString(8).padStart(6, '0') + '\0 ', 148);
    parts.push(h, bytes, Buffer.alloc((512 - (bytes.length % 512)) % 512));
  }
  return gzipSync(Buffer.concat([...parts, Buffer.alloc(1024)]));
}
export async function fixture(t, options = {}) {
  const directory = await mkdtemp(join(tmpdir(), 'auriga-market-'));
  t.after(() => rm(directory, { recursive: true, force: true }));
  let clock = Date.parse('2026-11-12T00:00:00Z');
  const release = generateKeyPairSync('ed25519'),
    revocation = generateKeyPairSync('ed25519');
  let archive = tarball({
    name: 'example-plugin',
    version: '1.0.0',
    dsh: { bundle: { patch: './cordis.patch.yml' } },
  });
  const digest = 'sha256:' + createHash('sha256').update(archive).digest('hex');
  const pack = {
    id: id(4),
    tenantId: id(1),
    ownerId: id(2),
    name: 'example-plugin',
    kind: 'plugin',
    enabled: true,
    level: 'l3',
    visibility: 'tenant',
  };
  const version = {
    id: id(5),
    packageId: id(4),
    version: '1.0.0',
    digest,
    sizeBytes: archive.length,
    releaseSequence: 1,
    state: 'published',
  };
  const envelope = {
    schemaVersion: 1,
    tenantId: id(1),
    packageId: id(4),
    versionId: id(5),
    releaseSequence: 1,
    notAfter: '2026-11-12T01:00:00Z',
    statement: {
      purpose: 'plugin',
      name: pack.name,
      version: version.version,
      os: 'any',
      arch: 'any',
      digest,
      issuedAt: '2026-11-11T23:59:59.123456789Z',
    },
  };
  const revocations = {
    schemaVersion: 1,
    tenantId: id(1),
    revision: 1,
    issuedAt: '2026-11-11T23:59:59Z',
    expiresAt: '2026-11-12T01:00:00Z',
    revokedKids: [],
    revokedArtifacts: [],
  };
  const signed = (value, key, kid) => ({
    ...value,
    signature: {
      alg: 'Ed25519',
      kid,
      value: sign(
        null,
        Buffer.from(canonicalize(value)),
        key.privateKey,
      ).toString('base64url'),
    },
  });
  const calls = [],
    hostCalls = [];
  const archiveVersions = new Map([[digest, '1.0.0']]);
  const oldVersions = new Map();
  const oldDownloads = new Map();
  let issuedArchive;
  const identityListeners = new Set();
  let claims = { tid: id(1), sub: id(2), did: id(3) },
    offline = false,
    denied = false;
  let row,
    observed,
    downloadFailures = 0,
    invalidSignature = false,
    reportFailures = 0;
  const runtime = {
    config: { platformOrigin: 'https://example.com' },
    vault: {
      subscribeResourceIdentity(listener) {
        identityListeners.add(listener);
        return () => identityListeners.delete(listener);
      },
      snapshot: () => ({
        credential: claims ? { claims } : null,
      }),
    },
    getAccess: async () => 'test-member-token',
  };
  const fetchImpl = async (url, init) => {
    const path = url.pathname,
      body = init.body ? JSON.parse(init.body) : undefined;
    calls.push({ path, init, body });
    if (offline) throw new Error('offline');
    if (path === `/api/resource/v1/packages/${id(4)}`)
      return Response.json(pack);
    if (oldVersions.has(path)) return Response.json(oldVersions.get(path));
    if (path === `/api/resource/v1/packages/${id(4)}/versions/${version.id}`)
      return Response.json(version);
    if (path.endsWith('/download-tickets')) {
      if (denied) return Response.json({}, { status: 403 });
      const old = oldDownloads.get(body.versionId);
      const e = signed(old?.envelope ?? envelope, release, 'release');
      issuedArchive = old?.archive ?? archive;
      if (invalidSignature) e.statement.name = 'tampered';
      return Response.json(
        {
          clientId: id(3),
          ticket: 'a'.repeat(43),
          expiresAt: new Date(clock + 60000).toISOString(),
          resourceEnvelope: e,
          revocations: signed(revocations, revocation, 'revocation'),
        },
        { status: 201 },
      );
    }
    if (path.endsWith('/downloads')) {
      if (downloadFailures-- > 0) throw new Error('disconnect');
      return new Response(issuedArchive ?? archive, {
        headers: { 'content-type': 'application/gzip' },
      });
    }
    if (path.endsWith('/revocations'))
      return Response.json(signed(revocations, revocation, 'revocation'));
    if (path.endsWith('/installations') && init.method === 'GET')
      return Response.json({ items: row ? [row] : [] });
    if (path.endsWith('/installations')) {
      if (init.headers['if-match'] !== `"${row?.revision ?? 0}"`)
        return Response.json({}, { status: 412 });
      row = {
        clientId: id(3),
        packageId: id(4),
        versionId: body.versionId,
        digest: body.digest,
        releaseSequence:
          oldVersions.get(
            `/api/resource/v1/packages/${id(4)}/versions/${body.versionId}`,
          )?.releaseSequence ?? version.releaseSequence,
        level: pack.level,
        desired: {
          install: 'enabled',
          enable: 'enabled',
          disable: 'disabled',
          remove: 'removed',
        }[body.action],
        state: 'applying',
        revision: (row?.revision ?? 0) + 1,
        reportSource: 'member',
      };
      return Response.json(row, { status: 202 });
    }
    if (path.endsWith('/installation-reports')) {
      row = {
        ...row,
        state: body.state,
        reportedRevision: body.installationRevision,
      };
      if (reportFailures-- > 0) throw new Error('report response lost');
      return Response.json(row);
    }
    throw new Error('unexpected resource request ' + path);
  };
  const host = {
    observe: async () => observed,
    install: async (file) => {
      hostCalls.push('install');
      const installedDigest =
        'sha256:' +
        createHash('sha256')
          .update(await readFile(file))
          .digest('hex');
      if (!archiveVersions.has(installedDigest))
        throw new Error('bytes changed');
      observed = {
        installed: true,
        name: pack.name,
        version: archiveVersions.get(installedDigest),
        enabled: false,
      };
      return { restartRequired: false };
    },
    enable: async (_name, enabled) => {
      hostCalls.push(enabled ? 'enable' : 'disable');
      observed = { ...observed, enabled };
      return { restartRequired: false };
    },
    remove: async () => {
      hostCalls.push('remove');
      observed = undefined;
      return { restartRequired: false };
    },
    stop: () => hostCalls.push('stop'),
  };
  resourceBroker(runtime, options.fetchImpl ?? fetchImpl);
  const engineOptions = {
    directory,
    runtime,
    identity: { tenantId: id(1), ownerId: id(2), clientId: id(3) },
    host,
    fetchImpl,
    roots: async () => ({
      releaseKeys: new Map([['release', release.publicKey]]),
      revocationKeys: new Map([['revocation', revocation.publicKey]]),
    }),
    now: () => clock,
    ...options,
  };
  let engine = await openMemberResources(engineOptions);
  t.after(() => engine.close());
  return {
    get engine() {
      return engine;
    },
    reopen: async () => {
      await engine.close();
      engine = await openMemberResources(engineOptions);
    },
    failReports: (n) => {
      reportFailures = n;
    },
    calls,
    hostCalls,
    pack,
    version,
    envelope,
    revocations,
    runtime,
    host,
    directory,
    engineOptions,
    fetchImpl,
    writeRoots: async () => {
      const file = join(directory, 'roots.json');
      const entry = (kid, key) => ({
        kid,
        publicKeySpki: key.publicKey
          .export({ type: 'spki', format: 'der' })
          .toString('base64url'),
      });
      await writeFile(
        file,
        JSON.stringify({
          schemaVersion: 1,
          releaseKeys: [entry('release', release)],
          revocationKeys: [entry('revocation', revocation)],
        }),
        { mode: 0o600 },
      );
      return file;
    },
    logout: () => {
      claims = null;
      return Promise.allSettled(
        [...identityListeners].map((listener) => listener()),
      );
    },
    switchMember: () => {
      claims = { tid: id(1), sub: id(8), did: id(9) };
      return Promise.allSettled(
        [...identityListeners].map((listener) => listener()),
      );
    },
    offline: (value = true) => {
      offline = value;
    },
    denyRenewal: () => {
      denied = true;
    },
    renew: () => {
      envelope.statement.issuedAt = new Date(clock - 1000).toISOString();
      envelope.notAfter = new Date(clock + 3600000).toISOString();
      revocations.revision++;
      revocations.issuedAt = new Date(clock - 1000).toISOString();
      revocations.expiresAt = new Date(clock + 3600000).toISOString();
    },
    upgrade: () => {
      oldVersions.set(
        `/api/resource/v1/packages/${id(4)}/versions/${version.id}`,
        { ...version },
      );
      oldDownloads.set(version.id, {
        archive,
        envelope: JSON.parse(JSON.stringify(envelope)),
      });
      archive = tarball({
        name: pack.name,
        version: '2.0.0',
        dsh: { bundle: { patch: './cordis.patch.yml' } },
      });
      version.id = id(6);
      version.version = '2.0.0';
      version.digest =
        'sha256:' + createHash('sha256').update(archive).digest('hex');
      version.sizeBytes = archive.length;
      version.releaseSequence = 2;
      envelope.versionId = version.id;
      envelope.releaseSequence = 2;
      envelope.statement.version = version.version;
      envelope.statement.digest = version.digest;
      archiveVersions.set(version.digest, version.version);
    },
    invalidSignature: () => {
      invalidSignature = true;
    },
    breakDownloads: (n) => {
      downloadFailures = n;
    },
    advance: (ms) => {
      clock += ms;
    },
  };
}
