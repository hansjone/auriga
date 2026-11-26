/** 客户端 deps：files 源映射、基线、缓存/套件解析。不联网。 */
import test from 'node:test';
import assert from 'node:assert/strict';
import { createHash, generateKeyPairSync, sign } from 'node:crypto';
import { Buffer } from 'node:buffer';
import {
  existsSync,
  mkdtempSync,
  mkdirSync,
  readFileSync,
  writeFileSync,
  rmSync,
} from 'node:fs';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';
import {
  ROOT,
  sealInputs,
  verifyInputs,
  digest,
  systemTools,
  readBuildNodePin,
  BUILD_NODE,
  BUILD_NODE_ZIP_SHA256,
  HASH,
} from './inputs.mjs';
import { createDownloader, mapLockedUrlCandidates } from './files-source.mjs';
import {
  TOOLS_BASELINE_MISMATCH,
  assertToolsAllowed,
  matchTools,
  registerBaseline,
  readBaseline,
} from './baseline.mjs';
import {
  CLIENT_ECO,
  clientCacheEntry,
  clientFetch,
  clientStatus,
  findClientKit,
  clientImport,
  parseArgs,
  resolveDevkit,
} from './site-deps.mjs';
import { validateBuildOptions, systemToolsEvidence } from './build.mjs';
import { lockIdentity } from './inputs.mjs';
import { sha256File } from '../../clients/installer/runtime/verify.mjs';

function temporary(t) {
  mkdirSync(join(ROOT, 'clients/installer/target'), { recursive: true });
  const directory = mkdtempSync(
    join(ROOT, 'clients/installer/target/site-deps-'),
  );
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  return directory;
}

test('files 源：锁定 URL 先 pathname 再 basename', () => {
  const locked = 'https://nodejs.org/dist/v22.19.0/node-v22.19.0-win-x64.zip';
  const files = 'https://files.internal.example/auriga-inputs';
  assert.deepEqual(mapLockedUrlCandidates(locked, files), [
    'https://files.internal.example/auriga-inputs/dist/v22.19.0/node-v22.19.0-win-x64.zip',
    'https://files.internal.example/auriga-inputs/node-v22.19.0-win-x64.zip',
  ]);
});

test('files 源：file:// 映射、pathname 优先、basename 回落；摘要不符由调用方拒绝', async (t) => {
  const root = temporary(t);
  const mirror = join(root, 'mirror');
  mkdirSync(join(mirror, 'dist', 'v1'), { recursive: true });
  writeFileSync(join(mirror, 'dist', 'v1', 'artifact.bin'), 'correct-bytes');
  writeFileSync(join(mirror, 'artifact.bin'), 'basename-fallback');
  const filesSource = pathToFileURL(mirror).href;
  const locked = 'https://example.test/dist/v1/artifact.bin';
  const download = createDownloader({ filesSource, allowPublic: false });

  const viaPath = join(root, 'out-path.bin');
  await download(locked, viaPath);
  assert.equal(readFileSync(viaPath, 'utf8'), 'correct-bytes');

  rmSync(join(mirror, 'dist', 'v1', 'artifact.bin'));
  const viaBase = join(root, 'out-base.bin');
  await download(locked, viaBase);
  assert.equal(readFileSync(viaBase, 'utf8'), 'basename-fallback');

  const expected = createHash('sha256').update('correct-bytes').digest('hex');
  assert.notEqual(sha256File(viaBase), expected);
  assert.throws(() => {
    if (sha256File(viaBase) !== expected)
      throw new Error(`文件摘要不符：artifact.bin`);
  }, /文件摘要不符/);
});

test('allowPublic=false 且无 files 源时失败', async () => {
  const download = createDownloader({ allowPublic: false });
  await assert.rejects(
    () =>
      download(
        'https://example.test/pkg.zip',
        join(ROOT, 'clients/installer/target', 'should-not-exist.zip'),
      ),
    /缺少文件类依赖源/,
  );
});

