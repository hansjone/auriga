/** Windows devkit identity. Approval of the manifest digest is independent of the kit. */
import { createHash } from 'node:crypto';
import {
  existsSync,
  lstatSync,
  readFileSync,
  readdirSync,
  statSync,
  writeFileSync,
} from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { CLIENT_TARGET, clientPlatform } from './platform.mjs';
export { physical } from './platform.mjs';
import { physical } from './platform.mjs';
import { sha256File } from '../../clients/installer/runtime/verify.mjs';
import { canonical } from '../../clients/installer/runtime/release.mjs';
import { verifySignedInventory } from './trust.mjs';
import { checkRequires, stampRequires } from './compat.mjs';
import { assertToolsAllowed, readBaseline } from './baseline.mjs';

export const ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '../..');
export const HASH = /^[a-f0-9]{64}$/;
/** 读取并校验 scripts/client-release/build-node.json（版本 + 钉死的 win-x64 zip SHA-256）。 */
export function readBuildNodePin(root = ROOT) {
  const pin = JSON.parse(
    readFileSync(join(root, 'scripts/client-release/build-node.json'), 'utf8'),
  );
  if (typeof pin.version !== 'string' || !/^\d+\.\d+\.\d+$/.test(pin.version))
    throw new Error('Windows 目标源码的构建 Node 版本无效');
  if (typeof pin.winX64ZipSha256 !== 'string' || !HASH.test(pin.winX64ZipSha256))
    throw new Error(
      'build-node.json 缺少有效的 winX64ZipSha256（须为 64 位小写十六进制）',
    );
  return {
    version: pin.version,
    winX64ZipSha256: pin.winX64ZipSha256,
    sources: Array.isArray(pin.winX64ZipSha256Sources)
      ? pin.winX64ZipSha256Sources
      : [],
  };
}

export const BUILD_NODE_PIN = readBuildNodePin(ROOT);
export const BUILD_NODE = BUILD_NODE_PIN.version;
export const BUILD_NODE_ZIP_SHA256 = BUILD_NODE_PIN.winX64ZipSha256;
export const LOCK_PATHS = [
  'package.json',
  'pnpm-workspace.yaml',
  '.npmrc',
  'web/pnpm-lock.yaml',
  'clients/dsh-plugin/package.json',
  'clients/dsh-plugin/package-lock.json',
  'clients/installer/upstream.json',
  'clients/installer/locks/python-win-x64.json',
];
export const digest = (value) =>
  createHash('sha256').update(value).digest('hex');
export function dependencyLocks(root = ROOT) {
  const config = JSON.parse(
    readFileSync(join(root, 'clients/installer/upstream.json')),
  );
  const paths = [
    ...LOCK_PATHS,
    `clients/installer/locks/dsh-${config.dsh}.package-lock.json`,
  ];
  // Every workspace manifest participates in the pnpm frozen-lockfile check.
  for (const base of ['web', 'clients']) {
    for (const entry of readdirSync(join(root, base), {
      withFileTypes: true,
    })) {
      if (!entry.isDirectory()) continue;
      const file = `${base}/${entry.name}/package.json`;
      try {
        statSync(join(root, file));
        paths.push(file);
      } catch (error) {
        if (error.code !== 'ENOENT') throw error;
      }
    }
  }
  return Object.fromEntries(
    [...new Set(paths)].sort().map((p) => [p, sha256File(join(root, p))]),
  );
}
export function lockIdentity(root = ROOT) {
  const locks = dependencyLocks(root);
  const buildNode = readBuildNodePin(root).version;
  return {
    buildNode,
    locks,
    lockSha256: digest(
      canonical({
        schema: 1,
        platform: CLIENT_TARGET,
        buildNode,
        locks,
      }),
    ),
  };
}
export function inventory(root, prefix = '') {
  const rows = [];
  for (const entry of readdirSync(join(root, prefix), {
    withFileTypes: true,
  })) {
    const path = prefix ? `${prefix}/${entry.name}` : entry.name;
    const info = lstatSync(join(root, path));
    if (info.isSymbolicLink() || (!info.isFile() && !info.isDirectory()))
      throw new Error(`拒绝特殊输入：${path}`);
    if (info.isDirectory()) rows.push(...inventory(root, path));
    else if (
      ![
        'windows-client.json',
        'client-inputs-release.json',
        'SHA256SUMS',
        'SHA256SUMS.sig',
        'release-public.pem',
        // 现场联网派生套件的审阅清单：含派生后的清单摘要，只能在封存之后写入。
        'site-dependencies.json',
        // 可写缓存条目元数据（deps fetch/import），不参与套件内容摘要。
        'auriga-origin.json',
      ].includes(path)
    )
      rows.push({
        path,
        bytes: info.size,
        sha256: sha256File(join(root, path)),
      });
  }
  return rows.sort((a, b) => a.path.localeCompare(b.path, 'en'));
}
export function sealInputs(directory, tools, root = ROOT) {
  const identity = lockIdentity(root);
  const files = inventory(directory);
  const manifest = stampRequires('windows-client.json', {
    schema: 1,
    platform: CLIENT_TARGET,
    ...identity,
    tools,
    files,
    bytes: files.reduce((n, f) => n + f.bytes, 0),
  });
  const bytes = JSON.stringify(manifest, null, 2) + '\n';
  writeFileSync(join(directory, 'windows-client.json'), bytes, { flag: 'wx' });
  return { ...manifest, manifestSha256: digest(bytes) };
}
const LOCK_HINT = (message) =>
  `${message}。仅前端 pnpm / dsh-plugin npm 依赖变化时，可改用 build --online 联网补依赖；其他变化需维护方出新套件`;
