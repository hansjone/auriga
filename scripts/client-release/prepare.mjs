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
import { sha256File } from '../../clients/installer/runtime/verify.mjs';
import {
  downloadPythonInputs,
  readPythonLock,
} from '../../clients/installer/scripts/python-runtime.mjs';

function run(command, args, cwd, env = process.env) {
  const result = spawnSync(command, args, {
    cwd,
    env,
    stdio: 'inherit',
    windowsHide: true,
  });
  if (result.status !== 0)
    throw new Error(`输入准备命令失败（${result.status}）：${command}`);
}
async function download(url, path) {
  if (existsSync(path)) throw new Error('准备输入禁止覆盖');
  // Windows curl honors the maintenance computer's HTTPS_PROXY. Node 22 fetch
  // does not automatically do so. No curl invocation exists in offline build.
  const result = spawnSync(
    'curl.exe',
    [
      '--fail',
      '--silent',
      '--show-error',
      '--location',
      '--proto',
      '=https',
      '--proto-redir',
      '=https',
      '--max-time',
      '300',
      '--output',
      path,
      url,
    ],
    { stdio: 'inherit', windowsHide: true },
  );
  if (result.status !== 0)
    throw new Error(`缺少联网准备输入：${url}（curl ${result.status}）`);
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
export async function prepareInputs(parent) {
  const tools = systemTools();
  const { lockSha256, locks } = lockIdentity();
  const kit = join(resolve(parent), `auriga-client-windows-${lockSha256}`);
  mkdirSync(kit, { recursive: false });
  const work = join(kit, '_prepare');
  mkdirSync(work);
  const runtime = JSON.parse(
    readFileSync(join(ROOT, 'clients/installer/upstream.json')),
  );
  const packageManager = JSON.parse(
    readFileSync(join(ROOT, 'package.json')),
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
      run(tar, ['-xf', zip, '-C', work], ROOT);
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
  };
  const flags = [
    '--ignore-scripts',
    '--no-audit',
    '--no-fund',
    '--os=win32',
    '--cpu=x64',
  ];
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
    join(ROOT, `clients/installer/locks/dsh-${runtime.dsh}.package-lock.json`),
    join(app, 'package-lock.json'),
  );
  const dsh = JSON.parse(readFileSync(join(app, 'package-lock.json'))).packages[
    'node_modules/@deepseek-ai/dsh'
  ];
  if (dsh.version !== runtime.dsh || dsh.integrity !== runtime.dshIntegrity)
    throw new Error('DSH pin 与锁不符');
  run(node, [npm, 'ci', '--omit=dev', ...flags], app, env);
  const plugin = join(kit, 'plugin-deps');
  mkdirSync(plugin);
  for (const file of ['package.json', 'package-lock.json'])
    cpSync(join(ROOT, 'clients/dsh-plugin', file), join(plugin, file));
  run(node, [npm, 'ci', '--omit=dev', '--omit=peer', ...flags], plugin, env);
  const workspace = join(work, 'workspace');
  mkdirSync(workspace);
  for (const path of Object.keys(locks)) {
    const dest = join(workspace, path);
    mkdirSync(dirname(dest), { recursive: true });
    cpSync(join(ROOT, path), dest);
  }
  const store = join(kit, 'pnpm-store');
  run(
    node,
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
  // Every recursive removal is a new directory inside this exclusively reserved kit.
  if (!work.startsWith(kit + '\\') && !work.startsWith(kit + '/'))
    throw new Error('非法准备工作路径');
  rmSync(work, { recursive: true, force: true });
  const manifest = sealInputs(kit, tools);
  console.log(
    JSON.stringify(
      {
        directory: kit,
        lockSha256,
        manifestSha256: manifest.manifestSha256,
        files: manifest.files.length,
        bytes: manifest.bytes,
      },
      null,
      2,
    ),
  );
  return manifest;
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