test('系统工具基线：命中与不符提示', (t) => {
  const cache = temporary(t);
  const current = [
    { path: 'System32/tar.exe', sha256: 'a'.repeat(64) },
    {
      path: 'System32/WindowsPowerShell/v1.0/powershell.exe',
      sha256: 'b'.repeat(64),
    },
  ];
  const old = [
    { path: 'System32/tar.exe', sha256: 'c'.repeat(64) },
    {
      path: 'System32/WindowsPowerShell/v1.0/powershell.exe',
      sha256: 'd'.repeat(64),
    },
  ];
  assert.equal(matchTools(current, current), true);
  assert.equal(matchTools(current, old), false);
  assert.deepEqual(assertToolsAllowed(current, current, null), {
    source: 'manifest',
  });
  assert.throws(
    () => assertToolsAllowed(current, old, null),
    (err) => err.message === TOOLS_BASELINE_MISMATCH,
  );
  const baseline = registerBaseline(cache, current);
  assert.equal(readBaseline(cache).registeredAt, baseline.registeredAt);
  assert.deepEqual(assertToolsAllowed(current, old, baseline), {
    source: 'baseline',
    baseline,
  });
  assert.throws(
    () => assertToolsAllowed(old, current, baseline),
    (err) => err.message === TOOLS_BASELINE_MISMATCH,
  );
});

test('verifyInputs：基线放宽工具但不绕过非工具摘要', (t) => {
  if (process.platform !== 'win32') {
    t.skip('需要 Windows 采集系统工具');
    return;
  }
  const dir = temporary(t);
  const tools = systemTools();
  writeFileSync(join(dir, 'dependency'), 'pinned bytes');
  const staleTools = tools.map((row, i) =>
    i === 0 ? { ...row, sha256: '0'.repeat(64) } : row,
  );
  const manifest = sealInputs(dir, staleTools);
  const cache = temporary(t);
  registerBaseline(cache, tools);

  const ok = verifyInputs(dir, manifest.manifestSha256, ROOT, {
    checkTools: true,
    toolsBaseline: cache,
  });
  assert.equal(ok.toolsMatch.source, 'baseline');

  writeFileSync(join(dir, 'dependency'), 'tampered');
  assert.throws(
    () =>
      verifyInputs(dir, manifest.manifestSha256, ROOT, {
        checkTools: true,
        toolsBaseline: cache,
      }),
    /dependency/,
  );

  writeFileSync(join(dir, 'dependency'), 'pinned bytes');
  assert.throws(
    () =>
      verifyInputs(dir, manifest.manifestSha256, ROOT, {
        checkTools: true,
        toolsBaseline: null,
      }),
    (err) => err.message === TOOLS_BASELINE_MISMATCH,
  );
});

// —— 以下为独审补充：缓存记录、套件独立信任、原子导入、files 编码、基线目录、参数 ——

function sealedKit(dir) {
  mkdirSync(join(dir, 'build-node'), { recursive: true });
  writeFileSync(join(dir, 'build-node', 'node.exe'), 'fake node');
  writeFileSync(join(dir, 'payload.bin'), 'client-kit-bytes');
  return sealInputs(dir, [{ path: 'tool', sha256: 'a'.repeat(64) }], ROOT);
}

function signKit(dir, sealed, { lockSha256 = sealed.lockSha256 } = {}) {
  const { publicKey, privateKey } = generateKeyPairSync('ed25519');
  const release = {
    schemaVersion: 1,
    version: '0.0.1',
    platform: 'win-x64',
    lockSha256,
    manifestSha256: sealed.manifestSha256,
  };
  const bytes = JSON.stringify(release, null, 2) + '\n';
  writeFileSync(join(dir, 'client-inputs-release.json'), bytes);
  const sums = `${digest(bytes)}  client-inputs-release.json\n${sealed.manifestSha256}  windows-client.json\n`;
  writeFileSync(join(dir, 'SHA256SUMS'), sums);
  writeFileSync(
    join(dir, 'SHA256SUMS.sig'),
    sign(null, Buffer.from(sums), privateKey).toString('base64'),
  );
  writeFileSync(
    join(dir, 'release-public.pem'),
    publicKey.export({ type: 'spki', format: 'pem' }),
  );
  return createHash('sha256')
    .update(publicKey.export({ type: 'spki', format: 'der' }))
    .digest('hex');
}

