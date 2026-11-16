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
import { spawnSync } from 'node:child_process';
import { sha256File } from '../../clients/installer/runtime/verify.mjs';
import { canonical } from '../../clients/installer/runtime/release.mjs';

export const ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '../..');
export const BUILD_NODE = '22.19.0';
export const HASH = /^[a-f0-9]{64}$/;
export const LOCK_PATHS = [
  'package.json',
  'pnpm-workspace.yaml',
  '.npmrc',
  'web/pnpm-lock.yaml',
  'clients/dsh-plugin/package.json',
  'clients/dsh-plugin/package-lock.json',
  'clients/installer/upstream.json',
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
  return {
    locks,
    lockSha256: digest(
      canonical({
        schema: 1,
        platform: 'win-x64',
        buildNode: BUILD_NODE,
        locks,
      }),
    ),
  };
}
export function physical(path) {
  const absolute = resolve(path);
  if (process.platform === 'win32') {
    // Reject UNC/device paths before lookup, and mapped drives using local
    // DriveInfo metadata before reading any share contents.
    if (!/^[A-Za-z]:\\/.test(absolute))
      throw new Error('仅支持本机固定磁盘输入，拒绝 UNC/设备路径');
    const env = Object.fromEntries(
      Object.entries(process.env).filter(
        ([name]) => name.toLowerCase() !== 'psmodulepath',
      ),
    );
    const result = spawnSync(
      join(
        process.env.SystemRoot ?? 'C:\\Windows',
        'System32/WindowsPowerShell/v1.0/powershell.exe',
      ),
      [
        '-NoProfile',
        '-NonInteractive',
        '-Command',
        `if (([IO.DriveInfo]::new('${absolute.slice(0, 3)}')).DriveType -ne [IO.DriveType]::Fixed) { exit 1 }`,
      ],
      { env, windowsHide: true, timeout: 15000 },
    );
    if (result.status !== 0)
      throw new Error('仅支持本机固定磁盘输入，拒绝映射网络盘');
  }
  const ancestors = [];
  for (let p = absolute; ; p = dirname(p)) {
    ancestors.push(p);
    if (p === dirname(p)) break;
  }
  // Check from the volume down, before following a junction into its children.
  for (const p of ancestors.reverse()) {
    if (lstatSync(p).isSymbolicLink())
      throw new Error(`拒绝间接输入路径：${p}`);
  }
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
    else if (path !== 'windows-client.json')
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
    platform: 'win-x64',
    buildNode: BUILD_NODE,
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
  { checkTools = true } = {},
) {
  physical(directory);
  const file = join(directory, 'windows-client.json');
  if (!HASH.test(expected ?? '') || sha256File(file) !== expected)
    throw new Error('Windows devkit 清单缺少独立批准摘要或摘要不符');
  const manifest = JSON.parse(readFileSync(file));
  const identity = lockIdentity(root);
  if (
    manifest.schema !== 1 ||
    manifest.platform !== 'win-x64' ||
    manifest.buildNode !== BUILD_NODE ||
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
  if (process.platform !== 'win32' || process.arch !== 'x64')
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
