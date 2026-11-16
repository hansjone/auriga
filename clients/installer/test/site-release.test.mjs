import test from 'node:test';
import assert from 'node:assert/strict';
import {
  cpSync,
  mkdtempSync,
  mkdirSync,
  readFileSync,
  writeFileSync,
  rmSync,
  symlinkSync,
} from 'node:fs';
import { join } from 'node:path';
import { spawnSync } from 'node:child_process';
import { generateKeyPairSync, sign } from 'node:crypto';
import { createServer } from 'node:http';
import {
  ROOT,
  sealInputs,
  verifyInputs,
  digest,
  physical,
} from '../../../scripts/client-release/inputs.mjs';
import {
  generateDesktopKeys,
  assertPrivateKey,
} from '../../../scripts/client-release/keys.mjs';
import { publish } from '../../../scripts/client-release/publish.mjs';
import { canonical, RELEASE_KID } from '../runtime/release.mjs';
import { buildPayload, wrapSetupFixture } from './helpers.mjs';
import { makeRelease } from '../scripts/make-release.mjs';
import { reviewRelease } from '../scripts/sign-release.mjs';
import {
  signCommand,
  main,
  parseAdminAuth,
} from '../../../scripts/client-release/cli.mjs';
import { removeProjectReferences } from '../../../scripts/client-release/prepare.mjs';
import { validateBuildOptions } from '../../../scripts/client-release/build.mjs';
import { verifyRelease } from '../runtime/release.mjs';
import { nodeRuntime } from '../scripts/build.mjs';

function temporary(t) {
  const directory = mkdtempSync(
    join(ROOT, 'clients/installer/target/site-test-'),
  );
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  return directory;
}
mkdirSync(join(ROOT, 'clients/installer/target'), { recursive: true });

test('input-manifest requires independent digest, exact locks, full files and rejects tampering/missing/extras', (t) => {
  const dir = temporary(t);
  writeFileSync(join(dir, 'dependency'), 'pinned bytes');
  const manifest = sealInputs(dir, []);
  const verify = () =>
    verifyInputs(dir, manifest.manifestSha256, ROOT, { checkTools: false });
  assert.equal(verify().bytes, 12);
  assert.throws(() => verifyInputs(dir, '0'.repeat(64)), /批准摘要/);
  writeFileSync(join(dir, 'dependency'), 'tampered');
  assert.throws(verify, /dependency/);
  rmSync(join(dir, 'dependency'));
  assert.throws(verify, /缺少离线输入.*dependency/);
  writeFileSync(join(dir, 'dependency'), 'pinned bytes');
  writeFileSync(join(dir, 'extra'), 'x');
  assert.throws(verify, /未登记/);
  rmSync(join(dir, 'extra'));
  const body = JSON.parse(readFileSync(join(dir, 'windows-client.json')));
  body.locks['package.json'] = '0'.repeat(64);
  const bytes = JSON.stringify(body);
  writeFileSync(join(dir, 'windows-client.json'), bytes);
  assert.throws(
    () => verifyInputs(dir, digest(bytes), ROOT, { checkTools: false }),
    /依赖锁/,
  );
});

test('local input gate refuses UNC/device paths and junction descendants before following them', (t) => {
  const dir = temporary(t);
  const target = join(dir, 'real');
  mkdirSync(target);
  writeFileSync(join(target, 'file'), 'local');
  const link = join(dir, 'link');
  symlinkSync(target, link, process.platform === 'win32' ? 'junction' : 'dir');
  assert.throws(() => physical(join(link, 'file')), /间接输入/);
  if (process.platform === 'win32') {
    assert.throws(() => physical('\\\\example.com\\share\\input'), /UNC/);
    assert.throws(() => physical('\\\\?\\C:\\input'), /设备路径/);
  }
});

test('offline runtime rejects missing/corrupt bytes without modifying local inputs', async (t) => {
  const dir = temporary(t);
  const file = join(dir, 'node-v24.21.0-win-x64.zip');
  await assert.rejects(
    nodeRuntime('24.21.0', '0'.repeat(64), dir, true),
    /缺少离线输入/,
  );
  writeFileSync(file, 'corrupt archive');
  await assert.rejects(
    nodeRuntime('24.21.0', '0'.repeat(64), dir, true),
    /checksum mismatch/,
  );
  assert.equal(readFileSync(file, 'utf8'), 'corrupt archive');
});

