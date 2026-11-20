import test from 'node:test';
import assert from 'node:assert/strict';
import {
  cpSync,
  existsSync,
  mkdtempSync,
  mkdirSync,
  readFileSync,
  readdirSync,
  writeFileSync,
  rmSync,
  symlinkSync,
} from 'node:fs';
import { dirname, join } from 'node:path';
import { spawnSync } from 'node:child_process';
import { generateKeyPairSync, sign } from 'node:crypto';
import { createServer } from 'node:http';
import {
  ROOT,
  BUILD_NODE,
  lockIdentity,
  sealInputs,
  verifyInputs,
  digest,
  physical,
} from '../../../scripts/client-release/inputs.mjs';
import {
  generateDesktopKeys,
  assertPrivateKey,
  fingerprint,
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
import {
  validateBuildOptions,
  launchOfflineBuild,
  isInside,
} from '../../../scripts/client-release/build.mjs';
import {
  classifyLockChanges,
  collectDependencyChanges,
  deriveSiteDevkit,
  diffPackages,
  lockMatches,
  onlineEnv,
  parseNpmPackages,
  parsePnpmPackages,
  readBaseLock,
  startBuild,
  startOnlineBuild,
  unsupportedMessage,
} from '../../../scripts/client-release/online.mjs';
import {
  FIELD_REGISTRY,
  checkRequires,
  stampRequires,
} from '../../../scripts/client-release/compat.mjs';
import { signInputs } from '../../../scripts/client-release/sign-inputs.mjs';
import { verifyRelease } from '../runtime/release.mjs';
import { nodeRuntime } from '../scripts/build.mjs';
import {
  assertBuildSourceClean,
  cleanSource,
  copyBuildSource,
} from '../../../scripts/client-release/source.mjs';
import { main as clientMain } from '../../../scripts/client-release/cli.mjs';
import { buildWorker } from '../../../scripts/client-release/build.mjs';

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

test('changing only the target build Node version invalidates a Windows kit', (t) => {
  const source = temporary(t);
  const kit = temporary(t);
  const pinPath = 'scripts/client-release/build-node.json';
  const original = lockIdentity();
  for (const path of Object.keys(original.locks)) {
    mkdirSync(dirname(join(source, path)), { recursive: true });
    cpSync(join(ROOT, path), join(source, path));
  }
  const pin = join(source, pinPath);
  mkdirSync(dirname(pin), { recursive: true });
  writeFileSync(pin, JSON.stringify({ version: original.buildNode }));
  writeFileSync(join(kit, 'input'), 'pinned');
  const sealed = sealInputs(kit, [], source);
  const verify = () =>
    verifyInputs(kit, sealed.manifestSha256, source, { checkTools: false });
  assert.equal(verify().buildNode, original.buildNode);
  assert.equal(sealed.lockSha256, original.lockSha256);
  const upgraded = original.buildNode.replace(/\d+$/, (patch) =>
    String(Number(patch) + 1),
  );
  writeFileSync(pin, JSON.stringify({ version: upgraded }));
  assert.deepEqual(lockIdentity(source).locks, original.locks);
  assert.throws(verify, /依赖锁不匹配/);
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
    sourceInfo: {
      sourceType: 'git',
      sourceCommit: 'a'.repeat(40),
      sourceTreeSha256: evidence.sourceTreeSha256,
      dirty: true,
      siteChanges: true,
    },
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
  assert.match(review.sourceWarning, /警告.*Git.*未.*签名/);
  assert.equal(review.siteDependencies, undefined);
  // 派生套件构建的输出目录：审阅输出提示含现场联网依赖，且不影响审阅摘要。
  writeFileSync(
    join(dir, 'site-dependencies.json'),
    JSON.stringify({ added: [{ name: 'x' }], removedCount: 0, warnings: [] }),
  );
  const withSite = signCommand({ dir, 'review-only': true });
  assert.equal(withSite.siteDependencies.count, 1);
  assert.match(withSite.siteDependencies.notice, /含现场联网依赖：1 项/);
  assert.equal(withSite.reviewSha256, review.reviewSha256);
  rmSync(join(dir, 'site-dependencies.json'));
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
  writeFileSync(
    options.buildRecord,
    JSON.stringify({
      ...record,
      source: {
        ...record.source,
        sourceType: 'signed-archive',
        sourceBaselineSha256: 'd'.repeat(64),
        sourceManifestSha256: 'e'.repeat(64),
        sourceIssuerFingerprint: 'f'.repeat(64),
      },
    }),
  );
  assert.equal(
    signCommand({ dir, 'review-only': true }).sourceWarning,
    undefined,
  );
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

test('client options reject invalid release policies and unknown flags before construction', async () => {
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
    buildRecord = join(dir, 'desktop-build.json'),
    key = join(dir, 'public.pem');
  writeFileSync(archive, bytes);
  writeFileSync(key, publicKey.export({ type: 'spki', format: 'pem' }));
  const keyFingerprint = fingerprint(readFileSync(key));
  writeFileSync(
    buildRecord,
    JSON.stringify({ publicKeyFingerprint: keyFingerprint }),
  );
  let envelope = release(privateKey, bytes),
    posts = 0,
    requests = 0,
    badDownload = false,
    stale = false,
    redirect = false;
  writeFileSync(manifest, JSON.stringify(envelope));
  const server = createServer(async (req, res) => {
    requests++;
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
    buildRecord,
    publicKey: key,
    adminOrigin: origin,
    portalOrigin: origin,
    confirmHttpOrigin: origin,
    auth: { cookie: 'admin=temporary', csrf: 'test-csrf' },
  };
  assert.equal((await publish(options)).sha256, digest(bytes));
  assert.equal(posts, 1);
  const beforeRequests = requests;
  writeFileSync(
    buildRecord,
    JSON.stringify({ publicKeyFingerprint: '0'.repeat(64) }),
  );
  await assert.rejects(publish(options), /公钥指纹.*构建记录/);
  assert.equal(requests, beforeRequests);
  assert.equal(posts, 1);
  writeFileSync(buildRecord, '{}');
  await assert.rejects(publish(options), /公钥指纹.*构建记录/);
  assert.equal(requests, beforeRequests);
  rmSync(buildRecord);
  await assert.rejects(publish(options), /ENOENT/);
  assert.equal(requests, beforeRequests);
  writeFileSync(
    buildRecord,
    JSON.stringify({ publicKeyFingerprint: keyFingerprint }),
  );
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

// ---------------------------------------------------------------------------
// build --online（联网补依赖）与清单格式兼容（requires）
// ---------------------------------------------------------------------------

const PNPM_LOCK = 'web/pnpm-lock.yaml';
const PLUGIN_LOCK = 'clients/dsh-plugin/package-lock.json';
const treeSnapshot = (dir, prefix = '') =>
  readdirSync(join(dir, prefix), { withFileTypes: true })
    .flatMap((entry) => {
      const path = prefix ? `${prefix}/${entry.name}` : entry.name;
      return entry.isDirectory()
        ? [`${path}/`, ...treeSnapshot(dir, path)]
        : [`${path}:${digest(readFileSync(join(dir, path)))}`];
    })
    .sort();

/** 把当前源码的依赖锁文件复制到临时根目录，可选修改。 */
function makeRoot(t, mutate) {
  const root = temporary(t);
  for (const path of Object.keys(lockIdentity().locks)) {
    mkdirSync(dirname(join(root, path)), { recursive: true });
    cpSync(join(ROOT, path), join(root, path));
  }
  const pin = join(root, 'scripts/client-release/build-node.json');
  mkdirSync(dirname(pin), { recursive: true });
  cpSync(join(ROOT, 'scripts/client-release/build-node.json'), pin);
  mutate?.(root);
  return root;
}
const withPnpmPackage = (root) => {
  const file = join(root, PNPM_LOCK);
  const text = readFileSync(file, 'utf8');
  writeFileSync(
    file,
    text.replace(
      '\npackages:\n',
      "\npackages:\n\n  'fake-new@1.2.3':\n    resolution: {integrity: sha512-FAKENEW}\n",
    ),
  );
};
const withPluginPackage = (root) => {
  const file = join(root, PLUGIN_LOCK);
  const lock = JSON.parse(readFileSync(file, 'utf8'));
  lock.packages['node_modules/fake-plugin'] = {
    version: '2.0.0',
    integrity: 'sha512-FAKEPLUGIN',
  };
  writeFileSync(file, JSON.stringify(lock, null, 2));
};

/** 造一个与真实布局一致的基础套件（内容为占位），按 baseRoot 的锁封存并签名。 */
function makeBaseKit(t, baseRoot, { signed = true, meta = {} } = {}) {
  const parent = temporary(t);
  const dir = join(parent, 'base');
  const write = (path, text) => {
    mkdirSync(dirname(join(dir, path)), { recursive: true });
    writeFileSync(join(dir, path), text);
  };
  const manager = JSON.parse(
    readFileSync(join(baseRoot, 'package.json'), 'utf8'),
  ).packageManager;
  write('build-node/node.exe', 'node');
  write('build-node/node_modules/npm/bin/npm-cli.js', 'npm');
  write(
    'build-pnpm/package.json',
    JSON.stringify({ dependencies: { pnpm: manager.slice('pnpm@'.length) } }),
  );
  write('build-pnpm/node_modules/pnpm/bin/pnpm.cjs', 'pnpm');
  write('pnpm-store/v11/index.db', 'old-store');
  mkdirSync(join(dir, 'pnpm-store/v11/projects'), { recursive: true });
  write(
    'plugin-deps/package.json',
    readFileSync(join(baseRoot, 'clients/dsh-plugin/package.json'), 'utf8'),
  );
  write(
    'plugin-deps/package-lock.json',
    readFileSync(join(baseRoot, PLUGIN_LOCK), 'utf8'),
  );
  write('plugin-deps/node_modules/old/index.js', 'old');
  const sealed = sealInputs(
    dir,
    [{ path: 'tool', sha256: 'a'.repeat(64) }],
    baseRoot,
  );
  const keyFile = join(parent, 'release.pem');
  if (!signed) return { dir, sealed, keyFile };
  const { publicKey, privateKey } = generateKeyPairSync('ed25519');
  const release = {
    schemaVersion: 1,
    version: '0.0.1',
    platform: 'win-x64',
    lockSha256: sealed.lockSha256,
    manifestSha256: sealed.manifestSha256,
    buildNodeSha256: digest(readFileSync(join(dir, 'build-node/node.exe'))),
    ...meta,
  };
  const bytes = JSON.stringify(release, null, 2) + '\n';
  const sums = `${digest(bytes)}  client-inputs-release.json\n${sealed.manifestSha256}  windows-client.json\n`;
  const pem = publicKey.export({ type: 'spki', format: 'pem' });
  writeFileSync(join(dir, 'client-inputs-release.json'), bytes);
  writeFileSync(join(dir, 'SHA256SUMS'), sums);
  writeFileSync(
    join(dir, 'SHA256SUMS.sig'),
    sign(null, Buffer.from(sums), privateKey).toString('base64') + '\n',
  );
  writeFileSync(join(dir, 'release-public.pem'), pem);
  writeFileSync(keyFile, pem);
  return { dir, sealed, keyFile };
}
const verifySkip = (dir, root, keyFile) =>
  verifyInputs(dir, undefined, root, {
    checkTools: false,
    verifyKey: keyFile,
    lockPolicy: 'skip',
  });

function onlineOptions(t, extra = {}) {
  const dir = temporary(t);
  const keys = join(dir, 'keys');
  generateDesktopKeys(keys);
  const trust = Object.fromEntries(
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
  const roots = join(dir, 'resource-roots.json');
  writeFileSync(roots, JSON.stringify(trust));
  return {
    online: true,
    devkit: join(dir, 'base'),
    'verify-key': join(dir, 'release.pem'),
    'site-devkit': join(dir, 'site'),
    out: join(dir, 'out'),
    'release-public-key': join(keys, 'desktop-public.pem'),
    'resource-trust-json': roots,
    version: '1.2.3',
    sequence: '2',
    ...extra,
  };
}

test('online options: exclusive with offline, need release trust and a fresh disjoint --site-devkit, checked before any work', async (t) => {
  const base = onlineOptions(t);
  assert.doesNotThrow(() => validateBuildOptions(base));
  for (const [patch, pattern] of [
    [{ offline: true }, /互斥/],
    [{ 'verify-key': undefined }, /发行签名/],
    [{ 'site-devkit': undefined }, /--site-devkit/],
    [{ 'devkit-sha256': 'a'.repeat(64) }, /devkit-sha256/],
    [{ 'site-devkit': join(base.devkit, 'inside') }, /全新目录/],
    [{ 'site-devkit': join(base.out, 'inside') }, /全新目录/],
    [{ out: join(base['site-devkit'], 'inside') }, /全新目录/],
    [{ out: base['site-devkit'] }, /全新目录/],
    [{ out: join(base.devkit, 'inside') }, /只读 devkit/],
  ])
    assert.throws(() => validateBuildOptions({ ...base, ...patch }), pattern);
  assert.doesNotThrow(() =>
    validateBuildOptions({
      ...base,
      'verify-key': undefined,
      'trust-fingerprint': 'a'.repeat(64),
    }),
  );
  // 离线模式不能带 --site-devkit；两种模式都没指定也拒绝。
  const offline = { ...base, online: undefined, offline: true };
  assert.throws(() => validateBuildOptions(offline), /只能与 --online/);
  assert.doesNotThrow(() =>
    validateBuildOptions({ ...offline, 'site-devkit': undefined }),
  );
  assert.throws(
    () =>
      validateBuildOptions({
        ...offline,
        offline: undefined,
        'site-devkit': undefined,
      }),
    /--offline.*--online/,
  );
  // 编排入口：参数错误时不碰任何联网/拷贝/校验钩子。
  const forbidden = () => {
    throw new Error('不应被调用');
  };
  const io = {
    sourceIdentity: forbidden,
    verifyBase: forbidden,
    runner: forbidden,
    launch: forbidden,
  };
  await assert.rejects(
    async () => startBuild({ ...base, offline: true }, io),
    /互斥/,
  );
  await assert.rejects(
    async () => startBuild({ ...base, 'verify-key': undefined }, io),
    /发行签名/,
  );
  assert.equal(existsSync(base['site-devkit']), false);
  await assert.rejects(main(['build', '--online', '--offline']), /互斥/);
});

test('online scope: only pnpm and dsh-plugin dependency files may change; everything else is refused with the file list', () => {
  const base = {
    buildNode: '22.19.0',
    locks: {
      'package.json': 'a',
      [PNPM_LOCK]: 'b',
      [PLUGIN_LOCK]: 'c',
      '.npmrc': 'n',
      'web/ui/package.json': 'u',
    },
  };
  const same = { ...base, locks: { ...base.locks } };
  const manager = {
    basePackageManager: 'pnpm@11.28.6',
    currentPackageManager: 'pnpm@11.28.6',
  };
  let plan = classifyLockChanges(base, same, manager);
  assert.deepEqual(
    [plan.changed, plan.unsupported, plan.pnpm, plan.plugin],
    [[], [], false, false],
  );
  plan = classifyLockChanges(
    base,
    {
      ...base,
      locks: {
        ...base.locks,
        [PNPM_LOCK]: 'x',
        'web/ui/package.json': 'y',
        'web/new/package.json': 'z',
        'clients/x/package.json': 'q',
        'package.json': 'r',
        'pnpm-workspace.yaml': 'w',
      },
    },
    manager,
  );
  assert.deepEqual(plan.unsupported, []);
  assert.equal(plan.pnpm, true);
  assert.equal(plan.plugin, false);
  plan = classifyLockChanges(
    base,
    { ...base, locks: { ...base.locks, [PLUGIN_LOCK]: 'x' } },
    manager,
  );
  assert.deepEqual(
    [plan.pnpm, plan.plugin, plan.unsupported],
    [false, true, []],
  );
  // 不支持：.npmrc、Python/DSH/upstream 锁、构建 Node、packageManager。
  const unsupported = {
    buildNode: '22.20.0',
    locks: {
      ...base.locks,
      '.npmrc': 'changed',
      'clients/installer/upstream.json': 'u',
      'clients/installer/locks/python-win-x64.json': 'p',
      'clients/installer/locks/dsh-1.0.0.package-lock.json': 'd',
      'package.json': 'changed',
    },
  };
  plan = classifyLockChanges(base, unsupported, {
    basePackageManager: 'pnpm@11.28.6',
    currentPackageManager: 'pnpm@12.0.0',
  });
  assert.deepEqual(plan.unsupported.sort(), [
    '.npmrc',
    'clients/installer/locks/dsh-1.0.0.package-lock.json',
    'clients/installer/locks/python-win-x64.json',
    'clients/installer/upstream.json',
    'package.json（packageManager 变化）',
    'scripts/client-release/build-node.json',
  ]);
  const message = unsupportedMessage(plan.unsupported);
  assert.match(message, /一期不支持，请联系维护方出新套件/);
  assert.match(message, /\.npmrc/);
});

test('dependency comparison lists added packages with integrity and never shows an unknown count as zero', () => {
  const oldLock = [
    "lockfileVersion: '9.0'",
    '',
    'importers:',
    '  .: {}',
    '',
    'packages:',
    '',
    "  '@scope/kept@1.0.0':",
    '    resolution: {integrity: sha512-KEPT}',
    '  gone@1.0.0:',
    '    resolution: {integrity: sha512-GONE}',
    '',
    'snapshots:',
    "  'zzz@9.9.9': {}",
    '',
  ].join('\n');
  const newLock = oldLock
    .replace('  gone@1.0.0:\n    resolution: {integrity: sha512-GONE}\n', '')
    .replace(
      '    resolution: {integrity: sha512-KEPT}\n',
      "    resolution: {integrity: sha512-KEPT}\n  'fresh@2.0.0':\n    resolution: {integrity: sha512-FRESH, tarball: https://example.test/x.tgz}\n  '@scope/kept@1.1.0':\n    resolution: {integrity: sha512-NEWER}\n",
    );
  const before = parsePnpmPackages(oldLock);
  assert.deepEqual([...before.keys()], ['@scope/kept@1.0.0', 'gone@1.0.0']);
  const diff = diffPackages(
    'pnpm',
    before,
    parsePnpmPackages(newLock.replaceAll('\n', '\r\n')),
  );
  assert.deepEqual(diff.added, [
    {
      ecosystem: 'pnpm',
      name: '@scope/kept',
      version: '1.1.0',
      integrity: 'sha512-NEWER',
    },
    {
      ecosystem: 'pnpm',
      name: 'fresh',
      version: '2.0.0',
      integrity: 'sha512-FRESH',
    },
  ]);
  assert.equal(diff.removed, 1);
  assert.throws(
    () => parsePnpmPackages("lockfileVersion: '6.0'\n"),
    /lockfileVersion/,
  );
  const npmBefore = parseNpmPackages(
    JSON.stringify({
      packages: {
        '': {},
        'node_modules/a': { version: '1.0.0', integrity: 'i1' },
      },
    }),
  );
  const npmAfter = parseNpmPackages(
    JSON.stringify({
      packages: {
        '': {},
        'node_modules/a': { version: '1.0.0', integrity: 'i1' },
        'node_modules/a/node_modules/@s/b': {
          version: '2.0.0',
          integrity: 'i2',
        },
        'node_modules/ln': { link: true, version: '1.0.0' },
      },
    }),
  );
  assert.deepEqual(diffPackages('plugin', npmBefore, npmAfter).added, [
    { ecosystem: 'plugin', name: '@s/b', version: '2.0.0', integrity: 'i2' },
  ]);
  // 旧锁来源：摘要必须与基础套件记录一致，兼容换行差异；取不到时返回 null。
  const bytes = Buffer.from(oldLock);
  assert.equal(
    lockMatches(Buffer.from(oldLock.replaceAll('\n', '\r\n')), digest(bytes)),
    true,
  );
  assert.equal(lockMatches(Buffer.from('other'), digest(bytes)), false);
  assert.equal(
    readBaseLock(digest(bytes), [
      () => Buffer.from('wrong'),
      () => {
        throw new Error('git 不可用');
      },
      () => bytes,
    ]),
    bytes,
  );
  assert.equal(readBaseLock(digest(bytes), [() => null]), null);
});

test('collectDependencyChanges degrades with a warning when the old pnpm lock is unobtainable', (t) => {
  const baseRoot = makeRoot(t);
  const root = makeRoot(t, (r) => {
    withPnpmPackage(r);
    withPluginPackage(r);
  });
  const { dir } = makeBaseKit(t, baseRoot, { signed: false });
  const baseLocks = lockIdentity(baseRoot).locks;
  const common = {
    baseDir: dir,
    baseLocks,
    root,
    changed: [PNPM_LOCK, PLUGIN_LOCK],
  };
  const ok = collectDependencyChanges({
    ...common,
    pnpmBaseSources: [() => readFileSync(join(baseRoot, PNPM_LOCK))],
  });
  assert.deepEqual(
    ok.added.map((p) => `${p.ecosystem}:${p.name}@${p.version}`),
    ['pnpm:fake-new@1.2.3', 'plugin:fake-plugin@2.0.0'],
  );
  assert.equal(ok.removedCount, 0);
  assert.deepEqual(ok.warnings, []);
  const degraded = collectDependencyChanges({
    ...common,
    pnpmBaseSources: [() => null],
  });
  assert.equal(degraded.removedCount, null);
  assert.match(degraded.warnings.join('\n'), /取不到.*web\/pnpm-lock\.yaml/);
  assert.deepEqual(
    degraded.added.map((p) => p.ecosystem),
    ['plugin'],
  );
});

test('online env drops npm/pnpm config but passes proxy and registry only when set', () => {
  const env = onlineEnv('C:\\work', {
    PATH: 'p',
    HTTPS_PROXY: 'http://proxy:8080',
    npm_config_registry: 'https://registry.example.test/',
    npm_config_prefix: 'x',
    NPM_CONFIG_USERCONFIG: 'y',
    pnpm_config_store_dir: 'z',
  });
  assert.equal(env.HTTPS_PROXY, 'http://proxy:8080');
  assert.equal(env.npm_config_registry, 'https://registry.example.test/');
  assert.equal(env.npm_config_prefix, undefined);
  assert.equal(env.NPM_CONFIG_USERCONFIG, undefined);
  assert.equal(env.pnpm_config_store_dir, undefined);
  assert.equal(env.NODE_OPTIONS, '');
  assert.equal(env.npm_config_update_notifier, 'false');
  const bare = onlineEnv('C:\\work', { PATH: 'p' });
  assert.equal(bare.HTTPS_PROXY, undefined);
  assert.equal(bare.npm_config_registry, undefined);
});

test('base kit verification keeps signature checks and only skips the lock-equals-source rule', (t) => {
  const baseRoot = makeRoot(t);
  const newRoot = makeRoot(t, withPnpmPackage);
  const { dir, keyFile } = makeBaseKit(t, baseRoot);
  assert.throws(
    () =>
      verifyInputs(dir, undefined, newRoot, {
        checkTools: false,
        verifyKey: keyFile,
      }),
    /依赖锁不匹配/,
  );
  const kit = verifySkip(dir, newRoot, keyFile);
  assert.equal(kit.locks[PNPM_LOCK], lockIdentity(baseRoot).locks[PNPM_LOCK]);
  assert.throws(
    () =>
      verifyInputs(dir, undefined, newRoot, {
        checkTools: false,
        lockPolicy: 'skip',
      }),
    /发行签名/,
  );
  const other = join(dirname(dir), 'other.pem');
  writeFileSync(
    other,
    generateKeyPairSync('ed25519').publicKey.export({
      type: 'spki',
      format: 'pem',
    }),
  );
  assert.throws(() => verifySkip(dir, newRoot, other), /签名/);
  writeFileSync(join(dir, 'pnpm-store/v11/index.db'), 'tampered');
  assert.throws(() => verifySkip(dir, newRoot, keyFile), /摘要不符/);
});

test('derive site devkit: copies without signature files, redoes only changed parts, seals a verifiable manifest, leaves the base untouched', (t) => {
  const baseRoot = makeRoot(t);
  const root = makeRoot(t, (r) => {
    withPnpmPackage(r);
    withPluginPackage(r);
  });
  const { dir, keyFile } = makeBaseKit(t, baseRoot);
  const before = treeSnapshot(dir);
  const base = verifySkip(dir, root, keyFile);
  const plan = classifyLockChanges(base, lockIdentity(root), {
    basePackageManager: 'pnpm@x',
    currentPackageManager: 'pnpm@x',
  });
  assert.deepEqual([plan.pnpm, plan.plugin], [true, true]);
  const dependencies = collectDependencyChanges({
    baseDir: dir,
    baseLocks: base.locks,
    root,
    changed: plan.changed,
    pnpmBaseSources: [() => readFileSync(join(baseRoot, PNPM_LOCK))],
  });
  const calls = [];
  const runner = (command, args, cwd, env) => {
    calls.push({ command, args, cwd, env });
    if (args.includes('fetch'))
      writeFileSync(
        join(args[args.indexOf('--store-dir') + 1], 'v11/new-package'),
        'fetched',
      );
    if (args.includes('ci')) {
      mkdirSync(join(cwd, 'node_modules/fake-plugin'), { recursive: true });
      writeFileSync(join(cwd, 'node_modules/fake-plugin/index.js'), 'new');
    }
  };
  const site = join(dirname(dir), 'site');
  const { manifest, report } = deriveSiteDevkit({
    baseDir: dir,
    site,
    root,
    base,
    plan,
    dependencies,
    runner,
    env: {
      PATH: 'p',
      HTTPS_PROXY: 'http://proxy:1',
      npm_config_userconfig: 'x',
    },
  });
  // 基础套件逐字节未变；签名材料与旧清单没有被复制，只有重新封存的清单。
  assert.deepEqual(treeSnapshot(dir), before);
  for (const name of [
    'SHA256SUMS',
    'SHA256SUMS.sig',
    'client-inputs-release.json',
    'release-public.pem',
  ])
    assert.equal(existsSync(join(site, name)), false, name);
  assert.equal(
    JSON.parse(readFileSync(join(site, 'windows-client.json'))).lockSha256,
    lockIdentity(root).lockSha256,
  );
  assert.equal(existsSync(join(site, '_online-work')), false);
  // 派生套件以刚算出的摘要通过完整校验（不用发行签名），旧 store 保留、新包与新插件依赖到位。
  const kit = verifyInputs(site, manifest.manifestSha256, root, {
    checkTools: false,
  });
  assert.equal(kit.lockSha256, lockIdentity(root).lockSha256);
  assert.equal(
    readFileSync(join(site, 'pnpm-store/v11/index.db'), 'utf8'),
    'old-store',
  );
  assert.equal(
    readFileSync(join(site, 'pnpm-store/v11/new-package'), 'utf8'),
    'fetched',
  );
  assert.equal(existsSync(join(site, 'plugin-deps/node_modules/old')), false);
  assert.equal(
    readFileSync(
      join(site, 'plugin-deps/node_modules/fake-plugin/index.js'),
      'utf8',
    ),
    'new',
  );
  assert.equal(
    readFileSync(join(site, 'plugin-deps/package-lock.json'), 'utf8'),
    readFileSync(join(root, PLUGIN_LOCK), 'utf8'),
  );
  // 执行器：用套件自带 node/pnpm/npm；代理透传、npm 配置变量被去掉。
  const fetchCall = calls.find((c) => c.args.includes('fetch'));
  assert.equal(fetchCall.command, join(site, 'build-node/node.exe'));
  assert.equal(
    fetchCall.args[0],
    join(site, 'build-pnpm/node_modules/pnpm/bin/pnpm.cjs'),
  );
  assert.deepEqual(fetchCall.args.slice(1, 5), [
    'fetch',
    '--frozen-lockfile',
    '--ignore-scripts',
    '--store-dir',
  ]);
  assert.equal(fetchCall.args[5], join(site, 'pnpm-store'));
  const ciCall = calls.find((c) => c.args.includes('ci'));
  assert.deepEqual(ciCall.args.slice(1, 4), [
    'ci',
    '--omit=dev',
    '--omit=peer',
  ]);
  assert.equal(
    ciCall.args[0],
    join(site, 'build-node/node_modules/npm/bin/npm-cli.js'),
  );
  for (const call of calls) {
    assert.equal(call.env.HTTPS_PROXY, 'http://proxy:1');
    assert.equal(call.env.npm_config_userconfig, undefined);
  }
  // site-dependencies.json
  const written = JSON.parse(
    readFileSync(join(site, 'site-dependencies.json')),
  );
  assert.deepEqual(written, report);
  assert.equal(written.baseManifestSha256, base.manifestSha256);
  assert.equal(written.baseLockSha256, base.lockSha256);
  assert.equal(written.manifestSha256, manifest.manifestSha256);
  assert.equal(written.lockSha256, lockIdentity(root).lockSha256);
  assert.deepEqual(written.changedLockFiles, [PLUGIN_LOCK, PNPM_LOCK]);
  assert.deepEqual(
    written.added.map((p) => p.name),
    ['fake-new', 'fake-plugin'],
  );
  assert.equal(written.removedCount, 0);
  // 目标已存在时拒绝，且不删它。
  assert.throws(
    () =>
      deriveSiteDevkit({
        baseDir: dir,
        site,
        root,
        base,
        plan,
        dependencies,
        runner,
      }),
    /全新目录/,
  );
  assert.equal(existsSync(join(site, 'windows-client.json')), true);
});

test('derive site devkit removes its half-built directory on failure and never writes the base', (t) => {
  const baseRoot = makeRoot(t);
  const root = makeRoot(t, withPnpmPackage);
  const { dir, keyFile } = makeBaseKit(t, baseRoot);
  const before = treeSnapshot(dir);
  const base = verifySkip(dir, root, keyFile);
  const plan = { changed: [PNPM_LOCK], pnpm: true, plugin: false };
  const site = join(dirname(dir), 'site');
  const failing = () => {
    throw new Error('网络中断');
  };
  assert.throws(
    () =>
      deriveSiteDevkit({
        baseDir: dir,
        site,
        root,
        base,
        plan,
        dependencies: { added: [], removedCount: 0, warnings: [] },
        runner: failing,
      }),
    /网络中断/,
  );
  assert.equal(existsSync(site), false);
  assert.deepEqual(treeSnapshot(dir), before);
});

test('online build orchestration: unsupported changes fail before any copy or network; unchanged locks point to --offline; success builds offline from the derived kit', async (t) => {
  const baseRoot = makeRoot(t);
  const { dir, keyFile } = makeBaseKit(t, baseRoot);
  const verify = (directory, expected, root, options) =>
    verifyInputs(directory, expected, root, { ...options, checkTools: false });
  const calls = [];
  const runner = (command, args) => {
    calls.push(args);
    if (args.includes('fetch'))
      writeFileSync(
        join(args[args.indexOf('--store-dir') + 1], 'v11/new-package'),
        'fetched',
      );
  };
  const options = (extra = {}) => ({
    ...onlineOptions(t, extra),
  });
  const make = (root, extra = {}) => {
    const o = options(extra);
    o.devkit = dir;
    o['verify-key'] = keyFile;
    return {
      o,
      io: {
        root,
        sourceIdentity: () => ({ sourceCommit: 'a'.repeat(40) }),
        verifyBase: verify,
        verifyDerived: verify,
        runner,
        gitShow: () => readFileSync(join(baseRoot, PNPM_LOCK)),
        log: () => {},
        env: { PATH: 'p' },
        launch: () => {
          throw new Error('launch 未注入');
        },
      },
    };
  };
  // 不支持的变化：列出文件，目录与执行器都没动。
  const bad = makeRoot(t, (r) =>
    writeFileSync(
      join(r, '.npmrc'),
      readFileSync(join(r, '.npmrc'), 'utf8') + '\n# changed\n',
    ),
  );
  let ctx = make(bad);
  await assert.rejects(
    async () => startOnlineBuild(ctx.o, ctx.io),
    (error) =>
      /一期不支持/.test(error.message) && /\.npmrc/.test(error.message),
  );
  assert.equal(existsSync(ctx.o['site-devkit']), false);
  assert.equal(calls.length, 0);
  // 锁一致：提示直接用 --offline。
  ctx = make(baseRoot);
  await assert.rejects(
    async () => startOnlineBuild(ctx.o, ctx.io),
    /--offline/,
  );
  assert.equal(existsSync(ctx.o['site-devkit']), false);
  // 允许的变化：派生并以派生清单摘要启动离线构建。
  const root = makeRoot(t, withPnpmPackage);
  ctx = make(root);
  let launched;
  ctx.io.launch = (o, kit, hooks) => {
    launched = { o, kit, hooks };
  };
  const result = await startOnlineBuild(ctx.o, ctx.io);
  assert.equal(result.added, 1);
  assert.equal(launched.kit.directory, ctx.o['site-devkit']);
  assert.equal(launched.kit.manifestSha256, result.manifestSha256);
  assert.equal(
    launched.hooks.siteDependencies,
    join(ctx.o['site-devkit'], 'site-dependencies.json'),
  );
  assert.equal(launched.o.online, true);
  assert.equal(calls.length, 1);
  assert.equal(calls[0].includes('fetch'), true);
});

test('offline launch from a derived kit keeps the offline guard, strips proxy/npm env and ships site-dependencies.json without touching the build record', (t) => {
  const baseRoot = makeRoot(t);
  const { dir, sealed } = makeBaseKit(t, baseRoot, { signed: false });
  const outParent = temporary(t);
  const out = join(outParent, 'out');
  writeFileSync(
    join(outParent, 'site-dependencies.json'),
    '{"schemaVersion":1}\n',
  );
  const kit = { directory: dir, manifestSha256: sealed.manifestSha256 };
  const spawned = [];
  const saved = {
    HTTPS_PROXY: process.env.HTTPS_PROXY,
    npm_config_registry: process.env.npm_config_registry,
  };
  process.env.HTTPS_PROXY = 'http://proxy:1';
  process.env.npm_config_registry = 'https://registry.example.test/';
  t.after(() => {
    for (const [name, value] of Object.entries(saved))
      if (value === undefined) delete process.env[name];
      else process.env[name] = value;
  });
  launchOfflineBuild(
    { ...onlineOptions(t), online: true, devkit: dir, out },
    kit,
    {
      siteDependencies: join(outParent, 'site-dependencies.json'),
      spawn: (command, args, spawnOptions) => {
        spawned.push({ command, args, spawnOptions });
        return { status: 0 };
      },
    },
  );
  const { command, args, spawnOptions } = spawned[0];
  assert.equal(command, join(dir, 'build-node/node.exe'));
  assert.equal(args[1], '_build-worker');
  assert.match(spawnOptions.env.NODE_OPTIONS, /offline-guard\.cjs/);
  assert.equal(spawnOptions.env.HTTPS_PROXY, undefined);
  assert.equal(spawnOptions.env.npm_config_registry, undefined);
  assert.equal(spawnOptions.env.npm_config_offline, 'true');
  const worker = JSON.parse(readFileSync(join(out, 'site-options.json')));
  assert.equal(worker.offline, true);
  assert.equal(worker['site-derived'], true);
  assert.equal(worker['devkit-sha256'], sealed.manifestSha256);
  assert.equal(worker.online, undefined);
  assert.equal(worker['site-devkit'], undefined);
  assert.equal(
    readFileSync(join(out, 'site-dependencies.json'), 'utf8'),
    '{"schemaVersion":1}\n',
  );
  // 构建失败保留输出目录（含审阅清单）供排查。
  assert.throws(
    () =>
      launchOfflineBuild(
        {
          ...onlineOptions(t),
          online: true,
          devkit: dir,
          out: join(outParent, 'out2'),
        },
        kit,
        { spawn: () => ({ status: 3 }) },
      ),
    /退出码 3/,
  );
});

test('requires: unknown capabilities, bad types and case variants are refused; informational fields and old format still load', (t) => {
  const registry = {
    'a.json': { old: '', note: 'info', power: 'cap.power' },
  };
  assert.doesNotThrow(() => checkRequires('a.json', { old: 1 }, registry));
  assert.doesNotThrow(() =>
    checkRequires('a.json', { old: 1, extra: true }, registry),
  );
  assert.doesNotThrow(() =>
    checkRequires('a.json', { requires: [], note: 1 }, registry),
  );
  assert.doesNotThrow(() =>
    checkRequires('a.json', { requires: ['cap.power'] }, registry),
  );
  for (const bad of [
    { requires: ['cap.future'] },
    { requires: ['cap.power', 'cap.future'] },
    { requires: 'cap.power' },
    { requires: [1] },
    { requires: null },
    { Requires: [] },
    { requires: [], REQUIRES: [] },
  ])
    assert.throws(
      () => checkRequires('a.json', bad, registry),
      /requires|客户端发行脚本/,
    );
  assert.throws(
    () => checkRequires('a.json', { requires: ['cap.future'] }, registry),
    /请使用该套件发行页对应版本的源码/,
  );
  assert.throws(() => checkRequires('a.json', [], registry), /JSON 对象/);
  // 读取：真实清单文件上同样生效。
  const dir = temporary(t);
  writeFileSync(join(dir, 'dependency'), 'x');
  const sealed = sealInputs(dir, []);
  const verify = (patch) => {
    const body = {
      ...JSON.parse(readFileSync(join(dir, 'windows-client.json'))),
      ...patch,
    };
    const bytes = JSON.stringify(body, null, 2) + '\n';
    writeFileSync(join(dir, 'windows-client.json'), bytes);
    return verifyInputs(dir, digest(bytes), ROOT, { checkTools: false });
  };
  assert.equal(
    verify({ requires: [], futureInfo: 1 }).manifestSha256.length,
    64,
  );
  assert.throws(
    () => verify({ requires: ['cap.future'] }),
    /需要更新的客户端发行脚本/,
  );
  assert.throws(
    () => verify({ requires: [], Requires: [] }),
    /requires 键无效/,
  );
  assert.equal(sealed.requires, undefined);
});

test('requires: signed release metadata with an unknown capability is refused', (t) => {
  const baseRoot = makeRoot(t);
  const { dir, keyFile } = makeBaseKit(t, baseRoot, {
    meta: { requires: ['cap.future'] },
  });
  assert.throws(
    () => verifySkip(dir, baseRoot, keyFile),
    /需要更新的客户端发行脚本/,
  );
  const ok = makeBaseKit(t, baseRoot, { meta: { requires: [], note: 'x' } });
  assert.doesNotThrow(() => verifySkip(ok.dir, baseRoot, ok.keyFile));
});

test('field registry gate: every written top-level field is registered and baseline output has no requires', (t) => {
  const baseRoot = makeRoot(t);
  const { dir, sealed } = makeBaseKit(t, baseRoot, { signed: false });
  const manifest = JSON.parse(readFileSync(join(dir, 'windows-client.json')));
  // 写出的清单没有 requires，且全部字段已登记为基线（与本机制之前逐字节一致）。
  assert.equal('requires' in manifest, false);
  assert.deepEqual(
    Object.keys(manifest).filter(
      (k) => !(k in FIELD_REGISTRY['windows-client.json']),
    ),
    [],
  );
  assert.equal(sealed.requires, undefined);
  assert.equal(
    readFileSync(join(dir, 'windows-client.json'), 'utf8'),
    JSON.stringify(manifest, null, 2) + '\n',
  );
  const keys = join(temporary(t), 'keys');
  generateDesktopKeys(keys);
  const signed = signInputs(dir, join(keys, 'desktop-private.pem'), '0.0.1', {
    checkTools: false,
    root: baseRoot,
  });
  const release = JSON.parse(
    readFileSync(join(dir, 'client-inputs-release.json')),
  );
  assert.equal('requires' in release, false);
  assert.deepEqual(
    Object.keys(release).filter(
      (k) => !(k in FIELD_REGISTRY['client-inputs-release.json']),
    ),
    [],
  );
  assert.equal(signed.requires, undefined);
  for (const [name, fields] of Object.entries(FIELD_REGISTRY))
    for (const feature of Object.values(fields))
      assert.equal(feature, '', `${name} 现在只应有基线字段`);
  // 门禁本身：漏登记的字段写出时失败；说明性字段只带空 requires；能力字段带能力名。
  const registry = { 'a.json': { old: '', note: 'info', power: 'cap.power' } };
  assert.throws(
    () => stampRequires('a.json', { old: 1, forgotten: 1 }, registry),
    /尚未在 compat\.mjs 登记/,
  );
  assert.throws(() => stampRequires('unknown.json', {}, registry), /未登记/);
  const plain = { old: 1 };
  assert.equal(stampRequires('a.json', plain, registry), plain);
  assert.deepEqual(
    stampRequires('a.json', { old: 1, note: 2 }, registry).requires,
    [],
  );
  assert.deepEqual(
    stampRequires('a.json', { old: 1, note: 2, power: 3 }, registry).requires,
    ['cap.power'],
  );
});

// ---------------------------------------------------------------------------
// 返工补充测试
// ---------------------------------------------------------------------------

test('pnpm-lock parsing only treats keys indented exactly two spaces as packages', () => {
  const text = [
    "lockfileVersion: '9.0'",
    '',
    'packages:',
    '',
    '  pkg-a@1.0.0:',
    '    resolution: {integrity: sha512-A}',
    '    peerDependencies:',
    '      msw: ^2.0.0',
    '    peerDependenciesMeta:',
    '      msw:',
    '        optional: true',
    '',
    "  '@s/b@2.0.0':",
    '    resolution: {integrity: sha512-B}',
    '    dependencies:',
    "      '@s/c': 1.0.0",
    '',
    'snapshots:',
    '  x@1: {}',
    '',
  ].join('\n');
  const parsed = parsePnpmPackages(text);
  assert.deepEqual([...parsed.keys()], ['pkg-a@1.0.0', '@s/b@2.0.0']);
  assert.equal(parsed.get('@s/b@2.0.0'), 'sha512-B');
  // 真实锁文件：没有带前导空格的假条目，且每个条目都是 name@version。
  const real = parsePnpmPackages(
    readFileSync(join(ROOT, 'web/pnpm-lock.yaml'), 'utf8'),
  );
  assert.ok(real.size > 100);
  for (const key of real.keys()) assert.match(key, /^[^\s'"].*@\S+$/, key);
});

test('copy re-check: a derived kit that differs from the verified base manifest fails and is cleaned up', (t) => {
  const baseRoot = makeRoot(t);
  const root = makeRoot(t, withPnpmPackage);
  const { dir, keyFile } = makeBaseKit(t, baseRoot);
  const base = verifySkip(dir, root, keyFile);
  const tampered = {
    ...base,
    files: base.files.map((f, i) =>
      i === 0 ? { ...f, sha256: '0'.repeat(64) } : f,
    ),
  };
  const site = join(dirname(dir), 'site');
  const calls = [];
  assert.throws(
    () =>
      deriveSiteDevkit({
        baseDir: dir,
        site,
        root,
        base: tampered,
        plan: { changed: [PNPM_LOCK], pnpm: true, plugin: false },
        dependencies: { added: [], removedCount: 0, warnings: [] },
        runner: (...args) => calls.push(args),
      }),
    /与已校验的基础套件不一致/,
  );
  assert.equal(calls.length, 0, '复核失败必须发生在联网之前');
  assert.equal(existsSync(site), false);
});

test('a stray site-dependencies.json in the base kit is not copied into the derived kit', (t) => {
  const baseRoot = makeRoot(t);
  const root = makeRoot(t, withPnpmPackage);
  const { dir, keyFile } = makeBaseKit(t, baseRoot);
  writeFileSync(join(dir, 'site-dependencies.json'), '{"stale":true}');
  const base = verifySkip(dir, root, keyFile);
  const site = join(dirname(dir), 'site');
  const { report } = deriveSiteDevkit({
    baseDir: dir,
    site,
    root,
    base,
    plan: { changed: [PNPM_LOCK], pnpm: true, plugin: false },
    dependencies: { added: [], removedCount: 0, warnings: [] },
    runner: () => {},
  });
  const written = JSON.parse(
    readFileSync(join(site, 'site-dependencies.json'), 'utf8'),
  );
  assert.equal(written.stale, undefined);
  assert.deepEqual(written, report);
});

test('new packages without integrity produce a warning in the report', (t) => {
  const baseRoot = makeRoot(t);
  const root = makeRoot(t, (r) => {
    const file = join(r, PNPM_LOCK);
    writeFileSync(
      file,
      readFileSync(file, 'utf8').replace(
        '\npackages:\n',
        "\npackages:\n\n  'nointegrity@1.0.0':\n    resolution: {tarball: https://example.test/x.tgz}\n",
      ),
    );
  });
  const { dir } = makeBaseKit(t, baseRoot, { signed: false });
  const result = collectDependencyChanges({
    baseDir: dir,
    baseLocks: lockIdentity(baseRoot).locks,
    root,
    changed: [PNPM_LOCK],
    pnpmBaseSources: [() => readFileSync(join(baseRoot, PNPM_LOCK))],
  });
  assert.deepEqual(
    result.added.map((p) => [p.name, p.integrity]),
    [['nointegrity', null]],
  );
  assert.match(
    result.warnings.join('\n'),
    /没有 integrity.*nointegrity@1\.0\.0/,
  );
});

test('online: package.json-only changes derive without network; bad --base-pnpm-lock fails before any copy or network', async (t) => {
  const baseRoot = makeRoot(t);
  const { dir, keyFile } = makeBaseKit(t, baseRoot);
  const verify = (directory, expected, root, options) =>
    verifyInputs(directory, expected, root, { ...options, checkTools: false });
  const calls = [];
  const make = (root, extra = {}) => {
    const o = onlineOptions(t, extra);
    o.devkit = dir;
    o['verify-key'] = keyFile;
    return {
      o,
      io: {
        root,
        sourceIdentity: () => ({ sourceCommit: 'a'.repeat(40) }),
        verifyBase: verify,
        verifyDerived: verify,
        runner: (...args) => calls.push(args),
        gitShow: () => null,
        log: () => {},
        env: { PATH: 'p' },
        launch: () => {},
      },
    };
  };
  // package.json 变了但锁没变。
  const member = Object.keys(lockIdentity(baseRoot).locks).find((p) =>
    /^web\/[^/]+\/package\.json$/.test(p),
  );
  const orphan = makeRoot(t, (r) => {
    const file = join(r, member);
    const json = JSON.parse(readFileSync(file, 'utf8'));
    json.description = 'changed without lock';
    writeFileSync(file, JSON.stringify(json, null, 2));
  });
  // 只改 package.json（锁不变）：不报错、不联网，照常派生、重封清单并继续构建。
  let ctx = make(orphan);
  const quiet = [];
  ctx.io.log = (line) => quiet.push(line);
  let launched = 0;
  ctx.io.launch = () => launched++;
  const only = await startOnlineBuild(ctx.o, ctx.io);
  assert.equal(calls.length, 0, '锁未变不得联网');
  assert.equal(launched, 1);
  assert.match(
    quiet.join('\n'),
    /依赖锁未变，仅 package\.json 变化，未联网下载/,
  );
  const derived = JSON.parse(
    readFileSync(join(ctx.o['site-devkit'], 'windows-client.json'), 'utf8'),
  );
  assert.equal(derived.locks[member], lockIdentity(orphan).locks[member]);
  assert.notEqual(derived.locks[member], lockIdentity(baseRoot).locks[member]);
  assert.equal(only.added, 0);
  // --base-pnpm-lock：只能与 --online 同用；路径错误、摘要不符都明确报错。
  assert.throws(
    () =>
      validateBuildOptions({
        ...onlineOptions(t),
        online: undefined,
        offline: true,
        'site-devkit': undefined,
        'base-pnpm-lock': 'x',
      }),
    /只能与 --online 同用/,
  );
  const root = makeRoot(t, withPnpmPackage);
  ctx = make(root, { 'base-pnpm-lock': join(temporary(t), 'missing.yaml') });
  await assert.rejects(
    async () => startOnlineBuild(ctx.o, ctx.io),
    /无法读取 --base-pnpm-lock/,
  );
  const wrong = join(temporary(t), 'wrong.yaml');
  writeFileSync(wrong, 'not the base lock');
  ctx = make(root, { 'base-pnpm-lock': wrong });
  await assert.rejects(
    async () => startOnlineBuild(ctx.o, ctx.io),
    /摘要与基础套件记录/,
  );
  assert.equal(existsSync(ctx.o['site-devkit']), false);
  assert.equal(calls.length, 0);
  // 正确的原始锁：成功，且新增清单完整、没有降级警告。
  const good = join(temporary(t), 'base.yaml');
  cpSync(join(baseRoot, PNPM_LOCK), good);
  ctx = make(root, { 'base-pnpm-lock': good });
  const logs = [];
  ctx.io.log = (line) => logs.push(line);
  const result = await startOnlineBuild(ctx.o, ctx.io);
  assert.equal(result.added, 1);
  assert.doesNotMatch(logs.join('\n'), /警告/);
});

test('directory containment compares normalized real paths on separator boundaries', (t) => {
  const dir = temporary(t);
  mkdirSync(join(dir, 'a/..foo'), { recursive: true });
  mkdirSync(join(dir, 'ab'));
  assert.equal(isInside(join(dir, 'a/..foo'), join(dir, 'a')), true);
  assert.equal(isInside(join(dir, 'a'), join(dir, 'a')), true);
  assert.equal(isInside(join(dir, 'ab'), join(dir, 'a')), false);
  assert.equal(isInside(join(dir, 'a'), join(dir, 'a/..foo')), false);
  if (process.platform === 'win32')
    assert.equal(isInside(join(dir, 'a/new/child'), join(dir, 'A')), true);
  // junction 别名落在基础目录里的不存在的子目录，也要识别为在内。
  symlinkSync(
    join(dir, 'a'),
    join(dir, 'alias'),
    process.platform === 'win32' ? 'junction' : 'dir',
  );
  assert.equal(isInside(join(dir, 'alias/new'), join(dir, 'a')), true);
  assert.equal(isInside(join(dir, 'ab/new'), join(dir, 'alias')), false);
});

test('plugin package.json-only changes: dependency declarations must match the base kit, other fields pass without network', async (t) => {
  const baseRoot = makeRoot(t);
  const { dir, keyFile } = makeBaseKit(t, baseRoot);
  const verify = (directory, expected, root, options) =>
    verifyInputs(directory, expected, root, { ...options, checkTools: false });
  const calls = [];
  const logs = [];
  const make = (root) => {
    const o = onlineOptions(t);
    o.devkit = dir;
    o['verify-key'] = keyFile;
    return {
      o,
      io: {
        root,
        sourceIdentity: () => ({ sourceCommit: 'a'.repeat(40) }),
        verifyBase: verify,
        verifyDerived: verify,
        runner: (...args) => calls.push(args),
        gitShow: () => null,
        log: (line) => logs.push(line),
        env: { PATH: 'p' },
        launch: () => {},
      },
    };
  };
  const pluginRoot = (patch) =>
    makeRoot(t, (r) => {
      const file = join(r, 'clients/dsh-plugin/package.json');
      const json = JSON.parse(readFileSync(file, 'utf8'));
      patch(json);
      writeFileSync(file, JSON.stringify(json, null, 2));
    });
  // 依赖声明变了而插件锁没变：联网和复制之前就失败，提示更新插件锁。
  for (const field of [
    'dependencies',
    'optionalDependencies',
    'peerDependencies',
    'bundledDependencies',
  ]) {
    const ctx = make(
      pluginRoot((json) => {
        json[field] =
          field === 'bundledDependencies' ? ['x'] : { 'added-dep': '1.0.0' };
      }),
    );
    await assert.rejects(
      async () => startOnlineBuild(ctx.o, ctx.io),
      new RegExp(`${field}.*npm install --package-lock-only`),
    );
    assert.equal(existsSync(ctx.o['site-devkit']), false);
  }
  assert.equal(calls.length, 0);
  // 只改 scripts/version：放行、零联网，只同步 package.json 副本，日志不出现“开始联网”。
  const ctx = make(
    pluginRoot((json) => {
      json.scripts = { ...json.scripts, extra: 'echo extra' };
      json.version = '9.9.9';
    }),
  );
  await startOnlineBuild(ctx.o, ctx.io);
  assert.equal(calls.length, 0);
  assert.match(
    logs.join('\n'),
    /依赖锁未变，仅 package\.json 变化，未联网下载/,
  );
  assert.doesNotMatch(logs.join('\n'), /开始联网/);
  const synced = JSON.parse(
    readFileSync(
      join(ctx.o['site-devkit'], 'plugin-deps/package.json'),
      'utf8',
    ),
  );
  assert.equal(synced.version, '9.9.9');
  assert.equal(
    existsSync(
      join(ctx.o['site-devkit'], 'plugin-deps/node_modules/old/index.js'),
    ),
    true,
  );
});

// ---- 源码树只读构建 / clean-source ----
const snapshotTree = (root) => {
  const found = [];
  const walk = (prefix = '') => {
    for (const entry of readdirSync(join(root, prefix), {
      withFileTypes: true,
    })) {
      const name = prefix ? `${prefix}/${entry.name}` : entry.name;
      if (entry.isSymbolicLink()) found.push(`${name}:link`);
      else if (entry.isDirectory()) walk(name);
      else found.push(`${name}:${readFileSync(join(root, name), 'utf8')}`);
    }
  };
  walk();
  return found.sort();
};
function makeSourceTree(t) {
  const root = temporary(t);
  const write = (path, text) => {
    mkdirSync(dirname(join(root, path)), { recursive: true });
    writeFileSync(join(root, path), text);
  };
  write('scripts/client-release/build-node.json', '{"version":"22.19.0"}\n');
  write('scripts/client-release/cli.mjs', '// worker\n');
  write('scripts/client-release/offline-guard.cjs', '// guard\n');
  write('package.json', '{"name":"fixture"}\n');
  write('web/web/src/main.ts', 'export {};\n');
  write('notes/dist-report.txt', '用户文件，名字像生成物但不是目录\n');
  write('docs/node_modules.md', '用户文档\n');
  return root;
}
// 假 runner：像真实 worker 一样在“自己所在的源码副本”里装依赖、产出 dist 和 ui。
function fakeWorker(seen) {
  return (command, args, options) => {
    const copy = options.cwd;
    seen.push({ copy, args });
    assert.equal(args[0], join(copy, 'scripts/client-release/cli.mjs'));
    assert.match(options.env.NODE_OPTIONS, /build-src/);
    assert.equal(existsSync(join(copy, 'web/web/node_modules')), false);
    for (const generated of [
      'node_modules/pnpm/x.js',
      'web/web/node_modules/vue/index.js',
      'clients/dsh-plugin/node_modules/a/b.js',
      'web/web/dist/index.html',
      'clients/dsh-plugin/ui/panel.js',
    ]) {
      mkdirSync(dirname(join(copy, generated)), { recursive: true });
      writeFileSync(join(copy, generated), 'generated');
    }
    return { status: 0 };
  };
}

test('build never writes into the source tree: twice in a row both pass, copy is removed, user files untouched', (t) => {
  const source = makeSourceTree(t);
  const before = snapshotTree(source);
  const base = makeRoot(t);
  const { dir, sealed } = makeBaseKit(t, base, { signed: false });
  const kit = { directory: dir, manifestSha256: sealed.manifestSha256 };
  const parent = temporary(t);
  for (const name of ['out1', 'out2']) {
    const seen = [];
    const out = join(parent, name);
    launchOfflineBuild(
      { ...onlineOptions(t), online: true, devkit: dir, out },
      kit,
      { root: source, spawn: fakeWorker(seen) },
    );
    assert.equal(seen.length, 1);
    assert.equal(seen[0].copy, join(out, 'build-src'));
    assert.equal(existsSync(join(out, 'build-src')), false);
    assert.equal(
      JSON.parse(readFileSync(join(out, 'site-options.json')))['source-root'],
      source,
    );
    // 源码树没有新增任何文件，第二次构建前的源码门禁照样通过。
    assert.deepEqual(snapshotTree(source), before);
    assert.doesNotThrow(() => assertBuildSourceClean(source));
  }
  // 失败的构建同样不留副本、不动源码。
  const failed = join(parent, 'out3');
  assert.throws(
    () =>
      launchOfflineBuild(
        { ...onlineOptions(t), online: true, devkit: dir, out: failed },
        kit,
        {
          root: source,
          spawn: (c, a, o) => {
            fakeWorker([])(c, a, o);
            return { status: 7 };
          },
        },
      ),
    /退出码 7/,
  );
  assert.equal(existsSync(join(failed, 'build-src')), false);
  assert.deepEqual(snapshotTree(source), before);
});

test('copyBuildSource skips node_modules/.git/target, rejects links and its copy passes the clean-source gate', (t) => {
  const source = makeSourceTree(t);
  mkdirSync(join(source, '.git'));
  writeFileSync(join(source, '.git/HEAD'), 'ref');
  mkdirSync(join(source, 'target'));
  writeFileSync(join(source, 'target/x'), 'x');
  mkdirSync(join(source, 'web/web/node_modules/stale'), { recursive: true });
  writeFileSync(join(source, 'web/web/node_modules/stale/index.js'), 'old');
  const dest = join(temporary(t), 'copy');
  copyBuildSource(source, dest);
  assert.equal(existsSync(join(dest, '.git')), false);
  assert.equal(existsSync(join(dest, 'target')), false);
  assert.equal(existsSync(join(dest, 'web/web/node_modules')), false);
  assert.equal(
    readFileSync(join(dest, 'web/web/src/main.ts'), 'utf8'),
    'export {};\n',
  );
  assert.equal(existsSync(join(dest, 'notes/dist-report.txt')), true);
  assert.doesNotThrow(() => assertBuildSourceClean(dest));
  assert.throws(() => assertBuildSourceClean(source), /clean-source/);
  const linked = makeSourceTree(t);
  try {
    symlinkSync(join(linked, 'package.json'), join(linked, 'link.json'));
  } catch {
    return; // 无符号链接权限的环境跳过这一小段。
  }
  assert.throws(() => copyBuildSource(linked, join(temporary(t), 'c')), /链接/);
});

test('clean-source removes only known generated directories, handles very long paths, refuses non-source dirs', async (t) => {
  const source = makeSourceTree(t);
  const user = [
    'notes/dist-report.txt',
    'docs/node_modules.md',
    'web/web/src/main.ts',
    'package.json',
  ];
  mkdirSync(join(source, 'web/web/node_modules/stale'), { recursive: true });
  // 超过 260 字符的深层路径（element-plus 在 .pnpm 下的情形）。
  let deep = join(source, 'node_modules/.pnpm');
  for (let i = 0; i < 12; i++)
    deep = join(deep, 'element-plus-very-long-name-' + i);
  mkdirSync(deep, { recursive: true });
  writeFileSync(join(deep, 'index.js'), 'x');
  mkdirSync(join(source, 'clients/dsh-plugin/dist'), { recursive: true });
  mkdirSync(join(source, 'clients/dsh-plugin/ui'), { recursive: true });
  writeFileSync(join(source, 'clients/dsh-plugin/ui/panel.js'), 'ui');
  mkdirSync(join(source, '.git/node_modules'), { recursive: true });
  const outside = temporary(t);
  writeFileSync(join(outside, 'keep.txt'), 'outside');
  assert.ok(deep.length > 300);
  const result = await clientMain(['clean-source', source]);
  assert.deepEqual(result.removed.sort(), [
    'clients/dsh-plugin/dist',
    'node_modules',
    'web/web/node_modules',
  ]);
  assert.equal(existsSync(join(source, 'node_modules')), false);
  assert.equal(existsSync(join(source, 'clients/dsh-plugin/dist')), false);
  // ui 默认保留；.git 内不碰。
  assert.equal(
    existsSync(join(source, 'clients/dsh-plugin/ui/panel.js')),
    true,
  );
  assert.equal(existsSync(join(source, '.git/node_modules')), true);
  for (const file of user) assert.equal(existsSync(join(source, file)), true);
  assert.equal(
    cleanSource(source, { includeUi: true }).removed[0],
    'clients/dsh-plugin/ui',
  );
  assert.equal(existsSync(join(source, 'clients/dsh-plugin/ui')), false);
  assert.equal(readFileSync(join(outside, 'keep.txt'), 'utf8'), 'outside');
  assert.doesNotThrow(() => assertBuildSourceClean(source));
  // 目录里没有 Auriga 构建标记就拒绝，避免误删别处的 node_modules。
  const foreign = temporary(t);
  mkdirSync(join(foreign, 'node_modules'));
  assert.throws(() => cleanSource(foreign), /不是 Auriga 源码目录/);
  assert.equal(existsSync(join(foreign, 'node_modules')), true);
  await assert.rejects(() => clientMain(['clean-source']), /用法/);
});

function gitFixture(t, mutate) {
  const root = makeRoot(t, mutate);
  const git = (...args) =>
    assert.equal(
      spawnSync(
        'git',
        [
          '-c',
          'user.name=Fixture',
          '-c',
          'user.email=fixture@example.test',
          '-c',
          'commit.gpgsign=false',
          ...args,
        ],
        { cwd: root, windowsHide: true },
      ).status,
      0,
    );
  git('init', '-q');
  git('add', '-A');
  git('commit', '-q', '-m', 'fixture');
  return root;
}

test('copyBuildSource uses the same file rules as source identity: no secrets, archives, binaries or runtime data in the copy', (t) => {
  const source = makeSourceTree(t);
  const write = (path, text) => {
    mkdirSync(dirname(join(source, path)), { recursive: true });
    writeFileSync(join(source, path), text);
  };
  for (const secret of [
    '.env',
    '.env.production',
    'web/web/.env.local',
    'keys/release.pem',
    'keys/private.key',
    'tools/helper.exe',
    'auriga-offline-1.0.tar',
    'auriga-devkit-1.0.tar.sha256',
    'bin/auriga',
    'data/db/file',
    'deploy/compose/var/dist/x',
    '.dev/cache/x',
    '.agents/notes.md',
  ])
    write(secret, 'secret');
  for (const meta of ['auriga-source.json', 'SHA256SUMS', 'release-public.pem'])
    write(meta, 'meta');
  write('.env.example', 'kept');
  const dest = join(temporary(t), 'copy');
  copyBuildSource(source, dest);
  const copied = [];
  const walk = (prefix = '') => {
    for (const entry of readdirSync(join(dest, prefix), {
      withFileTypes: true,
    })) {
      const name = prefix ? `${prefix}/${entry.name}` : entry.name;
      if (entry.isDirectory()) walk(name);
      else copied.push(name);
    }
  };
  walk();
  assert.deepEqual(
    copied.sort(),
    [
      '.env.example',
      'SHA256SUMS',
      'auriga-source.json',
      'docs/node_modules.md',
      'notes/dist-report.txt',
      'package.json',
      'release-public.pem',
      'scripts/client-release/build-node.json',
      'scripts/client-release/cli.mjs',
      'scripts/client-release/offline-guard.cjs',
      'web/web/src/main.ts',
    ].sort(),
  );
});

test('a failing copy (source contains a link) still removes the temporary build directory and leaves the source untouched', (t) => {
  const source = makeSourceTree(t);
  const outside = temporary(t);
  symlinkSync(outside, join(source, 'linkdir'), 'junction');
  const before = snapshotTree(source);
  const { dir, sealed } = makeBaseKit(t, makeRoot(t), { signed: false });
  const out = join(temporary(t), 'out');
  assert.throws(
    () =>
      launchOfflineBuild(
        { ...onlineOptions(t), online: true, devkit: dir, out },
        { directory: dir, manifestSha256: sealed.manifestSha256 },
        { root: source, spawn: () => assert.fail('不应启动 worker') },
      ),
    /链接/,
  );
  assert.equal(existsSync(join(out, 'build-src')), false);
  assert.deepEqual(snapshotTree(source), before);
});

test('real buildWorker with source-root refuses a build copy that differs from the verified source, and accepts an identical one', async (t) => {
  if (process.version !== `v${BUILD_NODE}`)
    return t.skip('需要固定的构建 Node');
  const source = gitFixture(t, (root) => {
    mkdirSync(join(root, 'src'));
    writeFileSync(join(root, 'src/app.js'), 'original');
  });
  const { dir } = makeBaseKit(t, source, { signed: false });
  rmSync(join(dir, 'windows-client.json'));
  const sealed = sealInputs(dir, [], source);
  const saved = {
    AURIGA_CLIENT_NETWORK_LOG: process.env.AURIGA_CLIENT_NETWORK_LOG,
    NODE_OPTIONS: process.env.NODE_OPTIONS,
  };
  process.env.AURIGA_CLIENT_NETWORK_LOG = join(temporary(t), 'denials.log');
  process.env.NODE_OPTIONS = '--require=offline-guard.cjs';
  t.after(() => {
    for (const [name, value] of Object.entries(saved))
      if (value === undefined) delete process.env[name];
      else process.env[name] = value;
  });
  const run = async (mutateCopy) => {
    const copy = join(temporary(t), 'copy');
    copyBuildSource(source, copy);
    mutateCopy?.(copy);
    const out = join(temporary(t), 'out');
    mkdirSync(out);
    const options = onlineOptions(t);
    delete options.online;
    delete options['site-devkit'];
    const file = join(out, 'site-options.json');
    writeFileSync(
      file,
      JSON.stringify({
        ...options,
        offline: true,
        'site-derived': true,
        'devkit-sha256': sealed.manifestSha256,
        devkit: dir,
        out,
        'source-root': source,
      }),
    );
    return buildWorker(file, { root: copy, checkTools: false });
  };
  // 与源码一致：越过副本校验，停在（假的）pnpm 安装步骤。
  await assert.rejects(() => run(), /离线构建步骤失败/);
  // 副本被篡改（改内容、删文件）：在装依赖前拒绝。
  await assert.rejects(
    () => run((copy) => writeFileSync(join(copy, 'src/app.js'), 'tampered')),
    /构建副本与源码身份不一致/,
  );
  await assert.rejects(
    () => run((copy) => rmSync(join(copy, 'src/app.js'))),
    /构建副本与源码身份不一致/,
  );
});

test('clean-source never follows junctions out of the tree', (t) => {
  const source = makeSourceTree(t);
  const outsideA = temporary(t);
  const outsideB = temporary(t);
  writeFileSync(join(outsideA, 'keep.txt'), 'A');
  mkdirSync(join(outsideB, 'inner'));
  writeFileSync(join(outsideB, 'inner/keep.txt'), 'B');
  symlinkSync(outsideA, join(source, 'node_modules'), 'junction');
  symlinkSync(outsideB, join(source, 'web/web/dist'), 'junction');
  // 非生成物名字的 junction 不进入也不删除。
  symlinkSync(outsideA, join(source, 'docs/shared'), 'junction');
  const result = cleanSource(source);
  assert.deepEqual(result.removed.sort(), ['node_modules', 'web/web/dist']);
  assert.equal(existsSync(join(source, 'node_modules')), false);
  assert.equal(existsSync(join(source, 'web/web/dist')), false);
  assert.equal(readFileSync(join(outsideA, 'keep.txt'), 'utf8'), 'A');
  assert.equal(readFileSync(join(outsideB, 'inner/keep.txt'), 'utf8'), 'B');
  assert.equal(existsSync(join(source, 'docs/shared')), true);
});

test('clean-source walks exactly like the build gate: ignored runtime directories are never entered', (t) => {
  const source = makeSourceTree(t);
  const survivors = [
    'bin/x/node_modules/a.js',
    'data/y/dist/a.js',
    'target/node_modules/a.js',
    'deploy/compose/var/volume/dist/a.js',
    '.dev/node_modules/a.js',
    '.agents/dist/a.js',
    '.git/node_modules/a.js',
  ];
  for (const path of [...survivors, 'web/web/dist/a.js']) {
    mkdirSync(dirname(join(source, path)), { recursive: true });
    writeFileSync(join(source, path), 'x');
  }
  // 门禁只会报 web/web/dist：先删掉它，门禁立即通过，说明 clean-source 与门禁范围一致。
  assert.throws(() => assertBuildSourceClean(source), /web\/web\/dist/);
  assert.deepEqual(cleanSource(source).removed, ['web/web/dist']);
  assert.doesNotThrow(() => assertBuildSourceClean(source));
  for (const path of survivors)
    assert.equal(existsSync(join(source, path)), true, path);
});

test('clean-source --include-ui refuses when ui belongs to the signed baseline or is tracked by Git', (t) => {
  const tracked = gitFixture(t, (root) => {
    mkdirSync(join(root, 'clients/dsh-plugin/ui'), { recursive: true });
    writeFileSync(join(root, 'clients/dsh-plugin/ui/panel.js'), 'source');
  });
  assert.throws(
    () => cleanSource(tracked, { includeUi: true }),
    /属于签名源码基线或已被 Git 跟踪/,
  );
  assert.equal(
    existsSync(join(tracked, 'clients/dsh-plugin/ui/panel.js')),
    true,
  );
  // 未跟踪的生成物：允许。
  const generated = gitFixture(t);
  mkdirSync(join(generated, 'clients/dsh-plugin/ui'), { recursive: true });
  writeFileSync(join(generated, 'clients/dsh-plugin/ui/panel.js'), 'gen');
  assert.deepEqual(cleanSource(generated, { includeUi: true }).removed, [
    'clients/dsh-plugin/ui',
  ]);
  // 签名基线含 ui：拒绝。
  const signed = makeSourceTree(t);
  mkdirSync(join(signed, 'clients/dsh-plugin/ui'), { recursive: true });
  writeFileSync(join(signed, 'clients/dsh-plugin/ui/panel.js'), 'source');
  writeFileSync(
    join(signed, 'auriga-source.json'),
    JSON.stringify({ files: { 'clients/dsh-plugin/ui/panel.js': {} } }),
  );
  assert.throws(
    () => cleanSource(signed, { includeUi: true }),
    /属于签名源码基线/,
  );
  assert.equal(
    existsSync(join(signed, 'clients/dsh-plugin/ui/panel.js')),
    true,
  );
});