test('缓存 / 套件 / 均缺：缓存以写入记录为准，套件必须带独立信任并验签', async (t) => {
  const cache = temporary(t);
  const { lockSha256 } = lockIdentity(ROOT);
  assert.equal(
    clientStatus({ cacheDir: cache, sourceRoot: ROOT }).present,
    false,
  );
  await assert.rejects(
    () =>
      clientFetch({
        cacheDir: cache,
        sourceRoot: ROOT,
        acceptNewDeps: false,
        log() {},
      }),
    /acceptNewDeps=false/,
  );
  await assert.rejects(
    () =>
      clientFetch({
        cacheDir: cache,
        sourceRoot: ROOT,
        acceptNewDeps: true,
        filesSource: 'file:///D:/files',
        log() {},
      }),
    /deps\.sources\.npm/,
  );
  assert.throws(
    () => resolveDevkit({ cacheDir: cache, sourceRoot: ROOT, log() {} }),
    /缺少客户端套件/,
  );

  // 套件：无指纹拒绝；未签名拒绝；指纹不符拒绝；签名正确且指纹匹配才命中。
  const kitRoot = temporary(t);
  const kitDir = join(kitRoot, `auriga-client-windows-${lockSha256}`);
  mkdirSync(kitDir, { recursive: true });
  const sealed = sealedKit(kitDir);
  assert.throws(() => findClientKit(cache, lockSha256, [kitRoot]), /独立信任/);
  assert.throws(() =>
    findClientKit(cache, lockSha256, [
      { path: kitRoot, trustFingerprint: 'c'.repeat(64) },
    ]),
  );
  const fp = signKit(kitDir, sealed);
  assert.throws(
    () =>
      findClientKit(cache, lockSha256, [
        { path: kitRoot, trustFingerprint: 'c'.repeat(64) },
      ]),
    /指纹不匹配/,
  );
  const kitHit = findClientKit(cache, lockSha256, [
    { path: kitRoot, trustFingerprint: `SHA256:${fp}` },
  ]);
  assert.equal(kitHit.source, 'kit');
  assert.equal(kitHit.signed, true);
  assert.equal(kitHit.devkitSha256, sealed.manifestSha256);
  const resolved = resolveDevkit({
    cacheDir: cache,
    sourceRoot: ROOT,
    kits: [{ path: kitRoot, trustFingerprint: fp }],
    log() {},
  });
  assert.equal(resolved.devkitSha256, sealed.manifestSha256);
  // 篡改套件清单：签名元数据不再绑定。
  const manifestPath = join(kitDir, 'windows-client.json');
  const original = readFileSync(manifestPath);
  writeFileSync(manifestPath, original.toString().replace('"tool"', '"tool2"'));
  assert.throws(
    () =>
      findClientKit(cache, lockSha256, [
        { path: kitRoot, trustFingerprint: fp },
      ]),
    /签名元数据不符/,
  );
  writeFileSync(manifestPath, original);

  // 缓存：导入后命中优先于套件，devkitSha256 来自写入记录；篡改清单或缺记录均拒绝。
  const imported = clientImport({
    cacheDir: cache,
    from: kitDir,
    sourceRoot: ROOT,
    trustFingerprint: `SHA256:${fp}`,
    log() {},
  });
  const entry = clientCacheEntry(cache, lockSha256);
  assert.equal(imported.location, entry);
  assert.equal(existsSync(join(cache, '_client-import-staging')), false);
  const record = JSON.parse(
    readFileSync(join(entry, 'site-dependencies.json'), 'utf8'),
  );
  assert.equal(record.manifestSha256, sealed.manifestSha256);
  assert.equal(record.signed, true);
  const cacheHit = findClientKit(cache, lockSha256, [
    { path: kitRoot, trustFingerprint: fp },
  ]);
  assert.equal(cacheHit.source, 'cache');
  assert.equal(cacheHit.devkitSha256, sealed.manifestSha256);
  const fetched = await clientFetch({
    cacheDir: cache,
    sourceRoot: ROOT,
    acceptNewDeps: false,
    log() {},
  });
  assert.equal(fetched.action, 'hit');
  writeFileSync(
    join(entry, 'windows-client.json'),
    original.toString().replace('"tool"', '"tool3"'),
  );
  assert.throws(() => findClientKit(cache, lockSha256), /写入时记录的摘要不符/);
  writeFileSync(join(entry, 'windows-client.json'), original);
  rmSync(join(entry, 'site-dependencies.json'));
  assert.throws(
    () => findClientKit(cache, lockSha256),
    /缺少 site-dependencies\.json/,
  );
  assert.equal(CLIENT_ECO, 'client');
});