test('offline-network rejects fetch, raw TCP, DNS, UDP and node descendants without a proxy', (t) => {
  const dir = temporary(t);
  const guard = join(ROOT, 'scripts/client-release/offline-guard.cjs');
  const log = join(dir, 'network.log');
  const env = {
    ...process.env,
    NODE_OPTIONS: `--require="${guard.replaceAll('\\', '/')}"`,
    AURIGA_CLIENT_NETWORK_LOG: log,
    HTTP_PROXY: '',
    HTTPS_PROXY: '',
    ALL_PROXY: '',
  };
  const local = spawnSync(
    process.execPath,
    ['-e', "require('fs').statSync(process.execPath)"],
    { env, encoding: 'utf8' },
  );
  assert.equal(local.status, 0, local.stderr);
  for (const expression of [
    "fetch('https://example.com')",
    "require('net').connect(443,'example.com')",
    "require('dns').lookup('example.com',()=>{})",
    "new (require('dns').Resolver)().resolve4('example.com',()=>{})",
    "new (require('dns').promises.Resolver)().resolve4('example.com')",
    "require('dgram').createSocket('udp4')",
    `require('child_process').execFileSync(process.execPath,['-e',"fetch('https://example.com')"],{stdio:'pipe'})`,
  ]) {
    const result = spawnSync(process.execPath, ['-e', expression], {
      env,
      encoding: 'utf8',
    });
    assert.notEqual(result.status, 0);
    assert.match(result.stderr, /AURIGA_OFFLINE_NETWORK_DENIED/);
  }
  assert.equal(readFileSync(log, 'utf8').trim().split('\n').length, 7);
});

test('portable pnpm store drops only project junctions and auth parse never echoes credentials', (t) => {
  const dir = temporary(t);
  const target = join(dir, 'target');
  mkdirSync(target);
  writeFileSync(join(target, 'kept'), 'original');
  const store = join(dir, 'store');
  mkdirSync(join(store, 'v11/projects'), { recursive: true });
  symlinkSync(
    target,
    join(store, 'v11/projects/ref'),
    process.platform === 'win32' ? 'junction' : 'dir',
  );
  removeProjectReferences(store);
  assert.equal(readFileSync(join(target, 'kept'), 'utf8'), 'original');
  const secret = 'test-credential-do-not-echo';
  assert.throws(
    () => parseAdminAuth(secret),
    (error) =>
      !error.message.includes(secret) && /有效 JSON/.test(error.message),
  );
});

test('key-acl generates only current-user protected private material and refuses overwrite', (t) => {
  const dir = join(temporary(t), 'keys');
  const result = generateDesktopKeys(dir);
  assert.match(result.publicKeyFingerprint, /^[a-f0-9]{64}$/);
  assertPrivateKey(join(dir, 'desktop-private.pem'));
  assert.throws(() => generateDesktopKeys(dir));
  if (process.platform === 'win32') {
    const script = `$f=Get-Item -LiteralPath $env:TEST_KEY; $a=$f.GetAccessControl([System.Security.AccessControl.AccessControlSections]::Access); $a.SetAccessRuleProtection($false,$true); $f.SetAccessControl($a)`;
    const env = Object.fromEntries(
      Object.entries(process.env).filter(
        ([name]) => name.toLowerCase() !== 'psmodulepath',
      ),
    );
    const changed = spawnSync(
      join(
        process.env.SystemRoot,
        'System32/WindowsPowerShell/v1.0/powershell.exe',
      ),
      ['-NoProfile', '-NonInteractive', '-Command', script],
      {
        env: { ...env, TEST_KEY: join(dir, 'desktop-private.pem') },
        encoding: 'utf8',
      },
    );
    assert.equal(changed.status, 0, changed.stderr);
    assert.throws(
      () => assertPrivateKey(join(dir, 'desktop-private.pem')),
      /权限/,
    );
  }
});

