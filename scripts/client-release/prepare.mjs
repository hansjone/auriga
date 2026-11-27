/** Connected maintainer only. Field build never invokes this module. */
import { spawnSync } from 'node:child_process';
import {
  chmodSync,
  copyFileSync,
  cpSync,
  existsSync,
  mkdirSync,
  readFileSync,
  readdirSync,
  lstatSync,
  realpathSync,
  rmdirSync,
  unlinkSync,
  writeFileSync,
} from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { removeDirSafely } from './cleanup.mjs';
import {
  ROOT,
  BUILD_NODE,
  readBuildNodePin,
  lockIdentity,
  systemTools,
  sealInputs,
} from './inputs.mjs';
import { CLIENT_TARGET, clientPlatform } from './platform.mjs';
import { createDownloader } from './files-source.mjs';
import { sha256File } from '../../clients/installer/runtime/verify.mjs';
import {
  downloadPythonInputs,
  pythonLockForTarget,
  readPythonLock,
} from '../../clients/installer/scripts/python-runtime.mjs';

export function run(command, args, cwd, env = process.env) {
  const result = spawnSync(command, args, {
    cwd,
    env,
    stdio: 'inherit',
    windowsHide: true,
  });
  if (result.status !== 0)
    throw new Error(`输入准备命令失败（${result.status}）：${command}`);
}

/** Replace package-internal symlinks with real files so kit inventory stays link-free. */
export function materializeSymlinks(root) {
  const walk = (directory) => {
    for (const entry of readdirSync(directory, { withFileTypes: true })) {
      const path = join(directory, entry.name);
      if (entry.isSymbolicLink()) {
        const target = realpathSync(path);
        const info = lstatSync(target);
        unlinkSync(path);
        if (info.isDirectory()) cpSync(target, path, { recursive: true });
        else {
          copyFileSync(target, path);
          chmodSync(path, info.mode & 0o777);
        }
        continue;
      }
      if (entry.isDirectory()) walk(path);
    }
  };
  walk(root);
}
export function removeProjectReferences(store) {
  // pnpm keeps machine-local junctions to previous virtual stores. They are
  // not package inputs and must never point out of a portable devkit.
  const version = join(store, 'v11');
  const projects = join(version, 'projects');
  for (const directory of [store, version, projects]) {
    const info = lstatSync(directory, { throwIfNoEntry: false });
    if (!info) return;
    if (!info.isDirectory())
      throw new Error(`pnpm projects 的路径包含非普通目录：${directory}`);
  }
  for (const name of readdirSync(projects)) {
    const path = join(projects, name);
    const info = lstatSync(path);
    if (info.isSymbolicLink()) {
      unlinkSync(path); // Remove the link itself, never its target.
      continue;
    }
    // Some Windows environments report pnpm project references as directories
    // rather than symbolic links. Use non-recursive rmdir: it cannot traverse
    // a target; nonempty ordinary directories remain for manual inspection.
    // Empty ordinary reference directories are also safe to remove.
    if (info.isDirectory()) {
      try {
        rmdirSync(path);
        continue;
      } catch (cause) {
        throw new Error(`无法清理 pnpm projects 条目，已保留：${path}`, {
          cause,
        });
      }
    }
    throw new Error(`无法清理 pnpm projects 条目，已保留：${path}`);
  }
}
/** npm 安装的公共参数：联网准备与现场联网派生共用，保证两处行为一致。 */
export function npmFlagsForTarget(target = CLIENT_TARGET) {
  const platform = clientPlatform(target);
  return [
    '--ignore-scripts',
    '--no-audit',
    '--no-fund',
    `--os=${platform.npmOs}`,
    `--cpu=${platform.npmCpu}`,
  ];
}
/** @deprecated Prefer npmFlagsForTarget(target); default remains win-x64. */
export const NPM_FLAGS = npmFlagsForTarget(CLIENT_TARGET);
/** 把依赖锁与各 workspace 的 package.json 摆进临时 workspace，供 pnpm fetch 使用。 */
export function stageWorkspaceLocks(root, locks, workspace) {
  mkdirSync(workspace, { recursive: true });
  for (const path of Object.keys(locks)) {
    const dest = join(workspace, path);
    mkdirSync(dirname(dest), { recursive: true });
    cpSync(join(root, path), dest);
  }
}
/**
 * 构造仅用于临时 workspace `pnpm fetch` 的隔离环境：
 * 显式关闭 symlink（PNPM_CONFIG_SYMLINK=false），避免 pnpm 在临时虚拟 store 内创建
 * 包间 NTFS Junction（导致 Windows 清理 _prepare 时出现悬空 Junction ENOTEMPTY）；
 * 仅作用于本次 fetch 子进程，不影响外部环境或离线构建时的 pnpm install。
 */
