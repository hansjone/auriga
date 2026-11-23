/** Source provenance compatible with deploy/offline/source_archive.go. */
import { Buffer, isUtf8 } from 'node:buffer';
import {
  existsSync,
  readFileSync,
  readdirSync,
  lstatSync,
  mkdirSync,
  copyFileSync,
  rmSync,
} from 'node:fs';
import { join, basename, extname, resolve } from 'node:path';
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
const ROOT_METADATA = new Set([
  'auriga-source.json',
  'sha256sums',
  'sha256sums.sig',
  'release-public.pem',
  '.auriga-devkit.json',
]);
const METADATA = new Set(['.agent-task.md', '.agent-report.md']);
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
    !(!name.includes('/') && ROOT_METADATA.has(base)) &&
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
        const ui = name.toLowerCase() === 'clients/dsh-plugin/ui';
        const quoted = resolve(root).replaceAll("'", "''");
        throw new Error(
          `构建前发现预置生成内容：${name}；构建不再向源码树写入，这多半是旧版本构建的残留。请运行一键清理（支持超长路径，只删已知生成目录）：.\\scripts\\client-release\\auriga-client.cmd clean-source '${quoted}'${ui ? ' --include-ui' : ''}；或重新解压源码到全新目录。路径：${join(root, name)}`,
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
/** 构建会生成的目录名（含历史版本在源码树里留下的残留）。 */
const GENERATED_DIRS = new Set(['node_modules', 'dist', '.pnpm-store']);
const UI_DIR = 'clients/dsh-plugin/ui';
/**
 * 清理源码树里已知的构建生成物：任意层级的 node_modules、dist、.pnpm-store，
 * 以及（指定 includeUi 时）clients/dsh-plugin/ui。不进入 .git，也不删其他任何文件；
 * 用 Node 的 fs.rmSync 删除，超长路径（如 element-plus 在 .pnpm 下）同样可删。
 * 链接只删链接本身，不跟随到目标目录。
 */
export function cleanSource(root, { includeUi = false } = {}) {
  root = resolve(root);
  physical(root);
  if (!existsSync(join(root, 'scripts/client-release/build-node.json')))
    throw new Error(
      '不是 Auriga 源码目录（缺少 scripts/client-release/build-node.json），拒绝清理',
    );
  const removed = [];
  const remove = (name) => {
    try {
      rmSync(join(root, name), {
        recursive: true,
        force: true,
        maxRetries: 3,
        retryDelay: 200,
      });
    } catch (error) {
      throw new Error(`无法删除 ${name}：${error.message}`);
    }
    removed.push(name);
  };
  if (includeUi && uiIsSource(root))
    throw new Error(
      `${UI_DIR} 属于签名源码基线或已被 Git 跟踪，是源码而不是生成物，拒绝清理；去掉 --include-ui`,
    );
  // 遍历规则与 assertBuildSourceClean 完全一致：不进入 sourcePathAllowed 为假的目录
  // （.git、bin、data、target、deploy/compose/var、.dev 等），不跟随链接。
  const walk = (prefix = '') => {
    for (const entry of readdirSync(join(root, prefix), {
      withFileTypes: true,
    })) {
      const name = prefix ? `${prefix}/${entry.name}` : entry.name;
      const lower = entry.name.toLowerCase();
      const generated =
        GENERATED_DIRS.has(lower) ||
        (includeUi && name.toLowerCase() === UI_DIR);
      if (generated) {
        if (entry.isDirectory() || entry.isSymbolicLink()) remove(name);
        continue;
      }
      if (!sourcePathAllowed(name)) continue;
      if (entry.isDirectory()) walk(name);
    }
  };
  walk();
  return { root, removed };
}
/** clients/dsh-plugin/ui 是否属于源码：签名基线含它，或 Git 正在跟踪它。 */
function uiIsSource(root) {
  const manifest = join(root, 'auriga-source.json');
  if (existsSync(manifest)) {
    try {
      const files = JSON.parse(readFileSync(manifest, 'utf8')).files ?? {};
      if (Object.keys(files).some((p) => p.startsWith(`${UI_DIR}/`)))
        return true;
    } catch {
      return true; // 清单读不懂时保守拒绝。
    }
  }
  if (existsSync(join(root, '.git'))) {
    const r = spawnSync('git', ['ls-files', '-z', '--', UI_DIR], {
      cwd: root,
      encoding: 'utf8',
      windowsHide: true,
    });
    return r.status !== 0 || r.stdout.length > 0;
  }
  return false;
}
// 签名源码包的元数据不计入身份，但副本里要有它们才能复核签名与摘要。
const SIGNED_METADATA = new Set([
  'auriga-source.json',
  'sha256sums',
  'sha256sums.sig',
  'release-public.pem',
]);
/**
 * 把源码复制到独立构建目录。文件集合与源码身份一致：只复制 sourcePathAllowed 允许的路径
 * （因此没有 node_modules、.git、.env*、*.pem/*.key/*.exe、顶层归档等），外加顶层的签名源码元数据；
 * 遇到链接/特殊文件即拒绝。副本是否与身份一致由 assertBuildCopyMatches 整体复核。
 * dest 必须是不存在的新目录；若位于源码内则自动跳过它。
 */
export function copyBuildSource(root, dest) {
  root = resolve(root);
  dest = resolve(dest);
  const key = (p) => (process.platform === 'win32' ? p.toLowerCase() : p);
  mkdirSync(dest);
  let count = 0;
  const walk = (prefix = '') => {
    mkdirSync(join(dest, prefix), { recursive: true });
    for (const entry of readdirSync(join(root, prefix), {
      withFileTypes: true,
    })) {
      const name = prefix ? `${prefix}/${entry.name}` : entry.name;
      const from = join(root, name);
      if (key(from) === key(dest)) continue;
      if (
        !sourcePathAllowed(name) &&
        !(!prefix && SIGNED_METADATA.has(entry.name.toLowerCase()))
      )
        continue;
      const stat = lstatSync(from);
      if (stat.isSymbolicLink()) throw new Error(`源码含链接：${name}`);
      if (stat.isDirectory()) walk(name);
      else if (stat.isFile()) {
        copyFileSync(from, join(dest, name));
        count++;
      } else throw new Error(`源码含特殊文件：${name}`);
    }
  };
  walk();
  return count;
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
function gitOutput(root, args) {
  const r = spawnSync('git', args, {
    cwd: root,
    encoding: 'utf8',
    windowsHide: true,
  });
  if (r.status !== 0) throw new Error('Git 源码来源不可用');
  return r.stdout;
}
function gitSourcePaths(root) {
  return [
    ...new Set(
      gitOutput(root, [
        'ls-files',
        '--cached',
        '--others',
        '--exclude-standard',
        '-z',
      ])
        .split('\0')
        .filter((p) => p && sourcePathAllowed(p)),
    ),
  ].sort();
}
/**
 * 构建副本必须与已校验的源码身份一致，否则拒绝构建（复制与校验之间源码或副本被改动）。
 * 签名源码包：副本里带着签名元数据，按同一套规则重新验签并算摘要；
 * Git 来源：副本没有 .git，按源码目录的同一份文件列表对副本逐文件算摘要
 * （身份已计入、副本里没有的文件按缺失处理，因而不一致）。
 */
export function assertBuildCopyMatches(copyRoot, sourceRoot, trust, source) {
  if (source.sourceType === 'signed-archive') {
    const actual = sourceIdentity(copyRoot, trust).sourceTreeSha256;
    if (actual !== source.sourceTreeSha256)
      throw new Error(
        '构建副本与源码身份不一致（复制后源码或副本被改动，或副本缺少身份已计入的文件），拒绝构建；请从新目录重建',
      );
    return;
  }
  const paths = gitSourcePaths(sourceRoot);
  const missing = [];
  const entries = paths.map((p) => {
    const inCopy = existsSync(join(copyRoot, p));
    if (!inCopy && existsSync(join(sourceRoot, p))) missing.push(p);
    return [p, inCopy ? digest(readFileSync(join(copyRoot, p))) : null];
  });
  const actual = digest(canonical(entries));
  if (actual !== source.sourceTreeSha256) {
    const detail = missing.length
      ? `（副本缺少文件：${missing.slice(0, 10).join('、')}${missing.length > 10 ? ` 等 ${missing.length} 项` : ''}）`
      : '';
    throw new Error(
      `构建副本与源码身份不一致（复制后源码或副本被改动，或副本缺少身份已计入的文件）${detail}，拒绝构建；请从新目录重建`,
    );
  }
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
  const git = (args) => gitOutput(root, args);
  const paths = gitSourcePaths(root);
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
