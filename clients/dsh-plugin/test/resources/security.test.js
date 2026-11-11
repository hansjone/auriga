import { test } from 'node:test';
/* global structuredClone */
import assert from 'node:assert/strict';
import { generateKeyPairSync, sign, createHash } from 'node:crypto';
import { writeFile, mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { gzipSync, gunzipSync } from 'node:zlib';
import {
  verifyRevocations,
  readResourceRoots,
} from '../../src/resources/trust.js';
import { canonicalize, parseStrictJson } from '../../src/resources/json.js';
import { verifyResourceArchive } from '../../src/resources/verify.js';
import { validateArchive } from '../../src/resources/archive.js';
import { resourceHost } from '../../src/resources/host.js';
import { id, tarball } from './fixtures.js';
const now = Date.parse('2026-11-12T00:00:00Z');
const signed = (body, key, kid = 'revocation') => ({
  ...body,
  signature: {
    alg: 'Ed25519',
    kid,
    value: sign(null, Buffer.from(canonicalize(body)), key.privateKey).toString(
      'base64url',
    ),
  },
});
test('revocation verification binds independent roots, tenant, revision and original validity', () => {
  const key = generateKeyPairSync('ed25519'),
    release = generateKeyPairSync('ed25519');
  const keys = new Map([['revocation', key.publicKey]]);
  const base = {
    schemaVersion: 1,
    tenantId: id(1),
    revision: 2,
    issuedAt: '2026-11-11T23:59:59Z',
    expiresAt: '2026-11-12T01:00:00Z',
    revokedKids: [],
    revokedArtifacts: [],
  };
  const current = verifyRevocations(signed(base, key), id(1), keys, now);
  for (const change of [
    (v) => {
      v.tenantId = id(9);
    },
    (v) => {
      v.revision = 1;
    },
    (v) => {
      v.expiresAt = '2026-11-12T00:00:00Z';
    },
    (v) => {
      v.extra = 1;
    },
    (v) => {
      v.revokedKids = ['release', 'release'];
    },
    (v) => {
      v.revokedArtifacts = [
        { packageId: id(2), versionId: id(3), digest: 'bad' },
      ];
    },
  ]) {
    const body = structuredClone(base);
    change(body);
    assert.throws(
      () => verifyRevocations(signed(body, key), id(1), keys, now, current),
      { code: 'resource.signature_invalid' },
    );
  }
  assert.throws(
    () =>
      verifyRevocations(
        signed({ ...base, expiresAt: '2026-11-12T02:00:00Z' }, key),
        id(1),
        keys,
        now,
        current,
      ),
    { code: 'resource.signature_invalid' },
  );
  assert.throws(
    () => verifyRevocations(signed(base, release), id(1), keys, now),
    { code: 'resource.signature_invalid' },
  );
});
test('resource trust roots come only from an explicit independent local deployment file', async (t) => {
  const dir = await mkdtemp(join(tmpdir(), 'auriga-resource-roots-'));
  t.after(() => rm(dir, { recursive: true, force: true }));
  const release = generateKeyPairSync('ed25519'),
    revocation = generateKeyPairSync('ed25519');
  const entry = (kid, key) => ({
    kid,
    publicKeySpki: key.publicKey
      .export({ format: 'der', type: 'spki' })
      .toString('base64url'),
  });
  const value = {
    schemaVersion: 1,
    releaseKeys: [entry('release', release)],
    revocationKeys: [entry('revocation', revocation)],
  };
  const file = join(dir, 'roots.json');
  await writeFile(file, JSON.stringify(value), { mode: 0o600 });
  assert.equal((await readResourceRoots(file)).releaseKeys.size, 1);
  await assert.rejects(readResourceRoots(undefined), {
    code: 'resource.storage_unavailable',
  });
  value.revocationKeys = [entry('revocation', release)];
  await writeFile(file, JSON.stringify(value));
  await assert.rejects(readResourceRoots(file), {
    code: 'resource.signature_invalid',
  });
});
test('archive admission rejects traversal, links, scripts, unresolved dependencies and compression bombs without extraction', () => {
  const manifest = {
    name: 'example-plugin',
    version: '1.0.0',
    dsh: { bundle: { patch: './cordis.patch.yml' } },
  };
  const expected = { name: manifest.name, version: manifest.version };
  assert.deepEqual(validateArchive(tarball(manifest), expected), expected);
  for (const archive of [
    tarball({ ...manifest, scripts: { install: 'dangerous' } }),
    tarball({ ...manifest, dependencies: { unverified: '1.0.0' } }),
    tarball(manifest, { 'package/../escape': 'x' }),
    gzipSync(Buffer.alloc(1048576)),
  ])
    assert.throws(() => validateArchive(archive, expected));
  const raw = gunzipSync(tarball(manifest));
  raw[156] = 50;
  raw.fill(32, 148, 156);
  raw.write(
    raw
      .subarray(0, 512)
      .reduce((a, b) => a + b, 0)
      .toString(8)
      .padStart(6, '0') + '\0 ',
    148,
  );
  assert.throws(() => validateArchive(gzipSync(raw), expected));
  const vendored = {
    'package/node_modules/dep/package.json': JSON.stringify({
      name: 'dep',
      version: '1.0.0',
    }),
  };
  assert.throws(() =>
    validateArchive(
      tarball({ ...manifest, dependencies: { dep: '1.0.0' } }, vendored),
      expected,
    ),
  );
  assert.deepEqual(
    validateArchive(
      tarball(
        {
          ...manifest,
          dependencies: { dep: '1.0.0' },
          bundledDependencies: ['dep'],
        },
        vendored,
      ),
      expected,
    ),
    expected,
  );
  const wrongMagic = gunzipSync(tarball(manifest));
  wrongMagic.write('v7tar\0', 257);
  wrongMagic.fill(32, 148, 156);
  wrongMagic.write(
    wrongMagic
      .subarray(0, 512)
      .reduce((a, b) => a + b, 0)
      .toString(8)
      .padStart(6, '0') + '\0 ',
    148,
  );
  assert.throws(() => validateArchive(gzipSync(wrongMagic), expected));
});
test('strict resource JSON rejects rounded fractional counters and ambiguous bytes while retaining general package numbers', () => {
  for (const source of [
    '{"n":2.0000000000000000001}',
    '{"n":9007199254740991.1}',
    '{"n":1e-400}',
    '{"n":1,"n":1}',
  ])
    assert.throws(() => parseStrictJson(Buffer.from(source), true));
  assert.equal(parseStrictJson(Buffer.from('{"n":2.00e0}'), true).n, 2);
  assert.equal(parseStrictJson(Buffer.from('{"n":0.5}')).n, 0.5);
  assert.throws(() => parseStrictJson(Buffer.from([0xff])));
});
test('all shortest fractional time mappings preserve signed text and agree with legacy instants', () => {
  const key = generateKeyPairSync('ed25519'),
    archive = Buffer.from('time-vector');
  for (const fraction of [
    '',
    '.001',
    '.01',
    '.1',
    '.123',
    '.1234',
    '.12345',
    '.123456',
    '.1234567',
    '.12345678',
    '.123456789',
  ]) {
    const issuedAt = '2026-11-12T00:00:00' + fraction + 'Z';
    const body = {
      schemaVersion: 1,
      tenantId: id(1),
      packageId: id(2),
      versionId: id(3),
      releaseSequence: 1,
      notAfter: '2026-11-12T01:00:00Z',
      statement: {
        purpose: 'plugin',
        name: 'example',
        version: '1.0.0',
        os: 'any',
        arch: 'any',
        digest: 'sha256:' + createHash('sha256').update(archive).digest('hex'),
        issuedAt,
      },
    };
    const wire = Buffer.from(JSON.stringify(signed(body, key, 'release')));
    const legacy = body.statement.issuedAt.replace(
      /(\.\d+)Z$/,
      (_all, f) => f.padEnd(10, '0') + 'Z',
    );
    assert.equal(
      verifyResourceArchive(wire, archive, {
        expected: {
          ...body,
          statement: { ...body.statement, issuedAt: legacy },
          sizeBytes: archive.length,
        },
        releaseKeys: new Map([['release', key.publicKey]]),
        now: Date.parse(issuedAt) + 1,
      }).statement.issuedAt,
      issuedAt,
    );
  }
});
test('public host adapter installs only the verified local file and never approves build scripts', async () => {
  const calls = [];
  const manager = {
    listBundles: async () => [],
    installBundle: async (spec, options) => {
      calls.push({ spec, options });
      return { target: 'example', application: 'applied' };
    },
    setBundleEnabled: async () => ({
      target: 'example',
      application: 'restart-required',
    }),
    removeBundle: async () => ({ target: 'example', application: 'applied' }),
  };
  const host = resourceHost({ get: () => manager }, { stop: () => {} });
  await host.install(join(tmpdir(), 'verified.tgz'), 'example');
  assert.equal(calls[0].options.enabled, false);
  assert.deepEqual(calls[0].options.approvedBuilds, []);
  assert(calls[0].spec.startsWith('file:'));
  assert.equal((await host.enable('example', true)).restartRequired, true);
  await assert.rejects(
    host.install('https://example.com/plugin.tgz', 'example'),
  );
});