test('site-sign-review requires explicit site profile, current review digest and same protected signing key', (t) => {
  const dir = temporary(t),
    keyDir = join(dir, 'keys');
  generateDesktopKeys(keyDir);
  const publicKey = readFileSync(join(keyDir, 'desktop-public.pem'));
  const resourceTrust = Object.fromEntries(
    ['release', 'revocation', 'downgrade'].map((role) => [
      role,
      {
        kid: role,
        publicKeySpkiBase64: generateKeyPairSync('ed25519')
          .publicKey.export({ format: 'der', type: 'spki' })
          .toString('base64'),
      },
    ]),
  );
  const payload = buildPayload(dir, {
    releasePublicKey: publicKey,
    resourceTrust,
  });
  const stage = join(dir, 'stage');
  mkdirSync(stage);
  const archive = join(dir, 'AurigaDSH-Setup-1.0.0.exe');
  const tar =
    process.platform === 'win32'
      ? join(process.env.SystemRoot, 'System32/tar.exe')
      : 'tar';
  const evidence = {
    schema: 1,
    inputsSha256: 'a'.repeat(64),
    lockSha256: 'b'.repeat(64),
    sourceTreeSha256: 'c'.repeat(64),
    dirty: true,
    networkPolicy: 'node-deny-local-native-tools',
  };
  // cpSync retains all manifest-checked bytes.
  cpSync(payload.dir, join(stage, 'payload'), { recursive: true });
  const zipped = spawnSync(tar, [
    '-a',
    '-cf',
    archive + '.zip',
    '-C',
    stage,
    '.',
  ]);
  assert.equal(zipped.status, 0);
  writeFileSync(archive, readFileSync(archive + '.zip'));
  wrapSetupFixture(archive);
  makeRelease({
    payload: payload.dir,
    archive,
    exe: archive,
    output: dir,
    sequence: 7,
    minimumVersion: '0.0.0',
    notAfter: new Date(Date.now() + 86400000).toISOString(),
    siteOffline: evidence,
  });
  const options = {
    manifest: join(dir, 'desktop-release.unsigned.json'),
    buildRecord: join(dir, 'desktop-build.json'),
    archive,
  };
  assert.throws(() => reviewRelease(options), /only desktop tag/);
  assert.throws(() =>
    signCommand({
      dir,
      'key-file': join(keyDir, 'desktop-private.pem'),
      'confirm-sha256': '0'.repeat(64),
    }),
  );
  const review = signCommand({ dir, 'review-only': true });
  assert.equal(review.event, 'site_offline');
  assert.equal(review.siteOffline.dirty, true);
  assert.throws(
    () =>
      signCommand({
        dir,
        'key-file': join(keyDir, 'desktop-private.pem'),
        'confirm-sha256': '0'.repeat(64),
      }),
    /confirmation/,
  );
  const body = JSON.parse(readFileSync(options.manifest));
  writeFileSync(
    options.manifest,
    JSON.stringify({ ...body, notes: 'changed after review' }),
  );
  assert.throws(
    () =>
      signCommand({
        dir,
        'key-file': join(keyDir, 'desktop-private.pem'),
        'confirm-sha256': review.reviewSha256,
      }),
    /重新审阅/,
  );
  writeFileSync(options.manifest, JSON.stringify(body));
  const record = JSON.parse(readFileSync(options.buildRecord));
  for (const patch of [
    { resourceTrustSha256: '0'.repeat(64) },
    { sourceCommit: 'bad' },
    { sha256: '0'.repeat(64) },
    { sourceOnMain: true },
    { publicKeyFingerprint: '0'.repeat(64) },
    { siteOffline: { ...evidence, networkPolicy: 'online' } },
  ]) {
    writeFileSync(options.buildRecord, JSON.stringify({ ...record, ...patch }));
    assert.throws(() => signCommand({ dir, 'review-only': true }));
  }
  writeFileSync(options.buildRecord, JSON.stringify(record));
  const next = signCommand({ dir, 'review-only': true });
  const missingPrivate = join(dir, 'missing-private.pem');
  const failed = spawnSync(
    process.execPath,
    [
      join(ROOT, 'scripts/client-release/cli.mjs'),
      'sign',
      '--dir',
      dir,
      '--key-file',
      missingPrivate,
      '--confirm-sha256',
      next.reviewSha256,
    ],
    { encoding: 'utf8' },
  );
  assert.equal(failed.status, 1);
  assert.match(failed.stderr, /签名失败/);
  assert.equal((failed.stdout + failed.stderr).includes(missingPrivate), false);
  const wrong = join(dir, 'wrong');
  generateDesktopKeys(wrong);
  assert.throws(
    () =>
      signCommand({
        dir,
        'key-file': join(wrong, 'desktop-private.pem'),
        'confirm-sha256': next.reviewSha256,
      }),
    /differs/,
  );
  assert.equal(
    signCommand({
      dir,
      'key-file': join(keyDir, 'desktop-private.pem'),
      'confirm-sha256': next.reviewSha256,
    }).signed,
    true,
  );
  verifyRelease(JSON.parse(readFileSync(join(dir, 'desktop-release.json'))), {
    publicKey,
  });
  assert.throws(() => signCommand({ dir, 'review-only': true }), /禁止覆盖/);
});

test('client options reject online build, invalid release policies and unknown flags before construction', async () => {
  assert.throws(() => validateBuildOptions({}), /offline/);
  const base = {
    offline: true,
    devkit: 'x',
    'devkit-sha256': 'a'.repeat(64),
    out: 'x',
    'release-public-key': 'x',
    'resource-trust-json': 'x',
    version: '1.2.3',
    sequence: '2',
  };
  for (const patch of [
    { sequence: '0' },
    { sequence: '1.5' },
    { sequence: '9007199254740992' },
    { channel: 'evil' },
    { 'minimum-version': '2.0.0' },
    { 'rollout-percentage': '101' },
    { 'not-after': '2000-01-01T00:00:00Z' },
  ])
    assert.throws(() => validateBuildOptions({ ...base, ...patch }));
  await assert.rejects(main(['build', '--online']));
});

