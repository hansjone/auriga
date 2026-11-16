/** Source provenance compatible with deploy/offline/source_archive.go. */
import { Buffer, isUtf8 } from 'node:buffer';
import { existsSync, readFileSync, readdirSync, lstatSync } from 'node:fs';
import { join, basename, extname } from 'node:path';
import { spawnSync } from 'node:child_process';
import { ROOT, digest, physical } from './inputs.mjs';
import { verifySignedInventory } from './trust.mjs';
import { canonical } from '../../clients/installer/runtime/release.mjs';

const HEX = /^[a-f0-9]{64}$/;
const EXCLUDED = new Set([
  '.git',
  '.agents',
  '.agent-forgejo',
  '.idea',
  '.vscode',
  'node_modules',
  'target',
  'dist',
  '.pnpm-store',
  '.offline-releases',
  '.offline-devkit-cache',
  '__pycache__',
  '.dev',
]);
const METADATA = new Set([
  'auriga-source.json',
  'sha256sums',
  'sha256sums.sig',
  'release-public.pem',
  '.auriga-devkit.json',
  '.agent-task.md',
  '.agent-report.md',
]);
export function sourcePathAllowed(name) {
  if (
    typeof name !== 'string' ||
    !name ||
    name.startsWith('/') ||
    /[\\:]/u.test(name) ||
    [...name].some((c) => c.charCodeAt(0) < 32) ||
    !name.isWellFormed()
  )
    return false;
  const parts = name.split('/');
  if (
    parts.some(
      (p) =>
        !p ||
        p === '.' ||
        p === '..' ||
        /[<>"|?*]/.test(p) ||
        /[. ]$/.test(p) ||
        /^(CON|PRN|AUX|NUL|COM[1-9¹²³]|LPT[1-9¹²³])(?:\.|$)/i.test(p),
    )
  )
    return false;
  const lower = name.toLowerCase(),
    base = basename(lower);
  if (
    parts.some((p) => EXCLUDED.has(p.toLowerCase())) ||
    /^(bin|data)(\/|$)/.test(lower) ||
    lower.startsWith('deploy/compose/var/')
  )
    return false;
  if (
    !name.includes('/') &&
    /^(auriga-offline-|auriga-devkit-|auriga-changes-)/.test(base) &&
    (base.includes('.tar') || base.endsWith('.sha256'))
  )
    return false;
  return (
    !METADATA.has(base) &&
    !(
      base === '.env' ||
      (base.startsWith('.env.') && base !== '.env.example')
    ) &&
    !/\.(pem|key|out|test|exe|tsbuildinfo)$/.test(base)
  );
}
function textFile(name) {
  const base = basename(name);
  return (
    [
      'auriga',
      'mvnw',
      'go.mod',
      'go.sum',
      'go.work',
      'go.work.sum',
      '.npmrc',
      '.gitattributes',
      '.gitignore',
    ].includes(base) ||
    base.startsWith('Dockerfile') ||
    /\.(sh|go|mod|sum|yaml|yml|json|xml|properties|md|txt|vue|ts|js|mjs|cjs|tsx|jsx|css|html|toml|config|lock|ps1|cmd|bat)$/.test(
      extname(name).toLowerCase(),
    )
  );
}
function sourceDigest(root, name, expected) {
  let raw = readFileSync(join(root, name));
  if (!textFile(name)) return digest(raw);
  if (!isUtf8(raw) || raw.includes(0))
    throw new Error(`源码文本必须为 UTF-8：${name}`);
  let text = raw.toString('utf8').replaceAll('\r\n', '\n');
  if (name.endsWith('.sh') || ['auriga', 'mvnw'].includes(basename(name)))
    text = text.replace(/^\ufeff/, '');
  if (text.includes('\r')) throw new Error(`源码不支持单独CR：${name}`);
  if (expected && digest(text.replaceAll('\n', '\r\n')) === expected)
    return expected;
  return digest(text);
}
const sorted = (names) =>
  names.sort((a, b) => Buffer.compare(Buffer.from(a), Buffer.from(b)));
// Only a fresh source tree may supply the initial build inputs. Generated
// outputs are excluded from provenance, so reject them before pnpm/build runs.
export function assertBuildSourceClean(root, baselinePaths = []) {
  physical(root);
  const baselineUI = baselinePaths.some((p) =>
    p.startsWith('clients/dsh-plugin/ui/'),
  );
  const walk = (prefix = '') => {
    for (const entry of readdirSync(join(root, prefix), {
      withFileTypes: true,
    })) {
      const name = prefix ? `${prefix}/${entry.name}` : entry.name;
      const lower = entry.name.toLowerCase();
      if (
        ['node_modules', 'dist', '.pnpm-store'].includes(lower) ||
        (!baselineUI && name.toLowerCase() === 'clients/dsh-plugin/ui')
      ) {
        const path = join(root, name).replaceAll("'", "''");
        throw new Error(
          `构建前发现预置生成内容：${name}；请核对后在 PowerShell 清理：Remove-Item -LiteralPath '${path}' -Recurse -Force；或重新解压源码到全新目录`,
        );
      }
      if (!sourcePathAllowed(name)) continue;
      const stat = lstatSync(join(root, name));
      if (stat.isSymbolicLink()) throw new Error(`源码含链接：${name}`);
      if (stat.isDirectory()) walk(name);
    }
  };
  walk();
}
function treeDigest(files) {
  return digest(
    sorted(Object.keys(files))
      .map(
        (name) =>
          `${files[name].mode.toString(8)} ${files[name].sha256} ${name}\n`,
      )
      .join(''),
  );
}
function checkPaths(names) {
  const seen = new Map();
  for (const name of names) {
    if (!sourcePathAllowed(name)) throw new Error(`非法源码路径：${name}`);
    const parts = name.split('/');
    for (let i = 1; i <= parts.length; i++) {
      const prefix = parts.slice(0, i).join('/'),
        fold = prefix.toLowerCase().toUpperCase();
      if (seen.has(fold) && seen.get(fold) !== prefix)
        throw new Error('源码大小写路径冲突');
      seen.set(fold, prefix);
    }
  }
}
export function snapshotInventory(root, baseline = {}) {
  const files = Object.create(null);
  // package-ui writes this generated tree in place. Official Git source
  // archives omit it; retain it only when the signed baseline contains it.
  const uiBaseline = Object.keys(baseline).some((p) =>
    p.startsWith('clients/dsh-plugin/ui/'),
  );
  const walk = (prefix = '') => {
    for (const entry of readdirSync(join(root, prefix), {
      withFileTypes: true,
    })) {
      const name = prefix ? `${prefix}/${entry.name}` : entry.name;
      if (!uiBaseline && name === 'clients/dsh-plugin/ui') continue;
      if (!sourcePathAllowed(name)) continue;
      const stat = lstatSync(join(root, name));
      if (stat.isSymbolicLink()) throw new Error(`源码含链接：${name}`);
      if (stat.isDirectory()) walk(name);
      else if (stat.isFile())
        files[name] = {
          sha256: sourceDigest(root, name, baseline[name]?.sha256),
          mode:
            baseline[name]?.mode ??
            (stat.mode & 0o111 ||
            name.endsWith('.sh') ||
            ['auriga', 'mvnw'].includes(basename(name))
              ? 0o755
              : 0o644),
        };
      else throw new Error(`源码含特殊文件：${name}`);
    }
  };
  walk();
  checkPaths(Object.keys(files));
  return { files, sha256: treeDigest(files) };
}
export function sourceIdentity(
  root = ROOT,
  trust = {},
  { forBuild = false } = {},
) {
  physical(root);
  if (existsSync(join(root, 'auriga-source.json'))) {
    const approved = verifySignedInventory(root, trust);
    const m = approved.verifyMetadata('auriga-source.json');
    if (
      m.schemaVersion !== 1 ||
      !/^[a-f0-9]{40}$/.test(m.source?.commit ?? '') ||
      !/^[a-f0-9]{40}$/.test(m.tree ?? '') ||
      !HEX.test(m.source?.snapshotSha256 ?? '') ||
      !HEX.test(m.locks?.digest ?? '') ||
      !/^[a-zA-Z0-9][a-zA-Z0-9._-]{0,95}$/.test(
        m.source?.upstreamVersion ?? '',
      ) ||
      typeof m.source?.siteChanges !== 'boolean' ||
      typeof m.source?.dirty !== 'boolean' ||
      !m.files ||
      Array.isArray(m.files) ||
      !Object.keys(m.files).length
    )
      throw new Error('签名源码清单无效');
    checkPaths(Object.keys(m.files));
    for (const file of Object.values(m.files))
      if (!HEX.test(file?.sha256 ?? '') || ![0o644, 0o755].includes(file.mode))
        throw new Error('签名源码文件摘要/模式无效');
    if (treeDigest(m.files) !== m.source.snapshotSha256)
      throw new Error('签名源码快照摘要不匹配');
    if (forBuild) assertBuildSourceClean(root, Object.keys(m.files));
    const current = snapshotInventory(root, m.files);
    const changed = current.sha256 !== m.source.snapshotSha256;
    return {
      sourceType: 'signed-archive',
      sourceCommit: m.source.commit,
      sourceTreeSha256: current.sha256,
      sourceBaselineSha256: m.source.snapshotSha256,
      sourceManifestSha256: digest(
        readFileSync(join(root, 'auriga-source.json')),
      ),
      sourceIssuerFingerprint: approved.fingerprint,
      upstreamVersion: m.source.upstreamVersion,
      dirty: changed || m.source.dirty,
      siteChanges: changed || m.source.siteChanges,
    };
  }
  if (!existsSync(join(root, '.git')))
    throw new Error('缺少已签 auriga-source.json 或 Git 源码来源');
  const git = (args) => {
    const r = spawnSync('git', args, {
      cwd: root,
      encoding: 'utf8',
      windowsHide: true,
    });
    if (r.status !== 0) throw new Error('Git 源码来源不可用');
    return r.stdout;
  };
  const paths = [
    ...new Set(
      git(['ls-files', '--cached', '--others', '--exclude-standard', '-z'])
        .split('\0')
        .filter(Boolean),
    ),
  ].sort();
  if (forBuild) {
    const tracked = git(['ls-files', '--cached', '-z'])
      .split('\0')
      .filter(Boolean);
    assertBuildSourceClean(root, tracked);
  }
  const files = paths.map((p) => [
    p,
    existsSync(join(root, p)) ? digest(readFileSync(join(root, p))) : null,
  ]);
  const dirty = git(['status', '--porcelain', '-z']).length > 0;
  return {
    sourceType: 'git',
    sourceCommit: git(['rev-parse', 'HEAD']).trim(),
    sourceTreeSha256: digest(canonical(files)),
    dirty,
    siteChanges: dirty,
  };
}
