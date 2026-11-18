import test from 'node:test';
import assert from 'node:assert/strict';
import { Buffer } from 'node:buffer';
import { execFileSync, spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { mkdtemp, readFile, rmdir, unlink, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { gzipSync, gunzipSync } from 'node:zlib';
import { schemaValid } from '../clients/dsh-plugin/src/resources/schema.js';
import { publishPlugin } from './publish-unified-plugin.mjs';

const archive = new URL(
  '../services/resource/impl/src/test/resources/plugin/example-dsh-plugin-1.0.0.tgz',
  import.meta.url,
);
const payload = await readFile(archive);
const digest = (bytes) =>
  'sha256:' + createHash('sha256').update(bytes).digest('hex');
const manifest = {
  schemaVersion: 1,
  kind: 'plugin',
  name: 'example-seed',
  version: '1.0.0',
  description: 'Seed fixture with prepared capabilities',
  author: { displayName: 'Fixture' },
  license: 'MIT',
  permissions: {
    tools: ['fixture_read'],
    outboundHosts: [],
    dataAccess: ['workspace_read', 'user_selected_files'],
  },
  runtimes: [{ kind: 'node', versionRange: '>=22.0.0' }],
  plugin: {
    packageName: '@example/dsh-plugin',
    dshVersionRange: '>=0.2.1-rc.3',
  },
  artifacts: [
    {
      id: 'payload',
      role: 'payload',
      digest: digest(payload),
      sizeBytes: payload.length,
      mediaType: 'application/gzip',
      os: 'any',
      arch: 'any',
    },
  ],
  signature: { mode: 'detached', algorithm: 'Ed25519' },
};
const rawManifest = Buffer.from(JSON.stringify(manifest, null, 2) + '\n');

const adminOrigin = 'https://admin.example.test';
const publishedPath =
  '/api/resource/v1/admin/catalog/published?kind=plugin&limit=1';
const tokens = { author: 'a', general: 'g', security: 's', publisher: 'p' };

function manifestBytes(tgz, changes) {
  return Buffer.from(
    JSON.stringify(
      {
        ...manifest,
        ...changes,
        artifacts: [
          { ...manifest.artifacts[0], digest: digest(tgz), sizeBytes: tgz.length },
        ],
      },
      null,
      2,
    ) + '\n',
  );
}

function packageMetadataVariant(changes) {
  const tar = gunzipSync(payload);
  for (let offset = 0; offset < tar.length; ) {
    const header = Buffer.from(tar.subarray(offset, offset + 512));
    if (header.every((byte) => byte === 0)) break;
    const name = header.toString('utf8', 0, 100).split('\0')[0];
    const size = Number.parseInt(header.toString('ascii', 124, 136), 8);
    const start = offset + 512;
    const next = start + Math.ceil(size / 512) * 512;
    if (name === 'package/package.json') {
      const metadata = JSON.parse(tar.subarray(start, start + size));
      const updated = Buffer.from(JSON.stringify({ ...metadata, ...changes }));
      header.write(updated.length.toString(8).padStart(11, '0') + '\0', 124);
      header.fill(32, 148, 156);
      const checksum = header.reduce((sum, byte) => sum + byte, 0);
      header.write(checksum.toString(8).padStart(6, '0') + '\0 ', 148);
      return gzipSync(
        Buffer.concat([
          tar.subarray(0, offset),
          header,
          updated,
          Buffer.alloc((512 - (updated.length % 512)) % 512),
          tar.subarray(next),
        ]),
      );
    }
    offset = next;
  }
  assert.fail('fixture has no package/package.json');
}

const noNetwork = () => assert.fail('unexpected preflight or write request');

function adminPreflight(url, request) {
  assert.equal(request.redirect, 'error');
  if (url.pathname === '/resources/plugins') {
    assert.equal(request.headers, undefined);
    return new Response('<meta name="auriga-access-mode" content="domain"/>', {
      headers: { 'Content-Type': 'text/html; charset=utf-8' },
    });
  }
  assert.equal(url.pathname + url.search, publishedPath);
  if (!request.headers) return new Response(null, { status: 401 });
  assert.deepEqual(request.headers, { Authorization: 'Bearer p' });
  return Response.json({ items: [] });
}

async function assertPublication(tgz, sidecar) {
  const calls = [];
  const reads = [];
  const expected = JSON.parse(sidecar);
  const manifestDigest = digest(sidecar);
  const send = async (url, request) => {
    assert.equal(url.origin, adminOrigin);
    assert.equal(request.redirect, 'error');
    if (request.method === 'GET') {
      assert.equal(calls.length, 0);
      reads.push(url.pathname + url.search);
      return adminPreflight(url, request);
    }
    calls.push({ path: url.pathname, ...request });
    let result;
    switch (calls.length) {
      case 1:
        assert.deepEqual(JSON.parse(request.body), {
          name: expected.name,
          kind: 'plugin',
          visibility: 'tenant',
        });
        result = { id: 'package-id', revision: 1 };
        break;
      case 2: {
        assert.equal(request.headers['If-Match'], '"1"');
        assert(request.body instanceof globalThis.FormData);
        const raw = Buffer.from(
          await request.body.get('manifest').arrayBuffer(),
        );
        assert.deepEqual(raw, sidecar);
        assert.equal(
          JSON.parse(await request.body.get('metadata').text()).expectedDigest,
          manifestDigest,
        );
        assert.deepEqual(
          Buffer.from(await request.body.get('artifacts').arrayBuffer()),
          tgz,
        );
        result = { id: 'version-id', revision: 1 };
        break;
      }
      case 3:
        result = {
          reviewId: 'review-id',
          revision: 1,
          version: { revision: 2, digest: manifestDigest },
        };
        break;
      case 4:
      case 5: {
        const body = JSON.parse(request.body);
        assert.equal(body.role, calls.length === 4 ? 'general' : 'security');
        assert.equal(body.digest, manifestDigest);
        assert.equal(body.versionRevision, 2);
        assert.equal(
          request.headers['If-Match'],
          calls.length === 4 ? '"1"' : '"2"',
        );
        result = {
          reviewId: 'review-id',
          revision: calls.length - 2,
          version: { revision: 2, digest: manifestDigest },
        };
        break;
      }
      case 6:
        assert.equal(request.headers['If-Match'], '"2"');
        result = { state: 'published' };
        break;
      default:
        assert.fail('unexpected request');
    }
    return { ok: true, json: async () => result };
  };
  const receipt = await publishPlugin(
    new URL(adminOrigin),
    tgz,
    sidecar,
    tokens,
    send,
  );
  assert.deepEqual(receipt, {
    packageId: 'package-id',
    versionId: 'version-id',
    kind: 'plugin',
    state: 'published',
    manifestDigest,
    artifactDigest: digest(tgz),
  });
  assert.deepEqual(reads, ['/resources/plugins', publishedPath, publishedPath]);
  assert.deepEqual(
    calls.map((call) => call.headers.Authorization),
    ['Bearer a', 'Bearer a', 'Bearer a', 'Bearer g', 'Bearer s', 'Bearer p'],
  );
  assert.deepEqual(
    calls.map((call) => call.path),
    [
      '/api/resource/v1/catalog/packages',
      '/api/resource/v1/catalog/packages/package-id/versions',
      '/api/resource/v1/catalog/packages/package-id/versions/version-id/submissions',
      '/api/resource/v1/admin/catalog/reviews/review-id/decisions',
      '/api/resource/v1/admin/catalog/reviews/review-id/decisions',
      '/api/resource/v1/admin/catalog/packages/package-id/versions/version-id/publications',
    ],
  );
  assert.equal(
    new Set(calls.map((call) => call.headers['Idempotency-Key'])).size,
    6,
  );
}

test('seed uses original multipart bytes, distinct review roles, exact digest and revision', async () => {
  await assertPublication(payload, rawManifest);
});

test('schema-valid outbound hosts and npm metadata fail before any request when the catalog would reject them', async () => {
  for (const host of [
    'Example.TEST',
    '*.example.test',
    '192.0.02.10',
    '999.0.2.10',
    '::ffff:c000:20a',
    '2001:db8::g',
  ]) {
    const sidecar = manifestBytes(payload, {
      permissions: { ...manifest.permissions, outboundHosts: [host] },
    });
    assert(schemaValid('package', 'PluginManifest', JSON.parse(sidecar)), host);
    await assert.rejects(
      publishPlugin(adminOrigin, payload, sidecar, tokens, noNetwork),
      /outboundHosts/,
      host,
    );
  }

  const tgz = packageMetadataVariant({
    engines: { dsh: manifest.plugin.dshVersionRange },
  });
  await assert.rejects(
    publishPlugin(
      adminOrigin,
      tgz,
      manifestBytes(tgz, {
        plugin: { ...manifest.plugin, dshVersionRange: '*' },
      }),
      tokens,
      noNetwork,
    ),
    /engines\.dsh must exactly match/,
  );
  await assertPublication(
    tgz,
    manifestBytes(tgz, {
      permissions: {
        ...manifest.permissions,
        outboundHosts: ['example.test', '192.0.2.10', '2001:db8::1'],
      },
    }),
  );
});

test('catalog rejects reserved names, noncanonical ranges and unsupported npm declarations before preflight', async () => {
  const tar = gunzipSync(payload);
  const firstSize = Number.parseInt(tar.toString('ascii', 124, 136), 8);
  assert.notEqual(firstSize % 512, 0);
  tar[512 + firstSize] = 1;
  for (const [tgz, changes, error] of [
    [
      payload,
      { plugin: { ...manifest.plugin, packageName: '@deepseek-ai/dsh' } },
      /reserved npm package name/,
    ],
    [
      payload,
      { plugin: { ...manifest.plugin, dshVersionRange: ' >=0.2.1-rc.3' } },
      /full SemVer/,
    ],
    [
      packageMetadataVariant({ engines: null }),
      {},
      /engines must be an object/,
    ],
    [
      packageMetadataVariant({ gypfile: false }),
      {},
      /native build declaration/,
    ],
    [
      packageMetadataVariant({ pnpm: { onlyBuiltDependencies: [] } }),
      {},
      /pnpm build declarations/,
    ],
    [
      Buffer.concat([payload, gzipSync(Buffer.alloc(0))]),
      {},
      /one gzip member/,
    ],
    [gzipSync(tar), {}, /nonzero ustar padding/],
  ]) {
    await assert.rejects(
      publishPlugin(
        adminOrigin,
        tgz,
        manifestBytes(tgz, changes),
        tokens,
        noNetwork,
      ),
      error,
    );
  }
});

test('seed rejects a mismatched payload or npm version before creating a package', async () => {
  const send = () => assert.fail('unexpected network request');
  const changedPayload = Buffer.from(payload);
  changedPayload[0] ^= 1;
  await assert.rejects(
    publishPlugin('https://example.com', changedPayload, rawManifest, {}, send),
    /does not match its manifest size and digest/,
  );
  const changedVersion = Buffer.from(
    JSON.stringify({ ...manifest, version: '1.0.1' }),
  );
  await assert.rejects(
    publishPlugin('https://example.com', payload, changedVersion, {}, send),
    /resource.apply_failed/,
  );
});

test('legacy abbreviated runtime range is rejected before contacting the origin', async () => {
  const legacy = Buffer.from(
    JSON.stringify({
      ...manifest,
      runtimes: [{ kind: 'node', versionRange: '>=22' }],
    }),
  );
  await assert.rejects(
    publishPlugin(adminOrigin, payload, legacy, tokens, () =>
      assert.fail('unexpected network request'),
    ),
    /full SemVer.*prepare\.mjs/,
  );
});

async function cliFixture(t) {
  const directory = await mkdtemp(join(tmpdir(), 'auriga-seed-preview-'));
  const sidecar = join(directory, 'example-seed.auriga-resource.json');
  await writeFile(sidecar, rawManifest);
  t.after(async () => {
    await unlink(sidecar);
    await rmdir(directory);
  });
  return [
    fileURLToPath(new URL('./publish-unified-plugin.mjs', import.meta.url)),
    fileURLToPath(archive),
    sidecar,
  ];
}

test('CLI with archive and manifest paths previews the original bytes offline', async (t) => {
  const preview = execFileSync(
    process.execPath,
    [
      '--import',
      'data:text/javascript,globalThis.fetch=()=>{throw new Error("unexpected fetch")}',
      ...(await cliFixture(t)),
    ],
    { env: { AURIGA_SEED_ORIGIN: 'invalid' } },
  );
  assert.deepEqual(preview, rawManifest);
});

test('publish CLI refuses IP, portal and unauthorized publisher entries before writes', async (t) => {
  const args = await cliFixture(t);
  const rejectEntry = `
    globalThis.fetch = async (url, { method, headers }) => {
      const entry = process.env.AURIGA_TEST_ENTRY;
      if (method !== 'GET' || entry === 'ip-literal') throw Error('unexpected request');
      if (url.pathname === '/resources/plugins') {
        const mode = entry === 'ip' ? 'ip' : 'domain';
        return new Response('<meta name="auriga-access-mode" content="' + mode + '"/>',
          { headers: { 'Content-Type': 'text/html; charset=utf-8' } });
      }
      if (url.pathname + url.search !== '${publishedPath}' || entry === 'ip')
        throw Error('unexpected preflight path');
      if (entry === 'portal' && headers) throw Error('unexpected credentials');
      return new Response(null, { status: entry === 'portal' || headers ? 403 : 401 });
    };
  `;
  for (const [entry, origin, error] of [
    ['ip-literal', 'https://192.0.2.10:8080', /admin origin in domain mode/],
    ['ip', adminOrigin, /admin origin in domain mode/],
    ['portal', 'https://portal.example.test', /admin origin in domain mode/],
    ['domain', adminOrigin, /publisher preflight failed \(403\)/],
  ]) {
    const result = spawnSync(
      process.execPath,
      [
        '--import',
        'data:text/javascript,' + encodeURIComponent(rejectEntry),
        ...args,
        '--publish',
      ],
      {
        encoding: 'utf8',
        env: {
          AURIGA_TEST_ENTRY: entry,
          AURIGA_SEED_ORIGIN: origin,
          AURIGA_SEED_AUTHOR_TOKEN: tokens.author,
          AURIGA_SEED_GENERAL_TOKEN: tokens.general,
          AURIGA_SEED_SECURITY_TOKEN: tokens.security,
          AURIGA_SEED_PUBLISHER_TOKEN: tokens.publisher,
        },
      },
    );
    assert.equal(result.status, 1, entry);
    assert.match(result.stderr, error, entry);
    assert.equal(result.stdout, '', entry);
  }
});

test('seed stops on rejected upload without leaking response or token', async () => {
  let count = 0;
  await assert.rejects(
    publishPlugin(
      adminOrigin,
      payload,
      rawManifest,
      { ...tokens, author: 'secret-value' },
      async (url, request) => {
        if (request.method === 'GET') return adminPreflight(url, request);
        count++;
        return count === 1
          ? { ok: true, json: async () => ({ id: 'p', revision: 1 }) }
          : { ok: false, status: 422, text: async () => 'secret-value' };
      },
    ),
    (error) =>
      !error.message.includes('secret-value') && error.message.includes('422'),
  );
  assert.equal(count, 2);
});