function release(privateKey, bytes, changes = {}) {
  const body = {
    schema: 2,
    product: 'Auriga Desktop',
    version: '1.2.3',
    packageId: 'auriga-test',
    channel: 'stable',
    platform: 'win-x64',
    sizeBytes: bytes.length,
    sha256: digest(bytes),
    minimumVersion: '1.0.0',
    publishedAt: new Date(Date.now() - 1000).toISOString(),
    notAfter: new Date(Date.now() + 86400000).toISOString(),
    sequence: 2,
    notes: '',
    components: {
      dshVersion: '0.2.1-rc.3',
      nodeVersion: '24.21.0',
      pluginVersion: '0.1.17',
    },
    rolloutPercentage: 100,
    rollbackFrom: null,
    ...changes,
  };
  return {
    ...body,
    signature: {
      alg: 'Ed25519',
      kid: RELEASE_KID,
      value: sign(null, Buffer.from(canonical(body)), privateKey).toString(
        'base64url',
      ),
    },
  };
}

test('publish-validation uses normal admin endpoint and verifies public download; refuses tampering, stale sequence, redirects', async (t) => {
  const dir = temporary(t);
  const { publicKey, privateKey } = generateKeyPairSync('ed25519');
  const bytes = Buffer.from('test setup bytes');
  const archive = join(dir, 'setup.exe'),
    manifest = join(dir, 'release.json'),
    key = join(dir, 'public.pem');
  writeFileSync(archive, bytes);
  writeFileSync(key, publicKey.export({ type: 'spki', format: 'pem' }));
  let envelope = release(privateKey, bytes),
    posts = 0,
    badDownload = false,
    stale = false,
    redirect = false;
  writeFileSync(manifest, JSON.stringify(envelope));
  const server = createServer(async (req, res) => {
    if (redirect) {
      res.writeHead(302, { Location: 'https://example.com' });
      res.end();
      return;
    }
    if (req.url === '/api/resource/v1/admin/desktop/releases') {
      assert.equal(req.headers.cookie, 'admin=temporary');
      if (req.method === 'POST') {
        assert.equal(req.headers['x-auriga-csrf'], 'test-csrf');
        assert.equal(
          req.headers.origin,
          `http://127.0.0.1:${server.address().port}`,
        );
        assert.deepEqual(
          JSON.parse(
            Buffer.from(req.headers['x-desktop-manifest'], 'base64url'),
          ),
          envelope,
        );
        const chunks = [];
        for await (const chunk of req) chunks.push(chunk);
        assert.deepEqual(Buffer.concat(chunks), bytes);
        posts++;
        res.writeHead(201);
        res.end('{}');
      } else
        res.end(
          JSON.stringify({ items: stale ? [{ manifest: envelope }] : [] }),
        );
    } else if (req.url.startsWith('/api/resource/v1/desktop/latest'))
      res.end(JSON.stringify(envelope));
    else if (req.url === '/api/resource/v1/desktop/packages/2')
      res.end(badDownload ? 'bad bytes' : bytes);
    else {
      res.writeHead(404);
      res.end();
    }
  });
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  t.after(() => new Promise((resolve) => server.close(resolve)));
  const origin = `http://127.0.0.1:${server.address().port}`;
  const options = {
    archive,
    manifest,
    publicKey: key,
    adminOrigin: origin,
    portalOrigin: origin,
    confirmHttpOrigin: origin,
    auth: { cookie: 'admin=temporary', csrf: 'test-csrf' },
  };
  assert.equal((await publish(options)).sha256, digest(bytes));
  assert.equal(posts, 1);
  badDownload = true;
  await assert.rejects(publish(options), /下载摘要/);
  badDownload = false;
  stale = true;
  await assert.rejects(publish(options), /序号/);
  stale = false;
  redirect = true;
  await assert.rejects(publish(options));
  redirect = false;
  envelope = { ...envelope, channel: 'beta' };
  writeFileSync(manifest, JSON.stringify(envelope));
  await assert.rejects(publish(options), /signature/);
  writeFileSync(
    manifest,
    JSON.stringify(release(privateKey, bytes, { sequence: 0 })),
  );
  await assert.rejects(publish(options), /malformed/);
  writeFileSync(manifest, JSON.stringify(release(privateKey, bytes)));
  writeFileSync(archive, 'tamper');
  await assert.rejects(publish(options), /安装包摘要/);
});
