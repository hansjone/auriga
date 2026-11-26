/**
 * auriga-client build --online：在发行签名套件的基础上，联网只补“变化的依赖”，
 * 派生出现场套件，再走与 --offline 完全相同的断网构建。
 *
 * 范围（一期）：前端/workspace 的 pnpm 依赖与 dsh-plugin 的 npm 依赖。
 * Node/Python/DSH/Java 等版本、.npmrc、packageManager 的变化仍须维护方出新套件。
 * 服务端同类功能见 deploy/offline/online.go（bundle --build --online）。
 * 联网步骤全部经可注入的执行器，单元测试不真联网。
 */
import { Buffer } from 'node:buffer';
import { spawnSync } from 'node:child_process';
import {
  cpSync,
  existsSync,
  mkdirSync,
  readFileSync,
  writeFileSync,
} from 'node:fs';
import { basename, dirname, join, resolve } from 'node:path';
import {
  ROOT,
  dependencyLocks,
  digest,
  inventory,
  lockIdentity,
  physical,
  sealInputs,
  verifyInputs,
} from './inputs.mjs';
import { sourceIdentity } from './source.mjs';
import { toolsBaselineOption } from './baseline.mjs';
import {
  launchOfflineBuild,
  resolveBuildPaths,
  startOfflineBuild,
  validateBuildOptions,
} from './build.mjs';
import {
  fetchPnpmStore,
  installPluginDeps,
  run,
  stageWorkspaceLocks,
} from './prepare.mjs';
import { cleanupAfterError, removeDirSafely } from './cleanup.mjs';

/** 派生现场套件时不复制：它们属于基础套件的发行签名，派生套件不再有效。 */
export const SIGNATURE_FILES = [
  'SHA256SUMS',
  'SHA256SUMS.sig',
  'client-inputs-release.json',
  'release-public.pem',
  'windows-client.json',
];
export const PNPM_LOCK = 'web/pnpm-lock.yaml';
export const PLUGIN_LOCK = 'clients/dsh-plugin/package-lock.json';
const BUILD_NODE_FILE = 'scripts/client-release/build-node.json';
const NOT_SUPPORTED =
  '这类变化一期不支持，请联系维护方出新套件（Java/Python/DSH/Node 版本、.npmrc、packageManager、上游 pin 变化都需要维护方）';

const trustOf = (o) => ({
  verifyKey: o['verify-key'],
  trustFingerprint: o['trust-fingerprint'],
});

function pnpmRelated(path) {
  return (
    path === PNPM_LOCK ||
    path === 'pnpm-workspace.yaml' ||
    path === 'package.json' ||
    /^web\/[^/]+\/package\.json$/.test(path) ||
    /^clients\/[^/]+\/package\.json$/.test(path)
  );
}
/** 一期允许现场变化的依赖文件。 */
export function lockChangeAllowed(path) {
  return pnpmRelated(path) || path === PLUGIN_LOCK;
}

/** 基础套件里 build-pnpm 固定的 pnpm 版本，对应根 package.json 的 packageManager。 */
export function kitPackageManager(kitDir) {
  const pkg = JSON.parse(
    readFileSync(join(kitDir, 'build-pnpm/package.json'), 'utf8'),
  );
  const version = pkg.dependencies?.pnpm;
  if (typeof version !== 'string')
    throw new Error('基础套件缺少 build-pnpm 版本');
  return `pnpm@${version}`;
}
export function rootPackageManager(root) {
  return JSON.parse(readFileSync(join(root, 'package.json'), 'utf8'))
    .packageManager;
}

/**
 * 对比基础套件记录的锁与当前源码的锁。
 * 返回 { changed, unsupported, pnpm, plugin }：
 * changed 是所有变化的文件（含新增/删除），unsupported 是其中一期不支持的。
 */
export function classifyLockChanges(
  base,
  current,
  { basePackageManager, currentPackageManager } = {},
) {
  const paths = [
    ...new Set([...Object.keys(base.locks), ...Object.keys(current.locks)]),
  ].sort();
  const changed = paths.filter((p) => base.locks[p] !== current.locks[p]);
  const unsupported = changed.filter((p) => !lockChangeAllowed(p));
  if (base.buildNode !== current.buildNode) {
    changed.push(BUILD_NODE_FILE);
    unsupported.push(BUILD_NODE_FILE);
  }
  if (
    changed.includes('package.json') &&
    basePackageManager !== currentPackageManager
  )
    unsupported.push('package.json（packageManager 变化）');
  return {
    changed,
    unsupported,
    // 联网只由锁文件决定：只改 package.json（scripts、version 等）时锁不变，
    // store 与插件依赖不需要新内容，只重封清单。
    pnpm: changed.includes(PNPM_LOCK),
    plugin: changed.includes(PLUGIN_LOCK),
    pluginManifest: changed.includes('clients/dsh-plugin/package.json'),
  };
}
export function unsupportedMessage(unsupported) {
  return `${NOT_SUPPORTED}：\n${unsupported.map((p) => `  - ${p}`).join('\n')}`;
}