test('import：副本校验失败不留半成品；未签名套件按清单自洽导入；不能从缓存内导入', (t) => {
  const cache = temporary(t);
  const { lockSha256 } = lockIdentity(ROOT);
  const srcRoot = temporary(t);
  const src = join(srcRoot, 'kit');
  mkdirSync(src);
  const sealed = sealedKit(src);
  writeFileSync(join(src, 'payload.bin'), 'tampered!!!!!!!!');
  assert.throws(
    () =>
      clientImport({ cacheDir: cache, from: src, sourceRoot: ROOT, log() {} }),
    /离线输入摘要不符/,
  );
  assert.equal(existsSync(clientCacheEntry(cache, lockSha256)), false);
  assert.equal(existsSync(join(cache, '_client-import-staging')), false);
  writeFileSync(join(src, 'payload.bin'), 'client-kit-bytes');
  assert.throws(
    () =>
      clientImport({
        cacheDir: cache,
        from: src,
        sourceRoot: ROOT,
        trustFingerprint: 'd'.repeat(64),
        log() {},
      }),
    /没有发行签名/,
  );
  clientImport({ cacheDir: cache, from: src, sourceRoot: ROOT, log() {} });
  const hit = findClientKit(cache, lockSha256);
  assert.equal(hit.devkitSha256, sealed.manifestSha256);
  assert.throws(
    () =>
      clientImport({
        cacheDir: cache,
        from: clientCacheEntry(cache, lockSha256),
        sourceRoot: ROOT,
        log() {},
      }),
    /缓存目录自身/,
  );
});

test('files 源：锁定 URL 百分号解码（%2B）且拒绝编码穿越、反斜杠与冒号', () => {
  const locked =
    'https://github.com/astral-sh/python-build-standalone/releases/download/20260901/cpython-3.12.14%2B20260901-x86_64-pc-windows-msvc-install_only.tar.gz';
  const [full, base] = mapLockedUrlCandidates(locked, 'file:///D:/files');
  assert.match(full, /cpython-3\.12\.14\+20260901/);
  assert.match(base, /\/files\/cpython-3\.12\.14\+20260901-/);
  const [https] = mapLockedUrlCandidates(locked, 'https://files.example/in');
  assert.match(https, /cpython-3\.12\.14%2B20260901/);
  for (const bad of [
    'https://x.example/a%2f..%2fb',
    'https://x.example/a%5c..%5cb',
    'https://x.example/a%3Ab',
    'https://x.example/a/%00',
    'https://x.example/a/b.',
    'http://x.example/a',
  ])
    assert.throws(
      () => mapLockedUrlCandidates(bad, 'file:///D:/files'),
      /不安全|只支持|编码/,
    );
  assert.throws(
    () => mapLockedUrlCandidates(locked, 'https://u:p@files.example'),
    /凭据/,
  );
  assert.throws(
    () => mapLockedUrlCandidates(locked, 'http://files.example'),
    /只支持 https 或 file/,
  );
});

test('verifyInputs：toolsBaseline 指向未登记基线的缓存目录时按清单核对（不把目录当文件读）', (t) => {
  if (process.platform !== 'win32') {
    t.skip('需要 Windows 采集系统工具');
    return;
  }
  const dir = temporary(t);
  writeFileSync(join(dir, 'dependency'), 'pinned bytes');
  const manifest = sealInputs(dir, systemTools());
  const emptyCache = temporary(t);
  const ok = verifyInputs(dir, manifest.manifestSha256, ROOT, {
    toolsBaseline: emptyCache,
  });
  assert.equal(ok.toolsMatch.source, 'manifest');
});

