import assert from 'node:assert/strict';
import { execFile, spawn } from 'node:child_process';
import { promisify } from 'node:util';
import { createHash, randomUUID } from 'node:crypto';
import { readFile, writeFile, mkdir, realpath, access } from 'node:fs/promises';
import { resolve, join, dirname, relative, isAbsolute } from 'node:path';
import { createHostOutput } from '../../scripts/beta-ui/plugin-dsh.mjs';
import { privatePath } from '../../scripts/beta-ui/runner.mjs';
import {
  isolatedEnvironment,
  assertRuntimeLock,
  hostPin,
} from '../network/support/native-install.mjs';

const exec = promisify(execFile);
export async function poll(action, label, timeout = 45000) {
  const end = Date.now() + timeout;
  do {
    const value = await action();
    if (value) return value;
    if (Date.now() >= end)
      throw Object.assign(new Error(label), { code: 'deadline_exceeded' });
    await new Promise((resolve) => setTimeout(resolve, 200));
  } while (Date.now() <= end);
  throw Object.assign(new Error(label), { code: 'deadline_exceeded' });
}

async function nativeRuntime(runtime) {
  runtime = resolve(runtime);
  const pkg = JSON.parse(
    await readFile(
      join(runtime, 'node_modules/@deepseek-ai/dsh/package.json'),
      'utf8',
    ),
  );
  assert.equal(pkg.version, hostPin.version, 'locked desktop DSH version');
  assertRuntimeLock(
    JSON.parse(await readFile(join(runtime, 'package-lock.json'), 'utf8')),
  );
  return {
    runtime,
    version: pkg.version,
    bin: join(runtime, 'node_modules/@deepseek-ai/dsh', pkg.bin.dsh),
  };
}

export async function prepareDesktop({
  root,
  state,
  runtime,
  pluginPackage,
  packageManifest,
  connect,
}) {
  root = resolve(root);
  state = resolve(state);
  const rel = relative(root, state);
  assert.ok(
    rel && !rel.startsWith('..') && !isAbsolute(rel),
    'desktop test state must stay in this worktree',
  );
  assert.equal(
    await realpath(dirname(state)),
    dirname(state),
    'state parent must not redirect through a link',
  );
  const host = await nativeRuntime(runtime);
  const asset = JSON.parse(await readFile(packageManifest, 'utf8'));
  const sha256 = createHash('sha256')
    .update(await readFile(pluginPackage))
    .digest('hex');
  assert.equal(asset.name, 'auriga-dsh');
  assert.equal(asset.sha256, sha256, 'exact release tarball SHA256');
  for (const file of [
    'src/plugin.js',
    'cordis.patch.yml',
    'ui/auriga-team-panel.js',
    'ui/metadata.json',
  ])
    assert.ok(
      asset.files.includes(file),
      'self-contained release asset missing',
    );
  await mkdir(state, { mode: 0o700 }); // Exclusive: never overwrite a profile or its identity.
  if (process.platform === 'win32') privatePath(state, true);
  const env = { ...isolatedEnvironment(state), AURIGA_MODE: 'connect' };
  for (const name of [
    'cwd',
    'session-cwd',
    'dsh',
    'home',
    'config',
    'tmp',
    'cache',
  ])
    await mkdir(join(state, name), { recursive: true, mode: 0o700 });
  for (const file of [env.npm_config_userconfig, env.npm_config_globalconfig])
    await writeFile(file, '');
  const invoke = async (args, label) => {
    const result = await exec(process.execPath, [host.bin, ...args], {
      env,
      cwd: join(state, 'cwd'),
      windowsHide: true,
      timeout: 180000,
      maxBuffer: 8 * 1024 * 1024,
    });
    await writeFile(join(state, label + '.log'), result.stdout + result.stderr);
  };
  await invoke(['--profile', 'web', '--dump-config'], 'profile');
  const profile = join(state, 'dsh/profiles/web');
  const profilePackage = join(profile, 'package.json');
  await writeFile(
    profilePackage,
    JSON.stringify(
      {
        ...JSON.parse(await readFile(profilePackage, 'utf8')),
        packageManager: hostPin.packageManager,
      },
      null,
      2,
    ),
  );
  await invoke(
    [
      'plugin',
      '--profile',
      'web',
      'add',
      resolve(pluginPackage),
      '--ignore-scripts',
      '--store-dir',
      join(state, 'cache/pnpm'),
    ],
    'package-install',
  );
  await writeFile(
    join(profile, 'cordis.patch.yml'),
    '- id: auriga-dsh\n  disabled: false\n  config: ' +
      JSON.stringify(connect ? { connect } : {}) +
      '\n',
  );
  await invoke(['--profile', 'web', '--dump-config'], 'configured-profile');
  const installed = JSON.parse(
    await readFile(
      join(profile, 'node_modules/auriga-dsh/package.json'),
      'utf8',
    ),
  );
  assert.equal(installed.version, asset.version);
  assert.equal(installed.main, './src/plugin.js');
  assert.equal(
    installed.scripts,
    undefined,
    'distributable must have no install/build lifecycle',
  );
  for (const name of ['ui/auriga-team-panel.js', 'ui/metadata.json'])
    await access(join(profile, 'node_modules/auriga-dsh', name));
  const result = {
    state,
    profile,
    runtime: host.runtime,
    dshVersion: host.version,
    packageVersion: asset.version,
    packageSHA256: sha256,
    installation: 'native-cli-tarball',
    connectConfigured: !!connect,
    platformOrigin: connect?.platformOrigin ?? null,
    usedForLiveRun: false,
  };
  await writeFile(
    join(state, 'cloud-desk-desktop.json'),
    JSON.stringify(result, null, 2),
  );
  return result;
}

