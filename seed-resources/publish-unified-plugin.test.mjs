import test from 'node:test';
import assert from 'node:assert/strict';
import { Buffer } from 'node:buffer';
import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { mkdtemp, readFile, rmdir, unlink, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
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

test('seed uses original multipart bytes, distinct review roles, exact digest and revision', async () => {
  const calls = [];
  const manifestDigest = digest(rawManifest);
  const send = async (url, request) => {
    assert.equal(url.origin, 'http://192.0.2.10:8080');
    assert.equal(request.redirect, 'error');
    calls.push({ path: url.pathname, ...request });
    let result;
    switch (calls.length) {
      case 1:
        assert.deepEqual(JSON.parse(request.body), {
          name: manifest.name,
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
        assert.deepEqual(raw, rawManifest);
        assert.equal(
          JSON.parse(await request.body.get('metadata').text()).expectedDigest,
          manifestDigest,
        );
        assert.deepEqual(
          Buffer.from(await request.body.get('artifacts').arrayBuffer()),
          payload,
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
    new URL('http://192.0.2.10:8080'),
    payload,
    rawManifest,
    { author: 'a', general: 'g', security: 's', publisher: 'p' },
    send,
  );
  assert.deepEqual(receipt, {
    packageId: 'package-id',
    versionId: 'version-id',
    kind: 'plugin',
    state: 'published',
    manifestDigest,
    artifactDigest: digest(payload),
  });
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

test('CLI with archive and manifest paths previews the original bytes offline', async (t) => {
  const directory = await mkdtemp(join(tmpdir(), 'auriga-seed-preview-'));
  const sidecar = join(directory, 'example-seed.auriga-resource.json');
  await writeFile(sidecar, rawManifest);
  t.after(async () => {
    await unlink(sidecar);
    await rmdir(directory);
  });
  const preview = execFileSync(
    process.execPath,
    [
      '--import',
      'data:text/javascript,globalThis.fetch=()=>{throw new Error("unexpected fetch")}',
      fileURLToPath(new URL('./publish-unified-plugin.mjs', import.meta.url)),
      fileURLToPath(archive),
      sidecar,
    ],
    { env: { AURIGA_SEED_ORIGIN: 'invalid' } },
  );
  assert.deepEqual(preview, rawManifest);
});

test('seed stops on rejected upload without leaking response or token', async () => {
  let count = 0;
  await assert.rejects(
    publishPlugin(
      'https://example.com',
      payload,
      rawManifest,
      { author: 'secret-value' },
      async () => {
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
