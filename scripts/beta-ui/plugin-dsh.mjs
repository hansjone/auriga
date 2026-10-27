/* global fetch, AbortSignal */
import fs from 'node:fs';
import path from 'node:path';
import os from 'node:os';
import { stripVTControlCharacters } from 'node:util';
import { spawn, spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import {
  privatePath,
  processIdentity,
  waitUntil,
  repo,
  windowsPowerShellEnvironment,
} from './runner.mjs';

const here = path.dirname(fileURLToPath(import.meta.url));
const origin = 'http://127.0.0.1:18080';
const inside = (parent, child) => {
  const rel = path.relative(parent, child);
  return (
    rel === '' ||
    (!rel.startsWith('..' + path.sep) && rel !== '..' && !path.isAbsolute(rel))
  );
};
const json = (file, data) =>
  fs.writeFileSync(file, JSON.stringify(data, null, 2) + '\n', { mode: 0o600 });

export function parseArguments(args) {
  const [action, ...rest] = args;
  if (!['launch', 'status', 'stop'].includes(action) || rest.length % 2)
    throw new Error(
      '用法：plugin-dsh.mjs launch|status|stop --state <绝对路径> [--dsh-package <已安装的 @deepseek-ai/dsh 目录>]',
    );
  const values = {};
  const allowed =
    action === 'launch' ? ['--state', '--dsh-package', '--port'] : ['--state'];
  for (let i = 0; i < rest.length; i += 2) {
    if (
      !allowed.includes(rest[i]) ||
      Object.hasOwn(values, rest[i]) ||
      !rest[i + 1] ||
      rest[i + 1].startsWith('--')
    )
      throw new Error('参数未知、重复或缺少值');
    values[rest[i]] = rest[i + 1];
  }
  if (!values['--state'] || !path.isAbsolute(values['--state']))
    throw new Error('StateDir 必须是绝对路径');
  // Random ports only: this acceptance host must not replace an existing service.
  if (values['--port'] !== undefined && values['--port'] !== '0')
    throw new Error('仅允许随机端口 --port 0');
  if (
    action === 'launch' &&
    (!values['--dsh-package'] || !path.isAbsolute(values['--dsh-package']))
  )
    throw new Error(
      '--dsh-package 必须指定本机已有 DSH 包的绝对目录，不自动下载或升级宿主',
    );
  return {
    action,
    state: path.resolve(values['--state']),
    dshPackage: values['--dsh-package'],
    port: 0,
  };
}

export function assertStatePath(input, source = process.env) {
  if (
    !path.isAbsolute(input) ||
    input.startsWith('\\\\') ||
    input.startsWith('//') ||
    input.includes('\0')
  )
    throw new Error('StateDir 必须是本机绝对路径');
  const state = path.resolve(input);
  if (process.platform === 'win32' && state.slice(2).includes(':'))
    throw new Error('StateDir 不接受设备流');
  const env = Object.fromEntries(
    Object.entries(source).map(([k, v]) => [k.toUpperCase(), v]),
  );
  const homes = [os.homedir(), env.HOME, env.USERPROFILE]
    .filter(Boolean)
    .map((x) => path.resolve(x));
  const protectedRoots = [
    ...homes.map((x) => path.join(x, '.dsh')),
    env.DSH_HOME,
  ]
    .filter(Boolean)
    .map((x) => path.resolve(x));
  if (
    homes.some((home) => inside(state, home)) ||
    protectedRoots.some((root) => inside(root, state) || inside(state, root)) ||
    state.split(/[\\/]/).some((part) => part.toLowerCase() === '.dsh') ||
    (inside(repo, state) && !inside(path.join(repo, '.dev/plugin-dsh'), state))
  )
    throw new Error('StateDir 与真实用户目录、DSH profile 或仓库源码重叠');
  // Inspect every existing ancestor before creating anything; no junction escape.
  for (let current = state; ; current = path.dirname(current)) {
    try {
      const stat = fs.lstatSync(current);
      if (
        !stat.isDirectory() ||
        stat.isSymbolicLink() ||
        path.resolve(fs.realpathSync(current)).toLowerCase() !==
          current.toLowerCase()
      )
        throw new Error('StateDir 祖先必须是普通目录，不能包含链接');
    } catch (error) {
      if (error.code !== 'ENOENT') throw error;
    }
    if (path.dirname(current) === current) break;
  }
  return state;
}

export function claimState(state) {
  assertStatePath(state);
  if (!fs.existsSync(path.dirname(state)))
    throw new Error('请先创建 StateDir 的父目录');
  fs.mkdirSync(state, { mode: 0o700 }); // Exclusive: never reuse even an empty directory.
}

export function isolatedEnvironment(state, source = process.env) {
  // Allowlist instead of inheriting API keys, real profile overrides, .env or Node hooks.
  const allowed =
    /^(PATH|SYSTEMROOT|WINDIR|COMSPEC|PATHEXT|SYSTEMDRIVE|PROCESSOR_ARCHITECTURE|NUMBER_OF_PROCESSORS|NODE_EXTRA_CA_CERTS|HTTPS?_PROXY|NO_PROXY)$/i;
  const env = Object.fromEntries(
    Object.entries(source).filter(([key]) => allowed.test(key)),
  );
  const p = (...parts) => path.join(state, ...parts);
  return {
    ...env,
    DSH_HOME: p('dsh'),
    HOME: p('home'),
    USERPROFILE: p('home'),
    APPDATA: p('home/roaming'),
    LOCALAPPDATA: p('home/local'),
    XDG_CONFIG_HOME: p('config'),
    XDG_DATA_HOME: p('data'),
    XDG_CACHE_HOME: p('cache'),
    TMP: p('tmp'),
    TEMP: p('tmp'),
    TMPDIR: p('tmp'),
    COREPACK_HOME: p('cache/corepack'),
    COREPACK_ENABLE_AUTO_PIN: '0',
    npm_config_cache: p('cache/npm'),
    npm_config_userconfig: p('config/npmrc'),
    npm_config_globalconfig: p('config/global-npmrc'),
    npm_config_store_dir: p('cache/pnpm'),
    PNPM_HOME: p('cache/pnpm-home'),
    CI: 'true',
    AURIGA_MODE: 'connect',
  };
}

export function connectionPatch(providerId) {
  if (typeof providerId !== 'string' || !providerId || providerId.length > 128)
    throw new Error('团队身份源无效');
  // JSON flow mappings are valid YAML; no interpolation into YAML syntax.
  return (
    '- id: auriga-dsh\n  disabled: false\n  config: ' +
    JSON.stringify({
      connect: {
        platformOrigin: origin,
        issuer: origin,
        clientId: 'auriga-beta-native',
        providerId,
        deviceName: 'Auriga Beta isolated DSH',
        allowLocalHttp: true,
      },
    }) +
    '\n'
  );
}

export async function stopOwnedProcess(
  record,
  { identity = processIdentity, kill = (pid) => process.kill(pid) } = {},
) {
  if (!record) return;
  if (
    !Number.isSafeInteger(record.pid) ||
    record.pid <= 0 ||
    typeof record.identity !== 'string' ||
    !record.identity
  )
    throw new Error('进程记录无效');
  const current = identity(record.pid);
  if (!current) return;
  if (current !== record.identity)
    throw new Error('PID 已复用，拒绝终止其他进程');
  kill(record.pid);
  await waitUntil(
    () => identity(record.pid) !== current,
    '自有 DSH 进程没有退出',
    10000,
  );
}

function regularJson(file) {
  const stat = fs.lstatSync(file);
  if (!stat.isFile() || stat.isSymbolicLink() || stat.nlink !== 1)
    throw new Error('状态文件必须是无链接普通文件');
  return JSON.parse(fs.readFileSync(file, 'utf8'));
}

function runLogged(command, args, state, env, name, cwd = state) {
  const log = fs.openSync(path.join(state, name + '.log'), 'a', 0o600);
  try {
    const result = spawnSync(command, args, {
      cwd,
      env,
      windowsHide: true,
      stdio: ['ignore', log, log],
      timeout: 300000,
    });
    if (result.error || result.status !== 0)
      throw new Error(
        `${name} 失败（退出码 ${result.status ?? result.error?.code}），查看 StateDir/${name}.log`,
      );
  } finally {
    fs.closeSync(log);
  }
}

async function discoverProvider() {
  const response = await fetch(origin + '/api/iam/v1/login/providers', {
    redirect: 'error',
    signal: AbortSignal.timeout(5000),
  });
  if (!response.ok) throw new Error('Beta 团队身份源不可读；请先启动既有 Beta');
  const data = await response.json();
  const providers = data.items?.filter(
    (p) => p.connectorType === 'team-account' && p.primaryStep === 'form',
  );
  if (providers?.length !== 1)
    throw new Error('Beta 必须恰有一个团队账号表单身份源');
  return providers[0].id;
}

function dshCommand(root, state) {
  if (inside(root, state) || inside(state, root))
    throw new Error('StateDir 不能与已安装宿主重叠');
  const pkg = regularJson(path.join(root, 'package.json'));
  if (pkg.name !== '@deepseek-ai/dsh')
    throw new Error('--dsh-package 不是 @deepseek-ai/dsh 包');
  const bin = typeof pkg.bin === 'string' ? pkg.bin : pkg.bin?.dsh;
  if (!bin || !inside(path.resolve(root), path.resolve(root, bin)))
    throw new Error('DSH 未声明公开 dsh CLI');
  return { bin: path.resolve(root, bin), version: pkg.version };
}

async function launch(options) {
  const { state } = options;
  assertStatePath(state);
  const dsh = dshCommand(path.resolve(options.dshPackage), state);
  const providerId = await discoverProvider();
  claimState(state);
  privatePath(state, true);
  const lock = path.join(state, 'plugin-dsh.lock');
  const handle = fs.openSync(lock, 'wx', 0o600);
  try {
    return await prepareLaunch(options, dsh, providerId);
  } finally {
    fs.closeSync(handle);
    fs.unlinkSync(lock);
  }
}

async function prepareLaunch({ state }, dsh, providerId) {
  const env = isolatedEnvironment(state);
  for (const dir of [
    'dsh',
    'home',
    'home/roaming',
    'home/local',
    'config',
    'data',
    'cache',
    'tmp',
    'workspace',
    'packages',
  ])
    fs.mkdirSync(path.join(state, dir), { recursive: true, mode: 0o700 });
  fs.writeFileSync(env.npm_config_userconfig, '');
  fs.writeFileSync(env.npm_config_globalconfig, '');
  const manifest = {
    kind: 'auriga-beta-plugin-dsh',
    schema: 1,
    state,
    dshVersion: dsh.version,
    nodeVersion: process.version,
    process: null,
  };
  const manifestFile = path.join(state, 'plugin-dsh.json');
  json(manifestFile, manifest);
  runLogged(
    'powershell.exe',
    [
      '-NoProfile',
      '-NonInteractive',
      '-File',
      path.join(here, 'plugin-dsh-build.ps1'),
      '-StateDir',
      state,
    ],
    state,
    windowsPowerShellEnvironment(),
    'build',
    repo,
  );
  const tar = path.join(state, 'packages/auriga-dsh-0.0.0.tgz');
  if (!fs.existsSync(tar)) throw new Error('插件成品包缺失');
  const cli = (args, name) =>
    runLogged(
      process.execPath,
      [dsh.bin, ...args],
      state,
      env,
      name,
      path.join(state, 'workspace'),
    );
  cli(['--profile', 'web', '--dump-config'], 'profile-init');
  cli(
    [
      'plugin',
      '--profile',
      'web',
      'add',
      tar,
      '--ignore-scripts',
      '--store-dir',
      path.join(state, 'cache/pnpm'),
    ],
    'plugin-install',
  );
  fs.writeFileSync(
    path.join(state, 'dsh/profiles/web/cordis.patch.yml'),
    connectionPatch(providerId),
  );
  cli(['--profile', 'web', '--dump-config'], 'profile-config');
  const output = fs.openSync(path.join(state, 'dsh.log'), 'a', 0o600);
  let child;
  try {
    child = spawn(
      process.execPath,
      [
        dsh.bin,
        '--profile',
        'web',
        '--host',
        '127.0.0.1',
        '--port',
        '0',
        '--no-open',
      ],
      {
        cwd: path.join(state, 'workspace'),
        env,
        windowsHide: true,
        detached: true,
        stdio: ['ignore', output, output],
      },
    );
    await new Promise((resolve, reject) => {
      child.once('spawn', resolve);
      child.once('error', reject);
    });
    const identity = processIdentity(child.pid);
    if (!identity) throw new Error('DSH 在登记前退出，请检查私有日志');
    manifest.process = { pid: child.pid, identity };
    json(manifestFile, manifest);
    let url;
    await waitUntil(
      async () => {
        if (processIdentity(child.pid) !== identity)
          throw new Error('DSH 启动失败，请检查 StateDir/dsh.log');
        const log = stripVTControlCharacters(
          fs.readFileSync(path.join(state, 'dsh.log'), 'utf8'),
        );
        const found = log
          .match(/http:\/\/127\.0\.0\.1:\d+\/[^\s<>"']*/g)
          ?.at(-1);
        if (!found) return false;
        const parsed = new URL(found);
        try {
          const exchange = await fetch(found, {
            redirect: 'manual',
            signal: AbortSignal.timeout(2000),
          });
          const cookie = exchange.headers
            .getSetCookie()
            .map((line) => line.split(';')[0])
            .join('; ');
          const response = await fetch(parsed.origin + '/auriga/local-login', {
            headers: { cookie },
            redirect: 'manual',
            signal: AbortSignal.timeout(2000),
          });
          if (response.status !== 200) return false;
          const page = await response.text();
          if (!page.includes('auriga')) return false;
          const status = await fetch(
            parsed.origin + '/auriga/local-login/status',
            {
              headers: { cookie },
              redirect: 'manual',
              signal: AbortSignal.timeout(2000),
            },
          );
          if (!status.ok || (await status.json()).available !== true)
            return false;
        } catch {
          return false;
        }
        url = found;
        return true;
      },
      'DSH 插件登录路由未就绪，请检查 StateDir/dsh.log',
      60000,
    );
    child.unref();
    console.log(
      `DSH ${dsh.version}；全新隔离 profile 已启动。\n本机 URL（含宿主启动票据，仅私下打开）：${url}\nStateDir：${state}`,
    );
  } catch (error) {
    if (manifest.process) await stopOwnedProcess(manifest.process);
    else if (child?.pid) child.kill();
    manifest.process = null;
    json(manifestFile, manifest);
    throw error;
  } finally {
    fs.closeSync(output);
  }
}

export async function main(args) {
  const options = parseArguments(args);
  if (process.platform !== 'win32') throw new Error('本脚手架要求原生 Windows');
  assertStatePath(options.state);
  if (options.action === 'launch') return launch(options);
  privatePath(options.state);
  const file = path.join(options.state, 'plugin-dsh.json');
  const manifest = regularJson(file);
  if (
    manifest.kind !== 'auriga-beta-plugin-dsh' ||
    manifest.schema !== 1 ||
    manifest.state !== options.state
  )
    throw new Error('不是本脚手架创建的 StateDir');
  const lock = path.join(options.state, 'plugin-dsh.lock');
  const handle = fs.openSync(lock, 'wx', 0o600);
  try {
    if (options.action === 'stop') {
      await stopOwnedProcess(manifest.process);
      manifest.process = null;
      json(file, manifest);
      console.log('隔离 DSH 已停止；保留测试数据，不影响 Beta 或用户 DSH。');
    } else {
      console.log(
        JSON.stringify({
          dshVersion: manifest.dshVersion,
          running:
            !!manifest.process &&
            processIdentity(manifest.process.pid) === manifest.process.identity,
        }),
      );
    }
  } finally {
    fs.closeSync(handle);
    fs.unlinkSync(lock);
  }
}

if (
  process.argv[1] &&
  path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)
) {
  main(process.argv.slice(2)).catch((error) => {
    console.error(error.message);
    process.exitCode = 1;
  });
}