export async function launchDesktop(prepared, { connect, caFile } = {}) {
  const host = await nativeRuntime(prepared.runtime);
  const state = resolve(prepared.state);
  const ownedRoot = resolve(import.meta.dirname, '../..');
  const ownedRelative = relative(ownedRoot, state);
  assert.ok(
    ownedRelative &&
      !ownedRelative.startsWith('..') &&
      !isAbsolute(ownedRelative),
    'only this worktree owns the desktop profile',
  );
  assert.equal(
    await realpath(state),
    state,
    'desktop state must not redirect through a link',
  );
  assert.equal(prepared.profile, join(state, 'dsh/profiles/web'));
  if (connect)
    await writeFile(
      join(prepared.profile, 'cordis.patch.yml'),
      '- id: auriga-dsh\n  disabled: false\n  config: ' +
        JSON.stringify({ connect }) +
        '\n',
    );
  const env = {
    ...isolatedEnvironment(state),
    AURIGA_MODE: 'connect',
    ...(caFile ? { NODE_EXTRA_CA_CERTS: resolve(caFile) } : {}),
  };
  const child = spawn(
    process.execPath,
    [
      host.bin,
      '--profile',
      'web',
      '--host',
      '127.0.0.1',
      '--port',
      '0',
      '--no-open',
    ],
    {
      env,
      cwd: join(state, 'cwd'),
      windowsHide: true,
      stdio: ['ignore', 'pipe', 'pipe'],
    },
  );
  let launch,
    closed,
    logs = '';
  const done = new Promise((resolve) =>
    child.once('close', (code, signal) => {
      closed = { code, signal };
      resolve(closed);
    }),
  );
  child.once('error', () => {
    closed = { error: 'native_launch_failed' };
  });
  const collector = createHostOutput(
    (line) => {
      logs += line;
    },
    (url) => {
      launch ??= url;
    },
  );
  for (const stream of [child.stdout, child.stderr])
    stream.on('data', (chunk) => collector.write(chunk));
  const cookies = new Map();
  const accept = (response) => {
    for (const value of response.headers.getSetCookie()) {
      const pair = value.split(';')[0],
        at = pair.indexOf('=');
      cookies.set(pair.slice(0, at), pair.slice(at + 1));
    }
  };
  let origin;
  const request = async (path, body, csrf) => {
    const response = await globalThis.fetch(origin + path, {
      method: body === undefined ? 'GET' : 'POST',
      redirect: 'error',
      signal: globalThis.AbortSignal.timeout(15000),
      headers: {
        cookie: [...cookies].map(([k, v]) => `${k}=${v}`).join('; '),
        ...(body === undefined
          ? {}
          : {
              origin,
              'content-type': 'application/json',
              ...(path.startsWith('/auriga/settings/')
                ? { 'x-auriga-settings': '1' }
                : {}),
              ...(csrf ? { 'x-auriga-csrf': csrf } : {}),
            }),
      },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
    accept(response);
    const value = await response.json();
    if (response.status !== 200)
      throw Object.assign(new Error('desktop_native_http_failed'), {
        status: response.status,
        ...([
          'httpsRequired',
          'invalidOrigin',
          'invalidSettings',
          'forbidden',
          'auth',
          'unavailable',
        ].includes(value.reason)
          ? { reason: value.reason }
          : {}),
      });
    return value;
  };
  const stop = async () => {
    if (!closed) child.kill();
    const result = await Promise.race([
      done,
      new Promise((resolve) => {
        const timer = setTimeout(() => resolve(null), 10000);
        timer.unref();
      }),
    ]);
    if (!result) {
      child.kill('SIGKILL');
      await done;
    }
    collector.end();
    await writeFile(join(state, 'native-host.log'), logs);
    assert.ok(closed, 'owned desktop process stopped');
    return closed;
  };
  try {
    await poll(() => {
      if (closed) throw new Error('desktop_native_exited');
      return launch;
    }, 'desktop_startup_url_missing');
    origin = new URL(launch).origin;
    const response = await globalThis.fetch(launch, {
      redirect: 'manual',
      signal: globalThis.AbortSignal.timeout(5000),
    });
    accept(response);
    await response.arrayBuffer();
    const status = await poll(async () => {
      if (closed) throw new Error('desktop_native_exited');
      try {
        return await request(
          connect ? '/auriga/local-login/status' : '/auriga/settings/state',
        );
      } catch (error) {
        if ([404, 503].includes(error.status)) return null;
        throw error;
      }
    }, 'desktop_plugin_not_ready');
    return {
      origin,
      request,
      stop,
      pid: child.pid,
      initialStatus: connect
        ? { available: status.available, loggedIn: status.loggedIn }
        : {
            configured: false,
            settingsAvailable: true,
            platformOrigin: status.platformOrigin,
          },
    };
  } catch (error) {
    try {
      const inventory = await request('/api/pluginManager/listPlugins', {
        type: 'client-request',
        rpcId: randomUUID(),
        method: 'pluginManager/listPlugins',
        payload: { args: {} },
      });
      const rows = inventory.result?.value
        ?.filter((row) => row.moduleName === 'auriga-dsh')
        .map(({ entryId, moduleName, enabled, fiberPhase }) => ({
          entryId,
          moduleName,
          enabled,
          fiberPhase,
        }));
      await writeFile(
        join(state, 'startup-inventory.json'),
        JSON.stringify(
          { rows: rows ?? [], ok: inventory.result?.ok === true },
          null,
          2,
        ),
      );
    } catch {
      /* Preserve the original startup failure. */
    }
    await stop();
    throw error;
  }
}
