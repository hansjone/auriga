/** Connected maintainer only. Field build never invokes this module. */
import { spawnSync } from 'node:child_process';
import {
  cpSync,
  existsSync,
  mkdirSync,
  readFileSync,
  readdirSync,
  lstatSync,
  unlinkSync,
  rmSync,
  writeFileSync,
} from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import {
  ROOT,
  BUILD_NODE,
  lockIdentity,
  systemTools,
  sealInputs,
} from './inputs.mjs';
import { createDownloader } from './files-source.mjs';
import { sha256File } from '../../clients/installer/runtime/verify.mjs';
import {
  downloadPythonInputs,
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
export function removeProjectReferences(store) {
  // pnpm keeps machine-local junctions to previous virtual stores. They are
  // not package inputs and must never point out of a portable devkit.
  const projects = join(store, 'v11/projects');
  if (!existsSync(projects)) return;
  for (const name of readdirSync(projects)) {
    const path = join(projects, name);
    if (!lstatSync(path).isSymbolicLink())
      throw new Error('不认识的 pnpm projects 内容，保留供人工核验');
    unlinkSync(path); // Remove the junction itself, never its target.
  }
}
/** npm 安装的公共参数：联网准备与现场联网派生共用，保证两处行为一致。 */
export const NPM_FLAGS = [
  '--ignore-scripts',
  '--no-audit',
  '--no-fund',
  '--os=win32',
  '--cpu=x64',
];
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
 * 用套件自带的 build-node 与 build-pnpm 取回 pnpm store（已有内容作为起点，只下新增），
 * 完成后去掉机器相关的项目引用。
 */
export function fetchPnpmStore(kit, workspace, store, env, runner = run) {
  runner(
    join(kit, 'build-node/node.exe'),
    [
      join(kit, 'build-pnpm/node_modules/pnpm/bin/pnpm.cjs'),
      'fetch',
      '--frozen-lockfile',
      '--ignore-scripts',
      '--store-dir',
      store,
    ],
    workspace,
    env,
  );
  removeProjectReferences(store);
}
/** 重建 plugin-deps：旧依赖整个删除，复制源码里的 package.json/lock 后 npm ci。 */
export function installPluginDeps(kit, root, env, runner = run) {
  const plugin = join(kit, 'plugin-deps');
  rmSync(plugin, { recursive: true, force: true });
  mkdirSync(plugin);
  for (const file of ['package.json', 'package-lock.json'])
    cpSync(join(root, 'clients/dsh-plugin', file), join(plugin, file));
  runner(
    join(kit, 'build-node/node.exe'),
    [
      join(kit, 'build-node/node_modules/npm/bin/npm-cli.js'),
      'ci',
      '--omit=dev',
      '--omit=peer',
      ...NPM_FLAGS,
    ],
    plugin,
    env,
  );
}
/**
 * 准备 Windows 客户端输入套件。
 * options：filesSource、allowPublic（默认 true，兼容维护机直连公网）、
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
  } = options;
  const download =
    injectedDownload ?? createDownloader({ filesSource, allowPublic, caFile });
  const tools = systemTools();
  const { lockSha256, locks } = lockIdentity(root);
  const kit = join(resolve(parent), `auriga-client-windows-${lockSha256}`);
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
  const tar = join(process.env.SystemRoot ?? 'C:\\Windows', 'System32/tar.exe');
  await downloadPythonInputs(readPythonLock(), kit, download);
  mkdirSync(join(kit, 'runtime'));
  mkdirSync(join(kit, 'build-node'));
  for (const version of [...new Set([BUILD_NODE, runtime.node])]) {
    const name = `node-v${version}-win-x64.zip`;
    const zip = join(
      version === runtime.node ? join(kit, 'runtime') : work,
      name,
    );
    const sums = join(work, `SHASUMS256-${version}.txt`);
    await download(`https://nodejs.org/dist/v${version}/SHASUMS256.txt`, sums);
    const expected = readFileSync(sums, 'utf8')
      .split('\n')
      .find((line) => line.trim().endsWith(` ${name}`))
      ?.trim()
      .split(/\s+/)[0];
    if (
      !expected ||
      (version === runtime.node && expected !== runtime.nodeWinX64Sha256)
    )
      throw new Error('Node runtime 与仓库固定摘要不符');
    await download(`https://nodejs.org/dist/v${version}/${name}`, zip);
    if (sha256File(zip) !== expected)
      throw new Error(`Node zip 摘要不符：${name}`);
    if (version === BUILD_NODE) {
      run(tar, ['-xf', zip, '-C', work], root);
      cpSync(join(work, `node-v${version}-win-x64`), join(kit, 'build-node'), {
        recursive: true,
      });
    }
  }
  const node = join(kit, 'build-node/node.exe');
  const npm = join(kit, 'build-node/node_modules/npm/bin/npm-cli.js');
  const env = {
    ...process.env,
    NODE_OPTIONS: '',
    npm_config_cache: join(work, 'npm-cache'),
    npm_config_update_notifier: 'false',
    ...(npmRegistry ? { npm_config_registry: npmRegistry } : {}),
    ...(caFile ? { npm_config_cafile: caFile } : {}),
  };
  const flags = NPM_FLAGS;
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
  installPluginDeps(kit, root, env);
  const workspace = join(work, 'workspace');
  stageWorkspaceLocks(root, locks, workspace);
  fetchPnpmStore(kit, workspace, join(kit, 'pnpm-store'), env);
  // Every recursive removal is a new directory inside this exclusively reserved kit.
  if (!work.startsWith(kit + '\\') && !work.startsWith(kit + '/'))
    throw new Error('非法准备工作路径');
  rmSync(work, { recursive: true, force: true });
  const manifest = sealInputs(kit, tools, root);
  const summary = {
    directory: kit,
    lockSha256,
    manifestSha256: manifest.manifestSha256,
    files: manifest.files.length,
    bytes: manifest.bytes,
  };
  console.log(JSON.stringify(summary, null, 2));
  return { ...manifest, ...summary };
}
if (resolve(process.argv[1] ?? '') === fileURLToPath(import.meta.url)) {
  try {
    const parent = process.argv[2];
    if (!parent || !existsSync(parent))
      throw new Error('请提供已存在的专用 Windows devkit 输出父目录');
    await prepareInputs(parent);
  } catch (error) {
    console.error(error.message);
    process.exitCode = 1;
  }
}
