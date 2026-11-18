/** Windows devkit identity. Approval of the manifest digest is independent of the kit. */
import { createHash } from 'node:crypto';
import {
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

export const ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '../..');
function readBuildNode(root) {
  const { version } = JSON.parse(
    readFileSync(join(root, 'scripts/client-release/build-node.json'), 'utf8'),
  );
  if (typeof version !== 'string' || !/^\d+\.\d+\.\d+$/.test(version))
    throw new Error('Windows 目标源码的构建 Node 版本无效');
  return version;
}

export const BUILD_NODE = readBuildNode(ROOT);
export const HASH = /^[a-f0-9]{64}$/;
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
  const buildNode = readBuildNode(root);
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
  const manifest = {
    schema: 1,
    platform: CLIENT_TARGET,
    ...identity,
    tools,
    files,
    bytes: files.reduce((n, f) => n + f.bytes, 0),
  };
  const bytes = JSON.stringify(manifest, null, 2) + '\n';
  writeFileSync(join(directory, 'windows-client.json'), bytes, { flag: 'wx' });
  return { ...manifest, manifestSha256: digest(bytes) };
}
export function verifyInputs(
  directory,
  expected,
  root = ROOT,
  { checkTools = true, verifyKey, trustFingerprint } = {},
) {
  physical(directory);
  const file = join(directory, 'windows-client.json');
  const identity = lockIdentity(root);
  if (verifyKey || trustFingerprint) {
    const signed = verifySignedInventory(directory, {
      verifyKey,
      trustFingerprint,
    });
    const release = signed.verifyMetadata('client-inputs-release.json');
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
    if (release.lockSha256 !== identity.lockSha256)
      throw new Error('签名输入发行依赖锁不匹配');
  }
  if (!lstatSync(file).isFile()) throw new Error('离线清单必须为普通文件');
  const manifestBytes = readFileSync(file);
  if (!HASH.test(expected ?? '') || digest(manifestBytes) !== expected)
    throw new Error('Windows devkit 清单缺少独立批准摘要或摘要不符');
  const manifest = JSON.parse(manifestBytes);
  if (
    manifest.schema !== 1 ||
    manifest.platform !== CLIENT_TARGET ||
    manifest.buildNode !== identity.buildNode ||
    manifest.lockSha256 !== identity.lockSha256 ||
    canonical(manifest.locks) !== canonical(identity.locks)
  )
    throw new Error('Windows devkit 依赖锁不匹配，请准备对应锁摘要套件');
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
  if (checkTools) {
    const tools = systemTools();
    if (canonical(tools) !== canonical(manifest.tools))
      throw new Error(
        'Windows 系统工具不匹配，请在匹配的 Windows 构建机准备套件',
      );
  }
  return {
    ...manifest,
    directory: resolve(directory),
    manifestSha256: expected,
  };
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