export function pnpmFetchEnv(env = process.env) {
  const next = {};
  for (const [key, value] of Object.entries(env ?? {})) {
    if (/^(pnpm|npm)_config_symlink$/i.test(key)) continue;
    next[key] = value;
  }
  next.PNPM_CONFIG_SYMLINK = 'false';
  return next;
}
/**
 * 用套件自带的 build-node 与 build-pnpm 取回 pnpm store（已有内容作为起点，只下新增），
 * 完成后去掉机器相关的项目引用。
 */
export function fetchPnpmStore(
  kit,
  workspace,
  store,
  env,
  runner = run,
  target = CLIENT_TARGET,
) {
  const platform = clientPlatform(target);
  runner(
    join(kit, platform.buildNode),
    [
      join(kit, 'build-pnpm/node_modules/pnpm/bin/pnpm.cjs'),
      'fetch',
      '--frozen-lockfile',
      '--ignore-scripts',
      '--store-dir',
      store,
    ],
    workspace,
    pnpmFetchEnv(env),
  );
  removeProjectReferences(store);
}
/** 重建 plugin-deps：旧依赖整个删除，复制源码里的 package.json/lock 后 npm ci。 */
export function installPluginDeps(
  kit,
  root,
  env,
  runner = run,
  target = CLIENT_TARGET,
) {
  const platform = clientPlatform(target);
  const plugin = join(kit, 'plugin-deps');
  removeDirSafely(plugin, {
    within: kit,
    phase: 'install-plugin-deps-reset',
  });
  mkdirSync(plugin);
  for (const file of ['package.json', 'package-lock.json'])
    cpSync(join(root, 'clients/dsh-plugin', file), join(plugin, file));
  runner(
    join(kit, platform.buildNode),
    [
      npmCliPath(kit, platform),
      'ci',
      '--omit=dev',
      '--omit=peer',
      ...npmFlagsForTarget(target),
    ],
    plugin,
    env,
  );
}

/** Official Node layout: Windows keeps npm under node_modules/; Linux under lib/. */
export function npmCliPath(kit, platform) {
  const candidates =
    platform.target === 'linux-x64'
      ? [
          'build-node/lib/node_modules/npm/bin/npm-cli.js',
          'build-node/node_modules/npm/bin/npm-cli.js',
        ]
      : [
          'build-node/node_modules/npm/bin/npm-cli.js',
          'build-node/lib/node_modules/npm/bin/npm-cli.js',
        ];
  for (const relative of candidates) {
    const path = join(kit, relative);
    if (existsSync(path)) return path;
  }
  throw new Error(`套件缺少 npm CLI（${platform.target}）`);
}
/**
 * 准备 Desktop 客户端输入套件（默认 win-x64；linux-x64 须在 Linux x64 主机上跑）。
 * options：target、filesSource、allowPublic（默认 true，兼容维护机直连公网）、
 * npmRegistry、caFile（内部依赖源私有 CA，curl --cacert 与 npm cafile）、
 * download（注入下载器，测试用）、root。
 */
