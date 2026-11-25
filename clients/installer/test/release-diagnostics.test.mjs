import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import { syncBuiltinESMExports } from 'node:module';
import { join, basename } from 'node:path';
import { spawnSync } from 'node:child_process';
import { generateKeyPairSync, sign } from 'node:crypto';
import { Buffer } from 'node:buffer';
import { buildPayload, tempBase, wrapSetupFixture } from './helpers.mjs';
import { systemTool } from '../runtime/system-tools.mjs';
import { makeRelease } from '../scripts/make-release.mjs';
import { reviewRelease } from '../scripts/sign-release.mjs';
import { publish } from '../../../scripts/client-release/publish.mjs';
import { fingerprint } from '../../../scripts/client-release/keys.mjs';
import { canonical, RELEASE_KID } from '../runtime/release.mjs';
import { sha256File } from '../runtime/verify.mjs';

function reviewFixture(t) {
  const base = tempBase(t);
  const pair = generateKeyPairSync('ed25519');
  const payload = buildPayload(base, {
    releasePublicKey: pair.publicKey.export({ type: 'spki', format: 'pem' }),
  });
  const stage = join(base, 'stage');
  fs.mkdirSync(stage);
  fs.cpSync(payload.dir, join(stage, 'payload'), { recursive: true });
  const archive = join(base, 'setup.exe');
  assert.equal(
    spawnSync(systemTool('tar'), ['-a', '-cf', archive, '-C', stage, '.'])
      .status,
    0,
  );
  wrapSetupFixture(archive);
  makeRelease({
    payload: payload.dir,
    archive,
    output: base,
    sequence: 3,
    event: 'push',
    ref: 'refs/tags/desktop-v1.0.0',
    sourceOnMain: true,
    workflowRun: '1234',
    minimumVersion: '0.0.0',
    notAfter: new Date(Date.now() + 86400000).toISOString(),
  });
  return {
    archive,
    manifest: join(base, 'desktop-release.unsigned.json'),
    buildRecord: join(base, 'desktop-build.json'),
  };
}

test('review cleanup failure warns with residual directory and preserves success or original verification error', (t) => {
  const options = reviewFixture(t);
  const originalRemove = fs.rmSync;
  const residues = [];
  const warnings = [];
  t.mock.method(console, 'warn', (message) => warnings.push(message));
  t.mock.method(fs, 'rmSync', (path, options) => {
    if (basename(String(path)).startsWith('desktop-review-')) {
      residues.push(path);
      throw Object.assign(new Error('secret cleanup detail'), {
        code: 'EBUSY',
      });
    }
    return originalRemove(path, options);
  });
  syncBuiltinESMExports();
  t.after(() => {
    t.mock.restoreAll();
    syncBuiltinESMExports();
    for (const path of residues)
      originalRemove(path, { recursive: true, force: true });
  });
  const first = reviewRelease(options);
  assert.match(first.summary.reviewSha256, /^[a-f0-9]{64}$/);
  assert.match(warnings[0], /警告.*EBUSY/);
  assert.ok(warnings[0].includes(residues[0]));
  assert.ok(!warnings[0].includes('secret cleanup detail'));
  const record = JSON.parse(fs.readFileSync(options.buildRecord));
  record.publicKeyFingerprint = '0'.repeat(64);
  fs.writeFileSync(options.buildRecord, JSON.stringify(record));
  assert.throws(
    () => reviewRelease(options),
    /package public key differs from build record/,
  );
  assert.equal(warnings.length, 2);
});

test('publish distinguishes actual 401/403 problem codes without echoing response secrets or assuming expiry', async (t) => {
  const base = tempBase(t);
  const { publicKey, privateKey } = generateKeyPairSync('ed25519');
  const archive = join(base, 'setup.exe');
  fs.writeFileSync(archive, 'fixture');
  const publicFile = join(base, 'public.pem');
  fs.writeFileSync(
    publicFile,
    publicKey.export({ type: 'spki', format: 'pem' }),
  );
  const body = {
    schema: 2,
    rolloutPercentage: 100,
    rollbackFrom: null,
    product: 'Auriga Desktop',
    version: '1.0.0',
    packageId: 'fixture',
    channel: 'stable',
    platform: 'win-x64',
    sizeBytes: 7,
    sha256: sha256File(archive),
    minimumVersion: '0.0.0',
    publishedAt: new Date(Date.now() - 1000).toISOString(),
    notAfter: new Date(Date.now() + 86400000).toISOString(),
    sequence: 2,
    notes: '',
    components: {
      dshVersion: '1.0.0',
      nodeVersion: '22.19.0',
      pluginVersion: '1.0.0',
    },
  };
  const manifest = join(base, 'release.json');
  fs.writeFileSync(
    manifest,
    JSON.stringify({
      ...body,
      signature: {
        alg: 'Ed25519',
        kid: RELEASE_KID,
        value: sign(null, Buffer.from(canonical(body)), privateKey).toString(
          'base64url',
        ),
      },
    }),
  );
  const buildRecord = join(base, 'desktop-build.json');
  fs.writeFileSync(
    buildRecord,
    JSON.stringify({
      publicKeyFingerprint: fingerprint(fs.readFileSync(publicFile)),
    }),
  );
  const options = {
    archive,
    publicKey: publicFile,
    manifest,
    buildRecord,
    adminOrigin: 'https://admin.example.com',
    portalOrigin: 'https://portal.example.com',
    auth: { cookie: 'session=fixture', csrf: 'fixture-csrf' },
  };
  let status = 403,
    code = 'iam.step_up_required';
  t.mock.method(
    globalThis,
    'fetch',
    async () =>
      new Response(
        JSON.stringify({
          code,
          detail: 'secret-cookie-value',
          title: 'secret-title',
        }),
        { status, headers: { 'Content-Type': 'application/problem+json' } },
      ),
  );
  for (const [nextStatus, nextCode, expected] of [
    [403, 'iam.step_up_required', /近期身份验证/],
    [403, 'common.forbidden', /发行权限/],
    [401, 'common.unauthenticated', /重新登录/],
    [403, 'unknown.secret', /未返回可识别原因/],
  ]) {
    status = nextStatus;
    code = nextCode;
    await assert.rejects(publish(options), (error) => {
      assert.match(error.message, new RegExp(`HTTP ${status}`));
      assert.match(error.message, expected);
      for (const value of [
        'secret-cookie-value',
        'secret-title',
        'unknown.secret',
        '已过期',
      ])
        assert.ok(!error.message.includes(value));
      return true;
    });
  }
});
