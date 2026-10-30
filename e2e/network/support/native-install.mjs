import assert from 'node:assert/strict';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { mkdir, readFile, copyFile, writeFile } from 'node:fs/promises';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import {
  hostInstallOptions,
  assertPublicHostLock,
  hostInstallFailure,
} from '../../../scripts/beta-ui/plugin-dsh.mjs';

export const hostPin = Object.freeze({
  version: '0.2.1-rc.3',
  integrity:
    'sha512-H+E9HLnHf7fJqzen84Z9Bq8ggb5xbrcbyhZjXTJLE9NabHr3zteGvTRvdvGkxPl4ov80is4WLglFmSo+/olJjg==',
  packageManager: 'pnpm@11.28.6',
});
const template = new URL('./dsh-runtime/', import.meta.url);
export const defaultRuntime = fileURLToPath(
  new URL('../target/native-runtime/', import.meta.url),
);

// Allowlist: no user profile overrides, model keys or Node startup hooks.
export function isolatedEnvironment(state, source = process.env) {
  const allowed =
    /^(PATH|SYSTEMROOT|WINDIR|COMSPEC|PATHEXT|SYSTEMDRIVE|PROCESSOR_ARCHITECTURE|NUMBER_OF_PROCESSORS|NODE_EXTRA_CA_CERTS|HTTPS?_PROXY|NO_PROXY)$/i;
  const env = Object.fromEntries(
    Object.entries(source).filter(([key]) => allowed.test(key)),
  );
  for (const [key, path] of Object.entries({
    DSH_HOME: 'dsh',
    HOME: 'home',
    USERPROFILE: 'home',
    APPDATA: 'home/roaming',
    LOCALAPPDATA: 'home/local',
    XDG_CONFIG_HOME: 'config',
    XDG_DATA_HOME: 'data',
    XDG_CACHE_HOME: 'cache',
    TMP: 'tmp',
    TEMP: 'tmp',
    TMPDIR: 'tmp',
    COREPACK_HOME: 'cache/corepack',
    npm_config_cache: 'cache/npm',
    npm_config_userconfig: 'config/npmrc',
    npm_config_globalconfig: 'config/global-npmrc',
    npm_config_store_dir: 'cache/pnpm',
    PNPM_HOME: 'cache/pnpm-home',
  }))
    env[key] = join(state, path);
  return {
    ...env,
    COREPACK_ENABLE_AUTO_PIN: '0',
    COREPACK_DEFAULT_TO_LATEST: '0',
    COREPACK_ENABLE_DOWNLOAD_PROMPT: '0',
    CI: 'true',
  };
}

export function assertRuntimeLock(lock) {
  assert.equal(
    lock.packages[''].dependencies['@deepseek-ai/dsh'],
    hostPin.version,
  );
  assert.equal(
    lock.packages['node_modules/@deepseek-ai/dsh'].version,
    hostPin.version,
  );
  assert.equal(
    lock.packages['node_modules/@deepseek-ai/dsh'].integrity,
    hostPin.integrity,
  );
  assertPublicHostLock(lock);
}

export async function runNativeInstallStep(
  name,
  args,
  { state, env, timeoutMs = 300000, npmCache },
) {
  const run = promisify(execFile),
    started = Date.now();
  const metadata = {
    command: process.execPath,
    args,
    cwd: state,
    timeoutMs,
    npmCache,
  };
  try {
    const result = await run(process.execPath, args, {
      cwd: state,
      env,
      windowsHide: true,
      timeout: timeoutMs,
      maxBuffer: 8 * 1024 * 1024,
    });
    await writeFile(
      join(state, name + '.log'),
      JSON.stringify({
        ...metadata,
        exitCode: 0,
        signal: null,
        killed: false,
        durationMs: Date.now() - started,
      }) +
        '\n' +
        result.stdout +
        result.stderr,
    );
    return result.stdout.trim();
  } catch (error) {
    const failure = hostInstallFailure(error, timeoutMs);
    const log = join(state, name + '.log');
    await writeFile(
      log,
      JSON.stringify({
        ...metadata,
        exitCode: error.code ?? null,
        signal: error.signal ?? null,
        killed: error.killed === true,
        durationMs: Date.now() - started,
        failure,
      }) +
        '\n' +
        (error.stdout ?? '') +
        (error.stderr ?? ''),
    );
    throw new Error(
      `${name} failed: ${failure}${npmCache ? '; npm cache=' + npmCache : ''}; see ${log}`,
      { cause: error },
    );
  }
}

export async function installNativeRuntime(
  runtime = defaultRuntime,
  options = {},
) {
  const state = resolve(runtime);
  const install = hostInstallOptions(process.env, options);
  const env = isolatedEnvironment(state);
  for (const path of [
    'dsh',
    'home/roaming',
    'home/local',
    'config',
    'data',
    'cache',
    'tmp',
  ])
    await mkdir(join(state, path), { recursive: true });
  for (const file of [env.npm_config_userconfig, env.npm_config_globalconfig])
    await writeFile(file, '', { mode: 0o600 });
  assertRuntimeLock(
    JSON.parse(await readFile(new URL('package-lock.json', template), 'utf8')),
  );
  for (const file of ['package.json', 'package-lock.json'])
    await copyFile(new URL(file, template), join(state, file));
  await mkdir(install.npmCache, { recursive: true });
  const invoke = (name, args, settings = {}) =>
    runNativeInstallStep(name, args, { state, env, ...settings });
  const corepack = join(
    dirname(process.execPath),
    'node_modules/corepack/dist/corepack.js',
  );
  await invoke('pnpm-prepare', [
    corepack,
    'prepare',
    hostPin.packageManager,
    '--activate',
  ]);
  assert.equal(
    await invoke('pnpm-version', [corepack, 'pnpm', '--version']),
    '11.28.6',
  );
  const npm = join(
    dirname(process.execPath),
    'node_modules/npm/bin/npm-cli.js',
  );
  await invoke('host-install', [npm, ...install.args], {
    timeoutMs: install.timeoutMs,
    npmCache: install.npmCache,
  });
  const pkg = JSON.parse(
    await readFile(
      join(state, 'node_modules/@deepseek-ai/dsh/package.json'),
      'utf8',
    ),
  );
  assert.equal(pkg.version, hostPin.version);
  return state;
}

if (
  process.argv[1] &&
  resolve(process.argv[1]) === fileURLToPath(import.meta.url)
) {
  assert.equal(process.argv.slice(2).join(' '), '--install');
  console.log(
    JSON.stringify({ runtime: await installNativeRuntime(), ...hostPin }),
  );
}