test('site-deps 参数：--kit-fingerprint 跟随 --kit，未知参数报错', () => {
  const args = parseArgs([
    'status',
    '--cache',
    'C:\\c',
    '--kit',
    'D:\\k1',
    '--kit-fingerprint',
    'a'.repeat(64),
    '--kit',
    'D:\\k2',
    '--json',
  ]);
  assert.deepEqual(args.kit, [
    { path: 'D:\\k1', trustFingerprint: 'a'.repeat(64) },
    { path: 'D:\\k2', trustFingerprint: null },
  ]);
  assert.equal(args.json, true);
  assert.throws(() => parseArgs(['status', '--skip-verify', 'x']), /未知参数/);
  assert.throws(
    () => parseArgs(['status', '--kit-fingerprint', 'a'.repeat(64)]),
    /紧跟/,
  );
});

test('build-node.json 钉死 win-x64 zip SHA-256，缺字段或非法摘要拒绝', (t) => {
  assert.match(BUILD_NODE, /^\d+\.\d+\.\d+$/);
  assert.match(BUILD_NODE_ZIP_SHA256, HASH);
  const pin = readBuildNodePin(ROOT);
  assert.equal(pin.version, BUILD_NODE);
  assert.equal(pin.winX64ZipSha256, BUILD_NODE_ZIP_SHA256);
  assert.equal(pin.archiveSha256, BUILD_NODE_ZIP_SHA256);
  assert.ok(pin.sources.length >= 2, '须记录至少两个独立核对来源');
  const linux = readBuildNodePin(ROOT, 'linux-x64');
  assert.equal(linux.version, BUILD_NODE);
  assert.match(linux.linuxX64TarSha256, HASH);
  assert.equal(linux.archiveSha256, linux.linuxX64TarSha256);
  assert.ok(linux.sources.length >= 2);
  const dir = temporary(t);
  const path = join(dir, 'scripts/client-release/build-node.json');
  mkdirSync(join(dir, 'scripts/client-release'), { recursive: true });
  writeFileSync(path, JSON.stringify({ version: '22.19.0' }));
  assert.throws(() => readBuildNodePin(dir), /winX64ZipSha256/);
  assert.throws(() => readBuildNodePin(dir, 'linux-x64'), /linuxX64TarSha256/);
  writeFileSync(
    path,
    JSON.stringify({ version: '22.19.0', winX64ZipSha256: 'ZZ' }),
  );
  assert.throws(() => readBuildNodePin(dir), /winX64ZipSha256/);
  writeFileSync(
    path,
    JSON.stringify({
      version: '22.19.0',
      winX64ZipSha256:
        'ea3fad0e67a991d8477d8c01344b56e69c676ccb733f065b22436994b1253f86',
    }),
  );
  assert.equal(readBuildNodePin(dir).winX64ZipSha256, BUILD_NODE_ZIP_SHA256);
});

test('build：--site-kit 必须带 --devkit-sha256；收据记录基线依据', () => {
  assert.throws(
    () => validateBuildOptions({ offline: true, 'site-kit': true }),
    /--devkit-sha256/,
  );
  assert.throws(
    () =>
      validateBuildOptions({
        online: true,
        'site-kit': true,
        'verify-key': 'x',
        'site-devkit': 'y',
      }),
    /--site-kit 只能与 --offline/,
  );
  assert.equal(systemToolsEvidence(undefined), null);
  assert.deepEqual(systemToolsEvidence({ source: 'manifest' }), {
    source: 'manifest',
  });
  const tools = [{ path: 'System32/tar.exe', sha256: 'a'.repeat(64) }];
  const ev = systemToolsEvidence({
    source: 'baseline',
    baseline: { windowsVersion: '10.0.22631', registeredAt: 'T', tools },
  });
  assert.equal(ev.source, 'baseline');
  assert.equal(ev.windowsVersion, '10.0.22631');
  assert.match(ev.toolsSha256, /^[a-f0-9]{64}$/);
});
