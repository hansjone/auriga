import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import { syncBuiltinESMExports } from 'node:module';
import { join, basename } from 'node:path';
import { spawnSync } from 'node:child_process';
import { generateKeyPairSync, sign } from 'node:crypto';
import { Buffer } from 'node:buffer';
import {
  buildPayload,
  tempBase,
  wrapSetupFixture,
  writeStageZip,
} from './helpers.mjs';
import { makeRelease } from '../scripts/make-release.mjs';
import { reviewRelease } from '../scripts/sign-release.mjs';
import { publish } from '../../../scripts/client-release/publish.mjs';
import { fingerprint } from '../../../scripts/client-release/keys.mjs';
import { canonical, RELEASE_KID } from '../runtime/release.mjs';
import { sha256File } from '../runtime/verify.mjs';
import { build } from '../scripts/build.mjs';
import {
  sealInputs,
  systemTools,
} from '../../../scripts/client-release/inputs.mjs';
import {
  registerBaseline,
  TOOLS_BASELINE_MISMATCH,
} from '../../../scripts/client-release/baseline.mjs';

import { formatCliError } from '../../../scripts/client-release/cli.mjs';
import { launchOfflineBuild } from '../../../scripts/client-release/build.mjs';

test('build diagnostics show a real output collision and keep sensitive or unrelated paths hidden', (t) => {
  const dir = tempBase(t);
  const out = join(dir, 'already exists');
  fs.mkdirSync(out);
  assert.throws(
    () => launchOfflineBuild({ out }, { directory: join(dir, 'kit') }),
    (error) => {
      assert.equal(error.code, 'EEXIST');
      const message = formatCliError('build', error, [], {}, { out });
      assert.match(message, /EEXIST.*mkdir/);
      assert.ok(message.includes(out));
      assert.ok(!message.includes('路径已隐藏'));
      assert.ok(!formatCliError('sign', error, [], {}).includes(out));
      return true;
    },
  );

  const config = join(dir, 'site config');
  const configFile = join(config, 'adapter.json');
  const keyFile = join(dir, 'signing.pem');
  const outside = join(dir, '..', 'elsewhere', 'example.txt');
  const tokenPath = join(dir, 'secret-token-value');
  for (const path of [configFile, keyFile, outside, tokenPath]) {
    const error = Object.assign(new Error(`ENOENT: open '${path}'`), { path });
    const message = formatCliError(
      'build',
      error,
      [],
      { FIXTURE_TOKEN: 'secret-token-value' },
      { temp: dir, 'uac-config-dir': config },
    );
    assert.ok(!message.includes(path));
    assert.ok(!message.includes('构建路径'));
  }
});

test(
  'offline installer uses the registered Windows tools baseline and still checks dependency bytes',
  { skip: process.platform !== 'win32' },
  async (t) => {
    const base = tempBase(t);
    const kit = join(base, 'kit');
    const cache = join(base, 'cache');
    fs.mkdirSync(kit);
    const dependency = join(kit, 'fixture.bin');
    fs.writeFileSync(dependency, 'approved bytes');
    const tools = systemTools();
    const manifest = sealInputs(
      kit,
      tools.map((tool) => ({ ...tool, sha256: '0'.repeat(64) })),
    );
    const previousBaseline = process.env.AURIGA_CLIENT_TOOLS_BASELINE;
    t.after(() => {
      if (previousBaseline === undefined)
        delete process.env.AURIGA_CLIENT_TOOLS_BASELINE;
      else process.env.AURIGA_CLIENT_TOOLS_BASELINE = previousBaseline;
    });
    const args = {
      offline: true,
      'offline-inputs': kit,
      'offline-inputs-sha256': manifest.manifestSha256,
      'broker-artifact': join(base, 'broker'),
    };
    delete process.env.AURIGA_CLIENT_TOOLS_BASELINE;
    await assert.rejects(() => build(args), {
      message: TOOLS_BASELINE_MISMATCH,
    });

    registerBaseline(cache, tools);
    process.env.AURIGA_CLIENT_TOOLS_BASELINE = cache;
    // Reaching plugin validation proves the installer passed the devkit checks.
    await assert.rejects(() => build(args), /离线构建必须提供本地重建的插件包/);
    fs.writeFileSync(dependency, 'changed bytes');
    await assert.rejects(() => build(args), /离线输入摘要不符：fixture\.bin/);
  },
);

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
  writeStageZip(stage, archive);
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
      new globalThis.Response(
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