export async function prepareInputs(parent, options = {}) {
  const {
    filesSource,
    allowPublic = true,
    npmRegistry,
    caFile,
    download: injectedDownload,
    root = ROOT,
    target = CLIENT_TARGET,
  } = options;
  const platform = clientPlatform(target);
  const download =
    injectedDownload ?? createDownloader({ filesSource, allowPublic, caFile });
  const tools = systemTools(target);
  if (!tools.length)
    throw new Error(`${platform.target} 构建机缺少必需的系统 tar`);
  const buildNodePin = readBuildNodePin(root, target);
  const { lockSha256, locks } = lockIdentity(root, target);
  const kit = join(resolve(parent), `${platform.kitPrefix}-${lockSha256}`);
  mkdirSync(kit, { recursive: false });
  const work = join(kit, '_prepare');
  mkdirSync(work);
  const runtime = JSON.parse(
    readFileSync(join(root, 'clients/installer/upstream.json')),
  );
  const packageManager = JSON.parse(
    readFileSync(join(root, 'package.json')),
  ).packageManager;
  const pnpmBuildVersion = packageManager.match(/^pnpm@(\d+\.\d+\.\d+)$/)?.[1];
  if (!pnpmBuildVersion) throw new Error('pnpm 必须固定精确版本');
  const tar =
    platform.target === 'linux-x64'
      ? tools[0].path
      : join(process.env.SystemRoot ?? 'C:\\Windows', 'System32/tar.exe');
  await downloadPythonInputs(
    readPythonLock(pythonLockForTarget(target)),
    kit,
    download,
  );
  mkdirSync(join(kit, 'runtime'));
  mkdirSync(join(kit, 'build-node'));
  const runtimeNodeSha = runtime[platform.nodeShaField];
  if (
    typeof runtimeNodeSha !== 'string' ||
    !/^[a-f0-9]{64}$/.test(runtimeNodeSha)
  )
    throw new Error(`upstream.json 缺少有效的 ${platform.nodeShaField}`);
  for (const version of [...new Set([BUILD_NODE, runtime.node])]) {
    const name = platform.nodeArchive(version);
    const archive = join(
      version === runtime.node ? join(kit, 'runtime') : work,
      name,
    );
    // 构建 Node：以仓库 build-node.json 目标归档摘要为准；runtime Node：以
    // upstream.json 为准。下载的 SHASUMS256.txt 仅作参考比对，不得覆盖仓库钉死摘要。
    const pinned =
      version === BUILD_NODE ? buildNodePin.archiveSha256 : runtimeNodeSha;
    const sums = join(work, `SHASUMS256-${version}.txt`);
    let listed;
    try {
      await download(
        `https://nodejs.org/dist/v${version}/SHASUMS256.txt`,
        sums,
      );
      listed = readFileSync(sums, 'utf8')
        .split('\n')
        .find((line) => line.trim().endsWith(` ${name}`))
        ?.trim()
        .split(/\s+/)[0];
    } catch (err) {
      console.warn(
        `警告：无法取得 SHASUMS256.txt（${err.message}），仍按仓库钉死摘要核归档`,
      );
    }
    if (listed && listed !== pinned)
      throw new Error(
        `Node SHASUMS256.txt 参考行与仓库钉死摘要不符：${name}（参考=${listed} 钉死=${pinned}）`,
      );
    if (listed === undefined && existsSync(sums))
      console.warn(
        `警告：SHASUMS256.txt 无 ${name} 行，仍按仓库钉死摘要核归档`,
      );
    await download(`https://nodejs.org/dist/v${version}/${name}`, archive);
    if (sha256File(archive) !== pinned)
      throw new Error(`Node 归档摘要不符：${name}（期望仓库钉死 ${pinned}）`);
    if (version === BUILD_NODE) {
      run(tar, ['-xf', archive, '-C', work], root);
      cpSync(
        join(work, platform.nodeExtractDir(version)),
        join(kit, 'build-node'),
        { recursive: true },
      );
      if (platform.target === 'linux-x64')
        materializeSymlinks(join(kit, 'build-node'));
    }
  }
  const node = join(kit, platform.buildNode);
  const npm = npmCliPath(kit, platform);
  const env = {
    ...process.env,
    NODE_OPTIONS: '',
    npm_config_cache: join(work, 'npm-cache'),
    npm_config_update_notifier: 'false',
    ...(npmRegistry ? { npm_config_registry: npmRegistry } : {}),
    ...(caFile ? { npm_config_cafile: caFile } : {}),
  };
  const flags = npmFlagsForTarget(target);
  for (const [name, version, integrity] of [
    ['build-pnpm', pnpmBuildVersion, null],
    ['payload-pnpm', runtime.pnpm, runtime.pnpmIntegrity],
  ]) {
    const dir = join(kit, name);
    mkdirSync(dir);
    writeFileSync(
      join(dir, 'package.json'),
      JSON.stringify({
        name: `auriga-${name}`,
        private: true,
        dependencies: { pnpm: version },
      }),
    );
    run(node, [npm, 'install', ...flags], dir, env);
    const lock = JSON.parse(readFileSync(join(dir, 'package-lock.json')));
    if (
      integrity &&
      lock.packages['node_modules/pnpm']?.integrity !== integrity
    )
      throw new Error('pnpm 发行完整性与仓库 pin 不符');
  }
  const app = join(kit, 'upstream-app');
  mkdirSync(app);
  writeFileSync(
    join(app, 'package.json'),
    JSON.stringify({
      name: 'auriga-upstream-dsh',
      private: true,
      dependencies: { '@deepseek-ai/dsh': runtime.dsh },
    }),
  );
  cpSync(
    join(root, `clients/installer/locks/dsh-${runtime.dsh}.package-lock.json`),
    join(app, 'package-lock.json'),
  );
  const dsh = JSON.parse(readFileSync(join(app, 'package-lock.json'))).packages[
    'node_modules/@deepseek-ai/dsh'
  ];
  if (dsh.version !== runtime.dsh || dsh.integrity !== runtime.dshIntegrity)
    throw new Error('DSH pin 与锁不符');
  run(node, [npm, 'ci', '--omit=dev', ...flags], app, env);
  installPluginDeps(kit, root, env, run, target);
  const workspace = join(work, 'workspace');
  stageWorkspaceLocks(root, locks, workspace);
  fetchPnpmStore(kit, workspace, join(kit, 'pnpm-store'), env, run, target);
  console.error('[prepare] pnpm fetch 完成，正在清理临时工作目录 _prepare…');
  // Every recursive removal is a new directory inside this exclusively reserved kit.
  removeDirSafely(work, {
    within: kit,
    phase: 'prepare-workspace-cleanup',
  });
  console.error('[prepare] 临时工作目录清理完成，正在封存套件清单…');
  // npm/.bin and other package links appear after installs; flatten before seal.
  if (platform.target === 'linux-x64') materializeSymlinks(kit);
  const manifest = sealInputs(kit, tools, root, target);
  const summary = {
    directory: kit,
    lockSha256,
    manifestSha256: manifest.manifestSha256,
    files: manifest.files.length,
    bytes: manifest.bytes,
    platform: platform.target,
  };
  console.log(JSON.stringify(summary, null, 2));
  return { ...manifest, ...summary };
}
if (resolve(process.argv[1] ?? '') === fileURLToPath(import.meta.url)) {
  try {
    const parent = process.argv[2];
    const targetArg = process.argv.find((arg) => arg.startsWith('--target='));
    const target = targetArg
      ? targetArg.slice('--target='.length)
      : CLIENT_TARGET;
    if (!parent || !existsSync(parent))
      throw new Error('请提供已存在的专用客户端套件输出父目录');
    await prepareInputs(parent, { target });
  } catch (error) {
    console.error(error.message);
    process.exitCode = 1;
  }
}