export function verifyInputs(
  directory,
  expected,
  root = ROOT,
  {
    checkTools = true,
    verifyKey,
    trustFingerprint,
    // 'skip'：联网派生的基础套件。只跳过“依赖锁必须与当前源码一致”，
    // 其余签名、摘要、逐文件和系统工具校验不变；锁与构建 Node 由调用方比对。
    lockPolicy = 'strict',
    // 现场系统工具基线：对象、基线 JSON 路径，或可写缓存目录（内含 client-tools-baseline.json）。
    // 只放宽系统工具比对，绝不绕过非工具输入摘要。
    toolsBaseline,
  } = {},
) {
  if (lockPolicy !== 'strict' && lockPolicy !== 'skip')
    throw new Error('lockPolicy 无效');
  if (lockPolicy === 'skip' && !verifyKey && !trustFingerprint)
    throw new Error('跳过依赖锁一致性的前提是套件经过发行签名校验');
  physical(directory);
  const file = join(directory, 'windows-client.json');
  const identity = lockIdentity(root);
  let signedLockSha256;
  if (verifyKey || trustFingerprint) {
    const signed = verifySignedInventory(directory, {
      verifyKey,
      trustFingerprint,
    });
    const release = signed.verifyMetadata('client-inputs-release.json');
    checkRequires('client-inputs-release.json', release);
    if (
      release.schemaVersion !== 1 ||
      release.platform !== CLIENT_TARGET ||
      !HASH.test(release.manifestSha256 ?? '') ||
      !HASH.test(release.lockSha256 ?? '')
    )
      throw new Error('客户端输入发行元数据无效');
    if (expected && expected !== release.manifestSha256)
      throw new Error('批准摘要与签名输入发行不符');
    expected = release.manifestSha256;
    signedLockSha256 = release.lockSha256;
    if (lockPolicy === 'strict' && release.lockSha256 !== identity.lockSha256)
      throw new Error(LOCK_HINT('签名输入发行依赖锁不匹配'));
  }
  if (!lstatSync(file).isFile()) throw new Error('离线清单必须为普通文件');
  const manifestBytes = readFileSync(file);
  if (!HASH.test(expected ?? '') || digest(manifestBytes) !== expected)
    throw new Error('Windows devkit 清单缺少独立批准摘要或摘要不符');
  const manifest = JSON.parse(manifestBytes);
  checkRequires('windows-client.json', manifest);
  if (lockPolicy === 'skip') {
    if (manifest.schema !== 1 || manifest.platform !== CLIENT_TARGET)
      throw new Error('Windows devkit 清单格式无效');
    // 基础套件自洽：清单记录的锁必须与其 lockSha256 对应，再与签名元数据一致。
    const own = digest(
      canonical({
        schema: 1,
        platform: CLIENT_TARGET,
        buildNode: manifest.buildNode,
        locks: manifest.locks,
      }),
    );
    if (
      typeof manifest.buildNode !== 'string' ||
      !manifest.locks ||
      typeof manifest.locks !== 'object' ||
      manifest.lockSha256 !== own ||
      manifest.lockSha256 !== signedLockSha256 ||
      Object.values(manifest.locks).some((v) => !HASH.test(v))
    )
      throw new Error('Windows devkit 清单记录的依赖锁与其摘要不符');
  } else if (
    manifest.schema !== 1 ||
    manifest.platform !== CLIENT_TARGET ||
    manifest.buildNode !== identity.buildNode ||
    manifest.lockSha256 !== identity.lockSha256 ||
    canonical(manifest.locks) !== canonical(identity.locks)
  )
    throw new Error(
      LOCK_HINT('Windows devkit 依赖锁不匹配，请准备对应锁摘要套件'),
    );
  if (!Array.isArray(manifest.files) || !manifest.files.length)
    throw new Error('Windows devkit 输入清单为空');
  const actual = inventory(directory);
  const indexed = new Map(actual.map((f) => [f.path, f]));
  const seen = new Set();
  for (const row of manifest.files) {
    if (
      typeof row.path !== 'string' ||
      /[\\:<>"|?*]/.test(row.path) ||
      [...row.path].some((character) => character.charCodeAt(0) < 32) ||
      row.path.startsWith('/') ||
      row.path
        .split('/')
        .some((part) => !part || part === '.' || part === '..') ||
      seen.has(row.path) ||
      !HASH.test(row.sha256) ||
      !Number.isSafeInteger(row.bytes) ||
      row.bytes < 0
    )
      throw new Error('非法或重复 devkit 输入路径');
    seen.add(row.path);
    const found = indexed.get(row.path);
    if (!found) throw new Error(`缺少离线输入：${row.path}`);
    if (found.sha256 !== row.sha256 || found.bytes !== row.bytes)
      throw new Error(`离线输入摘要不符：${row.path}`);
  }
  for (const row of actual)
    if (!seen.has(row.path)) throw new Error(`未登记的离线输入：${row.path}`);
  if (manifest.bytes !== actual.reduce((n, f) => n + f.bytes, 0))
    throw new Error('离线输入体积不符');
  let toolsMatch;
  if (checkTools) {
    const tools = systemTools();
    const baseline = resolveToolsBaseline(toolsBaseline);
    toolsMatch = assertToolsAllowed(tools, manifest.tools, baseline);
  }
  return {
    ...manifest,
    directory: resolve(directory),
    manifestSha256: expected,
    ...(toolsMatch ? { toolsMatch } : {}),
  };
}

function resolveToolsBaseline(value) {
  if (value == null || value === false) return null;
  if (typeof value === 'object' && Array.isArray(value.tools)) return value;
  if (typeof value === 'string') {
    const path = resolve(value);
    // 可写缓存目录：未登记基线时为 null（只按发行清单核对），不能把目录当文件读。
    if (existsSync(path) && lstatSync(path).isDirectory())
      return readBaseline(path);
    // 直接给基线文件路径
    const raw = JSON.parse(readFileSync(path, 'utf8'));
    if (
      raw?.schemaVersion !== 1 ||
      raw.platform !== CLIENT_TARGET ||
      !Array.isArray(raw.tools) ||
      !raw.tools.length
    )
      throw new Error(`系统工具基线无效：${path}`);
    return raw;
  }
  throw new Error('toolsBaseline 无效');
}
export function systemTools() {
  if (
    process.platform !== clientPlatform().os ||
    process.arch !== clientPlatform().arch
  )
    throw new Error('Windows x64 构建机必需');
  const windows = process.env.SystemRoot ?? 'C:\\Windows';
  return [
    'System32/tar.exe',
    'System32/WindowsPowerShell/v1.0/powershell.exe',
    ...[
      'csc.exe',
      'csc.exe.config',
      'mscorlib.dll',
      'System.dll',
      'System.Core.dll',
      'System.Drawing.dll',
      'System.Windows.Forms.dll',
      'System.Web.Extensions.dll',
      'System.Security.dll',
      'System.IO.Compression.dll',
      'System.IO.Compression.FileSystem.dll',
    ].map((p) => `Microsoft.NET/Framework64/v4.0.30319/${p}`),
  ].map((path) => ({ path, sha256: sha256File(join(windows, path)) }));
}