/** 解析 pnpm-lock.yaml（lockfileVersion 9）的 packages 段：name@version -> integrity。 */
export function parsePnpmPackages(text) {
  const lines = text.replaceAll('\r\n', '\n').split('\n');
  const version = lines
    .find((l) => l.startsWith('lockfileVersion:'))
    ?.match(/lockfileVersion:\s*['"]?(\d+)/)?.[1];
  if (version !== '9')
    throw new Error(`不支持的 pnpm-lock lockfileVersion：${version ?? '缺失'}`);
  const out = new Map();
  let inside = false;
  let key = null;
  for (const line of lines) {
    if (/^\S/.test(line)) {
      inside = line.trimEnd() === 'packages:';
      key = null;
      continue;
    }
    if (!inside) continue;
    // 包键缩进恰好 2 格；4/6 格的 peerDependencies 等嵌套键不是包。
    const entry = line.match(/^ {2}(?! )(['"]?)(.+?)\1:\s*$/);
    if (entry) {
      key = entry[2];
      out.set(key, null);
      continue;
    }
    const resolution = line.match(/^ {4}resolution:\s*\{(.*)\}\s*$/);
    if (resolution && key) {
      const integrity = resolution[1].match(/integrity:\s*([^,\s}]+)/)?.[1];
      if (integrity) out.set(key, integrity);
    }
  }
  return out;
}
/** 解析 package-lock.json 的 packages 段：name@version -> integrity。 */
export function parseNpmPackages(text) {
  const lock = JSON.parse(text);
  if (!lock.packages || typeof lock.packages !== 'object')
    throw new Error('package-lock 缺少 packages 段');
  const out = new Map();
  for (const [path, info] of Object.entries(lock.packages)) {
    if (!path || info?.link) continue;
    const name = path.slice(path.lastIndexOf('node_modules/') + 13);
    if (typeof info.version !== 'string') continue;
    out.set(`${name}@${info.version}`, info.integrity ?? null);
  }
  return out;
}
function splitKey(key) {
  const m = key.match(/^(@?[^@]+)@(.+)$/);
  return m ? { name: m[1], version: m[2] } : { name: key, version: '' };
}
/** 返回新增条目（排序）与“不再需要”的数量。 */
export function diffPackages(ecosystem, before, after) {
  const added = [...after.entries()]
    .filter(([key]) => !before.has(key))
    .map(([key, integrity]) => ({
      ecosystem,
      ...splitKey(key),
      integrity: integrity ?? null,
    }))
    .sort((a, b) =>
      `${a.name}@${a.version}`.localeCompare(`${b.name}@${b.version}`, 'en'),
    );
  return {
    added,
    removed: [...before.keys()].filter((key) => !after.has(key)).length,
  };
}

/** 文件内容是否对应清单记录的摘要（兼容 Git 检出换行差异）。 */
export function lockMatches(bytes, expected) {
  const lf = bytes.toString('utf8').replaceAll('\r\n', '\n');
  return [
    bytes,
    Buffer.from(lf),
    Buffer.from(lf.replaceAll('\n', '\r\n')),
  ].some((candidate) => digest(candidate) === expected);
}
/** 依次尝试取得“摘要与基础套件记录一致”的旧锁原文，取不到返回 null。 */
export function readBaseLock(expected, sources) {
  if (!expected) return null;
  for (const source of sources) {
    try {
      const bytes = source();
      if (bytes && lockMatches(bytes, expected)) return bytes;
    } catch {
      // 该来源不可用，继续下一个。
    }
  }
  return null;
}

export const SITE_NOTICE =
  'pnpm 依赖由 web/pnpm-lock.yaml 的 integrity 钉定，插件依赖由 package-lock.json 的 integrity 钉定；联网取得的字节只按锁内哈希校验，镜像（registry）的可信性由现场负责。签名发布前请人工审阅 added 清单。';

/** 新增依赖清单。旧锁取不到时必须明确警告，removedCount 为 null，不能显示成 0。 */
export function collectDependencyChanges({
  baseDir,
  baseLocks,
  root,
  changed,
  pnpmBaseSources = [],
}) {
  const added = [];
  const warnings = [];
  let removed = 0;
  let removedKnown = true;
  if (changed.includes(PNPM_LOCK)) {
    const before = readBaseLock(baseLocks[PNPM_LOCK], pnpmBaseSources);
    if (!before) {
      removedKnown = false;
      warnings.push(
        '取不到基础套件对应的旧 web/pnpm-lock.yaml（套件不含锁原文，也无法从源码基线的 Git 提交或 --base-pnpm-lock 取得），pnpm 新增依赖清单与“不再需要”数量不可用；请用该锁文件的修改记录人工核对新增包。可用 --base-pnpm-lock 指向原始源码包里的 web/pnpm-lock.yaml（摘要须与套件记录一致）。',
      );
    } else {
      const diff = diffPackages(
        'pnpm',
        parsePnpmPackages(before.toString('utf8')),
        parsePnpmPackages(readFileSync(join(root, PNPM_LOCK), 'utf8')),
      );
      added.push(...diff.added);
      removed += diff.removed;
    }
  }
  if (changed.includes(PLUGIN_LOCK)) {
    const old = existsSync(join(baseDir, 'plugin-deps/package-lock.json'))
      ? readFileSync(join(baseDir, 'plugin-deps/package-lock.json'))
      : null;
    const before =
      old && baseLocks[PLUGIN_LOCK] && digest(old) === baseLocks[PLUGIN_LOCK]
        ? old
        : null;
    if (!before) {
      removedKnown = false;
      warnings.push(
        '基础套件 plugin-deps 里的 package-lock.json 缺失或与清单记录不符，插件新增依赖清单与“不再需要”数量不可用。',
      );
    } else {
      const diff = diffPackages(
        'plugin',
        parseNpmPackages(before.toString('utf8')),
        parseNpmPackages(readFileSync(join(root, PLUGIN_LOCK), 'utf8')),
      );
      added.push(...diff.added);
      removed += diff.removed;
    }
  }
  const noIntegrity = added.filter((p) => !p.integrity);
  if (noIntegrity.length)
    warnings.push(
      `新增的 ${noIntegrity.length} 个包在锁里没有 integrity（tarball/git 依赖或只有 resolved），联网取得的字节没有哈希钉定，请人工核对来源：${noIntegrity
        .slice(0, 20)
        .map((p) => `${p.name}@${p.version}`)
        .join('、')}${noIntegrity.length > 20 ? ' 等' : ''}`,
    );
  return { added, removedCount: removedKnown ? removed : null, warnings };
}

/** 联网步骤的环境：去掉 npm/pnpm 配置变量，只透传已设置的代理与 registry。 */
export function onlineEnv(work, base = process.env) {
  const env = {};
  for (const [name, value] of Object.entries(base)) {
    if (
      /^(npm|pnpm)_config_/i.test(name) &&
      name.toLowerCase() !== 'npm_config_registry'
    )
      continue;
    env[name] = value;
  }
  return Object.assign(env, {
    NODE_OPTIONS: '',
    npm_config_cache: join(work, 'npm-cache'),
    npm_config_update_notifier: 'false',
  });
}

const PLUGIN_DEPENDENCY_FIELDS = [
  'dependencies',
  'optionalDependencies',
  'peerDependencies',
  'bundleDependencies',
  'bundledDependencies',
];
/**
 * 插件锁没变、只有 package.json 变化时：依赖声明必须与基础套件 plugin-deps 里的一致，
 * 否则 node_modules 是旧的，会绕过构建里“插件依赖已过期”的检查。
 */
export function assertPluginManifestCompatible(baseDir, root) {
  const before = JSON.parse(
    readFileSync(join(baseDir, 'plugin-deps/package.json'), 'utf8'),
  );
  const after = JSON.parse(
    readFileSync(join(root, 'clients/dsh-plugin/package.json'), 'utf8'),
  );
  const different = PLUGIN_DEPENDENCY_FIELDS.filter(
    (field) =>
      JSON.stringify(before[field] ?? null) !==
      JSON.stringify(after[field] ?? null),
  );
  if (different.length)
    throw new Error(
      `插件依赖声明变了（${different.join('、')}）但 clients/dsh-plugin/package-lock.json 没有更新：请先在 clients/dsh-plugin 运行 npm install --package-lock-only 更新插件锁，再重试`,
    );
}

/** 派生目录内容必须与基础套件已校验的清单逐项一致（路径、大小、SHA-256）。 */
export function assertCopyMatches(site, files) {
  const actual = new Map(inventory(site).map((f) => [f.path, f]));
  if (actual.size !== files.length)
    throw new Error('复制后的派生套件文件数与已校验的基础套件不一致');
  for (const row of files) {
    const found = actual.get(row.path);
    if (!found || found.bytes !== row.bytes || found.sha256 !== row.sha256)
      throw new Error(
        `复制后的派生套件与已校验的基础套件不一致：${row.path}（复制期间基础套件可能被改动）`,
      );
  }
}

/**
 * 派生现场套件。基础套件只读；任何失败都会清理已创建的派生目录。
 * runner(command, args, cwd, env) 默认真实执行，测试注入假实现。
 */
export function deriveSiteDevkit({
  baseDir,
  site,
  root = ROOT,
  base,
  plan,
  dependencies,
  runner = run,
  env: baseEnv = process.env,
}) {
  if (existsSync(site)) throw new Error('--site-devkit 必须是全新目录');
  // 基础套件根目录里即使有 site-dependencies.json 也不复制：派生套件要自己写一份。
  const excluded = new Set(
    [...SIGNATURE_FILES, 'site-dependencies.json'].map((n) => n.toLowerCase()),
  );
  const baseAbs = resolve(baseDir);
  let created = false;
  try {
    created = true;
    cpSync(baseAbs, site, {
      recursive: true,
      force: false,
      errorOnExist: true,
      filter: (src) =>
        !(
          dirname(resolve(src)).toLowerCase() === baseAbs.toLowerCase() &&
          excluded.has(basename(src).toLowerCase())
        ),
    });
    // 复制耗时长，期间基础套件可能被改动：联网前逐项复核复制结果与已校验清单一致。
    assertCopyMatches(site, base.files);
    const work = join(site, '_online-work');
    mkdirSync(work);
    const env = onlineEnv(work, baseEnv);
    if (plan.pnpm) {
      const workspace = join(work, 'workspace');
      stageWorkspaceLocks(root, dependencyLocks(root), workspace);
      fetchPnpmStore(site, workspace, join(site, 'pnpm-store'), env, runner);
    }
    if (plan.plugin) installPluginDeps(site, root, env, runner);
    else if (plan.pluginManifest)
      // 插件锁没变、只有 package.json 变化：不联网，只同步 plugin-deps 里的副本。
      cpSync(
        join(root, 'clients/dsh-plugin/package.json'),
        join(site, 'plugin-deps/package.json'),
        { force: true },
      );
    removeDirSafely(work, {
      within: site,
      phase: 'online-work-cleanup',
    });
    const manifest = sealInputs(site, base.tools, root);
    const report = {
      schemaVersion: 1,
      online: true,
      baseManifestSha256: base.manifestSha256,
      baseLockSha256: base.lockSha256,
      lockSha256: manifest.lockSha256,
      manifestSha256: manifest.manifestSha256,
      changedLockFiles: plan.changed,
      ecosystems: [
        plan.pnpm ? 'pnpm' : null,
        plan.plugin ? 'plugin' : null,
      ].filter(Boolean),
      added: dependencies.added,
      removedCount: dependencies.removedCount,
      warnings: dependencies.warnings,
      notice: SITE_NOTICE,
    };
    writeFileSync(
      join(site, 'site-dependencies.json'),
      JSON.stringify(report, null, 2) + '\n',
      { flag: 'wx' },
    );
    return { manifest, report };
  } catch (error) {
    // 只清理本次创建的派生目录；基础套件从未被写入。
    if (created) {
      cleanupAfterError(
        error,
        () =>
          removeDirSafely(site, {
            within: dirname(resolve(site)),
            phase: 'online-derive-rollback',
          }),
        { phase: 'online-derive-rollback', path: site },
      );
    }
    throw error;
  }
}

export function formatSiteReport(report) {
  const lines = [
    `现场联网补依赖完成：新增 ${report.added.length} 个包，不再需要 ${report.removedCount ?? '未知'} 个。`,
    `变化的锁文件：${report.changedLockFiles.join('、')}`,
  ];
  for (const item of report.added.slice(0, 200))
    lines.push(`  + [${item.ecosystem}] ${item.name}@${item.version}`);
  if (report.added.length > 200)
    lines.push(`  ……其余见输出目录 site-dependencies.json`);
  for (const warning of report.warnings) lines.push(`警告：${warning}`);
  lines.push(report.notice);
  return lines.join('\n');
}

/**
 * 联网派生并构建。io 供测试注入：root、sourceIdentity、verifyBase、verifyDerived、
 * runner、env、gitShow、launch、spawn、log。
 */
export function startOnlineBuild(options, io = {}) {
  const o = resolveBuildPaths(options);
  if (o.online !== true) throw new Error('startOnlineBuild 需要 --online');
  validateBuildOptions(o); // 参数错误先于任何联网/拷贝报出。
  const root = io.root ?? ROOT;
  const site = o['site-devkit'];
  const out = o.out;
  physical(dirname(site));
  physical(dirname(out));
  if (existsSync(site))
    throw new Error('--site-devkit 必须是全新目录，目录已存在');
  if (existsSync(out)) throw new Error('--out 必须是全新目录，目录已存在');
  const trust = trustOf(o);
  const source = (io.sourceIdentity ?? sourceIdentity)(root, trust, {
    forBuild: true,
  });
  const base = (io.verifyBase ?? verifyInputs)(o.devkit, undefined, root, {
    ...trust,
    lockPolicy: 'skip',
    toolsBaseline: toolsBaselineOption(),
  });
  // 用户显式给出的旧锁：读不到或摘要不符直接报错，不静默降级。
  let userBaseLock = null;
  if (o['base-pnpm-lock']) {
    try {
      physical(o['base-pnpm-lock']);
      userBaseLock = readFileSync(o['base-pnpm-lock']);
    } catch (error) {
      throw new Error(`无法读取 --base-pnpm-lock：${error.message}`);
    }
    if (!lockMatches(userBaseLock, base.locks[PNPM_LOCK]))
      throw new Error(
        '--base-pnpm-lock 的摘要与基础套件记录的 web/pnpm-lock.yaml 不符，请使用基础套件对应源码里的原始文件',
      );
  }
  const plan = classifyLockChanges(base, lockIdentity(root), {
    basePackageManager: kitPackageManager(base.directory),
    currentPackageManager: rootPackageManager(root),
  });
  if (plan.unsupported.length)
    throw new Error(unsupportedMessage(plan.unsupported));
  if (!plan.changed.length)
    throw new Error(
      '依赖锁与套件一致，无需联网补依赖：请直接使用 --offline（不要加 --online/--site-devkit）',
    );
  if (plan.pluginManifest && !plan.plugin)
    assertPluginManifestCompatible(base.directory, root);
  const gitShow =
    io.gitShow ??
    ((commit, path) => {
      const r = spawnSync('git', ['-C', root, 'show', `${commit}:${path}`], {
        windowsHide: true,
        maxBuffer: 256 * 1024 ** 2,
      });
      return r.status === 0 ? r.stdout : null;
    });
  const commit = /^[a-f0-9]{40}$/.test(source?.sourceCommit ?? '')
    ? source.sourceCommit
    : null;
  const dependencies = collectDependencyChanges({
    baseDir: base.directory,
    baseLocks: base.locks,
    root,
    changed: plan.changed,
    pnpmBaseSources: [
      () => userBaseLock,
      () => (commit ? gitShow(commit, PNPM_LOCK) : null),
    ],
  });
  const log = io.log ?? console.log;
  if (!plan.pnpm && !plan.plugin)
    log(
      `依赖锁未变，仅 package.json 变化，未联网下载（基础套件只读，派生到 ${site}）`,
    );
  else
    log(
      `依赖锁变化：${plan.changed.join('、')}；开始联网补依赖（基础套件只读，派生到 ${site}）`,
    );
  const { manifest, report } = deriveSiteDevkit({
    baseDir: base.directory,
    site,
    root,
    base,
    plan,
    dependencies,
    runner: io.runner,
    env: io.env,
  });
  // 构建期望摘要就是刚算出的派生清单摘要，不再用发行签名。
  const kit = (io.verifyDerived ?? verifyInputs)(
    site,
    manifest.manifestSha256,
    root,
    { toolsBaseline: toolsBaselineOption() },
  );
  (io.launch ?? launchOfflineBuild)(o, kit, {
    siteDependencies: join(site, 'site-dependencies.json'),
    spawn: io.spawn,
    root,
  });
  log(formatSiteReport(report));
  return {
    out,
    siteDevkit: site,
    manifestSha256: manifest.manifestSha256,
    added: report.added.length,
  };
}

/** auriga-client build 入口：按 --online / --offline 分派。 */
export function startBuild(options, io = {}) {
  if (options.online === true && options.offline === true)
    throw new Error('--online 与 --offline 互斥，只能二选一');
  if (options.online === true) return startOnlineBuild(options, io);
  return startOfflineBuild(options, io);
}
