/**
 * Isolated end-to-end check of built packages (Windows).
 *
 *   node scripts/e2e.mjs --old <package dir|setup.exe> --sandbox <dir>
 *        --disposable-windows-user true
 *        [--new <package dir>] [--broken <package dir>] [--expect-origin <origin>]
 *
 * Installs into a sandbox (install root, DSH_HOME, USERPROFILE/APPDATA, start
 * menu and desktop folders and a test-only registry key all live under or are
 * named for --sandbox), starts DSH on an explicit isolated port, checks the
 * plugin, shell integration and pnpm, tries a broken upgrade (must roll back),
 * upgrades, rolls back and uninstalls. Every started process is killed at the
 * end and the test registry key is removed.
 */
import { spawn, spawnSync } from 'node:child_process';
import { once } from 'node:events';
import {
  existsSync,
  mkdirSync,
  readFileSync,
  renameSync,
  rmSync,
  writeFileSync,
} from 'node:fs';
import { join, resolve } from 'node:path';
import { createServer } from 'node:net';
import { randomUUID } from 'node:crypto';
import { fileURLToPath } from 'node:url';
import { setTimeout as delay } from 'node:timers/promises';
import { launchEnv } from '../runtime/core.mjs';
import { DESKTOP_CORE_DIR, treeDigest } from '../runtime/verify.mjs';

const args = Object.fromEntries(
  process.argv.slice(2).reduce((acc, v, i, all) => {
    if (v.startsWith('--')) acc.push([v.slice(2), all[i + 1]]);
    return acc;
  }, []),
);
if (args['disposable-windows-user'] !== 'true')
  throw new Error(
    'Run the native installer E2E only as a disposable Windows user.',
  );
const sandbox = resolve(args.sandbox);
const oldPkg = resolve(args.old);
const newPkg = args.new ? resolve(args.new) : null;
const brokenPkg = args.broken ? resolve(args.broken) : null;
const expectOrigin = args['expect-origin'] ?? null;
const root = join(sandbox, 'root');
const dataHome = join(sandbox, 'data');
const startMenu = join(sandbox, 'startmenu');
const desktop = join(sandbox, 'desktop');
const registryKey = `AurigaDesktop-e2e-${randomUUID()}`;
const registryPath = `HKCU\\Software\\Microsoft\\Windows\\CurrentVersion\\Uninstall\\${registryKey}`;
const runPath = 'HKCU\\Software\\Microsoft\\Windows\\CurrentVersion\\Run';
const env = {
  ...process.env,
  USERPROFILE: join(sandbox, 'home'),
  HOME: join(sandbox, 'home'),
  APPDATA: join(sandbox, 'appdata'),
  LOCALAPPDATA: join(sandbox, 'local'),
  TMP: join(sandbox, 'temp'),
  TEMP: join(sandbox, 'temp'),
  AURIGA_INSTALL_DIR: root,
  AURIGA_DATA_HOME: dataHome,
  AURIGA_START_MENU_DIR: startMenu,
  AURIGA_DESKTOP_DIR: desktop,
  AURIGA_REGISTRY_KEY: registryKey,
};
delete env.DSH_HOME;
delete env.AURIGA_PLATFORM_ORIGIN;
for (const d of ['home', 'appdata', 'local', 'temp'])
  mkdirSync(join(sandbox, d), { recursive: true });

const steps = [];
const note = (name, detail) => {
  steps.push({ name, ok: true, detail });
  console.log(`ok   ${name}${detail ? ` - ${detail}` : ''}`);
};
function check(condition, name, detail) {
  if (!condition) {
    steps.push({ name, ok: false, detail });
    throw new Error(`check failed: ${name}${detail ? ` - ${detail}` : ''}`);
  }
  note(name, detail);
}
const readJson = (f) =>
  JSON.parse(readFileSync(f, 'utf8').replace(/^\uFEFF/, ''));
const run = (cmd, argv, extra = {}) =>
  spawnSync(cmd, argv, {
    env,
    cwd: sandbox,
    encoding: 'utf8',
    timeout: 1800000,
    ...extra,
  });
const regQuery = () => run('reg.exe', ['query', registryPath]);
const shortcut = (path, target) =>
  run('powershell.exe', [
    '-NoProfile',
    '-NonInteractive',
    '-ExecutionPolicy',
    'Bypass',
    '-File',
    fileURLToPath(new URL('./e2e-shortcut.ps1', import.meta.url)),
    '-LinkPath',
    path,
    ...(target ? ['-TargetPath', target] : []),
  ]);
const sameWindowsPath = (a, b) =>
  resolve(a).toLowerCase() === resolve(b).toLowerCase();

function checkShortcut(label, path, expectedTarget) {
  const result = shortcut(path);
  check(
    result.status === 0 &&
      sameWindowsPath(result.stdout.trim(), expectedTarget),
    label,
    result.status === 0
      ? `${path} -> ${result.stdout.trim()} (expected ${expectedTarget})`
      : `${path}: ${result.stderr.trim()}`,
  );
}

/** Install (or upgrade) from a package directory or a single-file setup exe. */
function install(pkg, extra = []) {
  const r = pkg.endsWith('.exe')
    ? run(pkg, ['/VERYSILENT', ...extra])
    : run('powershell.exe', [
        '-NoProfile',
        '-ExecutionPolicy',
        'Bypass',
        '-File',
        join(pkg, 'install.ps1'),
        ...extra,
      ]);
  return r;
}
function mustInstall(pkg, extra) {
  const r = install(pkg, extra);
  if (r.status !== 0)
    throw new Error(`install failed (${r.status}): ${r.stdout}\n${r.stderr}`);
  const warning = [r.stdout, r.stderr]
    .join('\n')
    .split(/\r?\n/)
    .find((line) => line.includes('Shell integration failed:'));
  if (warning) throw new Error(warning);
  return JSON.parse(r.stdout.split('\n').find((l) => l.startsWith('{')));
}

// Old packages may have a different default; select and pass the same port for
// every install/upgrade/rollback probe. Default-port behavior is tested separately.
async function freePort() {
  return new Promise((resolve, reject) => {
    const server = createServer();
    server.once('error', reject);
    server.listen(0, '127.0.0.1', () => {
      const { port } = server.address();
      server.close((error) => (error ? reject(error) : resolve(port)));
    });
  });
}

const started = [];
function trayExecutable(id) {
  return join(root, 'versions', id, 'auriga', 'tray', 'AurigaDesktop.exe');
}

function running(pid) {
  if (!Number.isSafeInteger(pid) || pid < 1) return false;
  try {
    process.kill(pid, 0);
    return true;
  } catch (error) {
    if (error.code === 'ESRCH') return false;
    throw error;
  }
}

async function startTrayForUninstall(id) {
  const tray = trayExecutable(id);
  if (!existsSync(tray)) return null;
  const options = { env, cwd: sandbox, windowsHide: true, stdio: 'ignore' };
  const first = spawn(tray, ['/autostart'], options);
  started.push(first);
  const processFile = join(dataHome, 'auriga', 'guardian', 'process.json');
  const deadline = Date.now() + 180000;
  let processTree;
  while (Date.now() < deadline && first.exitCode === null) {
    const tree = existsSync(processFile) ? readJson(processFile) : null;
    if (running(tree?.launcher?.pid) && running(tree?.root?.pid)) {
      processTree = tree;
      break;
    }
    await delay(500);
  }
  check(processTree, 'tray starts a real launcher and DSH');
  const second = spawn(tray, ['/autostart'], options);
  started.push(second);
  const [code] = await once(second, 'exit');
  check(
    code === 0 && running(first.pid),
    'second tray start exits; original instance remains',
  );
  return { first, processTree };
}

async function withDsh(fn) {
  const port = await freePort();
  const child = spawn(
    'cmd.exe',
    [
      '/c',
      join(root, 'bin', 'auriga-desktop.cmd'),
      '--port',
      String(port),
      '--no-open',
    ],
    { env, cwd: sandbox },
  );
  started.push(child);
  let out = '';
  child.stdout.on('data', (d) => (out += d));
  child.stderr.on('data', (d) => (out += d));
  try {
    const deadline = Date.now() + 180000;
    let token;
    while (Date.now() < deadline && !token) {
      token = /token=([A-Za-z0-9_-]+)/.exec(out)?.[1];
      if (!token) await new Promise((r) => setTimeout(r, 1000));
    }
    if (!token) throw new Error(`dsh did not start: ${out.slice(-500)}`);
    const base = `http://127.0.0.1:${port}`;
    const login = await globalThis.fetch(`${base}/?token=${token}`, {
      redirect: 'manual',
    });
    const cookie = login.headers
      .getSetCookie()
      .map((c) => c.split(';')[0])
      .join('; ');
    const call = (path, init = {}) =>
      globalThis.fetch(base + path, {
        ...init,
        headers: { cookie, origin: base, ...init.headers },
        redirect: 'manual',
      });
    return await fn({
      base,
      call,
      out: () => out.replace(/token=[A-Za-z0-9_-]+/g, 'token=REDACTED'),
    });
  } finally {
    spawnSync('taskkill', ['/PID', String(child.pid), '/T', '/F']);
  }
}

async function pluginChecks({ base, call }, expected, label) {
  const anon = await globalThis.fetch(`${base}/auriga/`, {
    redirect: 'manual',
  });
  const missing = await globalThis.fetch(`${base}/nonexistent-zzz`, {
    redirect: 'manual',
  });
  check(
    anon.status === 401 && missing.status === 404,
    `${label}: plugin route registered`,
    `/auriga/=${anon.status}, unknown=${missing.status}`,
  );
  const state = await (await call('/auriga/settings/state')).json();
  check(
    state.mode === 'connect' && state.platformOrigin === expected,
    `${label}: plugin enabled, platform address`,
    JSON.stringify(state),
  );
}

const pluginVersion = () => {
  const id = readJson(join(root, 'install.json')).current;
  const dir = join(root, 'versions', id);
  const manifest = readJson(join(dir, 'manifest.json'));
  const core =
    manifest.plugin.scope === 'installation'
      ? join(dir, DESKTOP_CORE_DIR)
      : join(dataHome, 'profiles/auriga/node_modules/auriga-dsh');
  return readJson(join(core, 'package.json')).version;
};

try {
  // 1. install the old package (zip directory or single-file exe)
  const first = mustInstall(oldPkg);
  check(first.upgraded === false, 'install old package', first.id);
  const manifestOld = readJson(
    join(root, 'versions', first.id, 'manifest.json'),
  );
  const profile = join(dataHome, 'profiles', 'auriga');
  const versionDir = join(root, 'versions', first.id);
  const scoped = manifestOld.plugin.scope === 'installation';
  const profileCore = join(
    profile,
    'node_modules',
    'auriga-dsh',
    'package.json',
  );
  check(
    scoped
      ? existsSync(join(versionDir, DESKTOP_CORE_DIR, 'package.json')) &&
          !existsSync(profileCore) &&
          !Object.hasOwn(
            readJson(join(profile, 'package.json')).dependencies,
            'auriga-dsh',
          )
      : existsSync(profileCore),
    'core ownership matches the release layout',
  );

  // 2. shell integration: shortcuts and Apps & features entry (current user)
  const launcher = join(root, 'bin', 'auriga-desktop.cmd');
  checkShortcut(
    'Start menu opens the installed launcher',
    join(startMenu, 'Auriga DSH.lnk'),
    launcher,
  );
  checkShortcut(
    'Desktop opens the installed launcher',
    join(desktop, 'Auriga DSH.lnk'),
    launcher,
  );
  if (existsSync(trayExecutable(first.id)))
    checkShortcut(
      'Start menu opens the installed uninstaller',
      join(startMenu, '卸载 Auriga DSH.lnk'),
      join(root, 'bin', 'uninstall.cmd'),
    );
  const reg = regQuery();
  check(
    reg.status === 0 &&
      /DisplayName\s+REG_SZ\s+Auriga DSH/.test(reg.stdout) &&
      /UninstallString\s+REG_SZ\s+.*uninstall\.ps1/.test(reg.stdout),
    'HKCU uninstall entry written',
    registryKey,
  );

  // Keep a sandbox inside a checkout from inheriting its ancestor pnpm version.
  writeFileSync(
    join(sandbox, 'package.json'),
    JSON.stringify({
      private: true,
      packageManager: `pnpm@${manifestOld.pnpm.version}`,
    }),
  );
  writeFileSync(join(sandbox, 'pnpm-workspace.yaml'), 'packages: []\n');

  // 3. bundled pnpm on the launch PATH
  const launchEnvironment = launchEnv(versionDir, dataHome, env);
  const pnpm = spawnSync('pnpm', ['--version'], {
    env: launchEnvironment,
    cwd: sandbox,
    encoding: 'utf8',
    shell: true,
    timeout: 60000,
  });
  check(
    pnpm.status === 0 && pnpm.stdout.trim() === manifestOld.pnpm.version,
    'bundled pnpm resolves from launch PATH',
    pnpm.stdout.trim(),
  );
  const dshBin = join(
    versionDir,
    'upstream',
    'app',
    'node_modules',
    '@deepseek-ai',
    'dsh',
    'lib',
    'bin.js',
  );
  // The plugin manager path: installing another plugin through the bundled
  // pnpm must keep the Auriga plugin intact.
  const tiny = join(sandbox, 'tiny');
  mkdirSync(tiny, { recursive: true });
  writeFileSync(
    join(tiny, 'package.json'),
    JSON.stringify({
      name: 'e2e-tiny-plugin',
      version: '1.0.0',
      main: 'index.js',
      dsh: { bundle: { patch: './patch.yml' } },
    }),
  );
  writeFileSync(join(tiny, 'index.js'), 'module.exports = {};\n');
  writeFileSync(join(tiny, 'patch.yml'), '[]\n');
  const pack = spawnSync('npm', ['pack', '--silent'], {
    cwd: tiny,
    env,
    encoding: 'utf8',
    shell: true,
  });
  const tgz = join(tiny, pack.stdout.trim().split(/\r?\n/).pop());
  if (scoped) {
    const failingSource = join(sandbox, 'failing-plugin', 'package');
    mkdirSync(failingSource, { recursive: true });
    writeFileSync(
      join(failingSource, 'package.json'),
      JSON.stringify({
        name: 'e2e-failing-plugin',
        version: '1.0.0',
        dsh: { bundle: { patch: './patch.yml' } },
        dependencies: {
          'e2e-missing-dependency': 'file:./missing-dependency.tgz',
        },
      }),
    );
    writeFileSync(join(failingSource, 'patch.yml'), '[]\n');
    const failingTar = join(sandbox, 'failing-plugin.tgz');
    const packedFailure = run('tar.exe', [
      '-czf',
      failingTar,
      '-C',
      join(failingSource, '..'),
      'package',
    ]);
    check(packedFailure.status === 0, 'pack failing business plugin fixture');
    writeFileSync(
      join(failingSource, 'package.json'),
      JSON.stringify({
        name: 'auriga-dsh',
        version: '99.0.0',
        dsh: { bundle: { patch: './patch.yml' } },
      }),
    );
    const collisionTar = join(sandbox, 'same-name-plugin.tgz');
    const packedCollision = run('tar.exe', [
      '-czf',
      collisionTar,
      '-C',
      join(failingSource, '..'),
      'package',
    ]);
    check(
      packedCollision.status === 0,
      'pack same-name business plugin fixture',
    );
    const before = treeDigest(versionDir, DESKTOP_CORE_DIR);
    await withDsh(async (dsh) => {
      const { call } = dsh;
      const manage = async (operation, args) => {
        const method = 'pluginManager/' + operation;
        const response = await call('/api/' + method, {
          method: 'POST',
          headers: { 'content-type': 'application/json' },
          body: JSON.stringify({
            type: 'client-request',
            rpcId: randomUUID(),
            method,
            payload: { args },
          }),
        });
        check(
          response.status === 200,
          'native plugin installer responded',
          String(response.status),
        );
        const { result } = await response.json();
        check(
          result?.ok === true,
          'native plugin RPC accepted the request',
          result?.error?.code,
        );
        return result.value;
      };
      const install = (file) =>
        manage('installBundle', {
          spec: 'file:' + file,
          options: { enabled: false, requestId: randomUUID() },
        });
      const blocked = await manage('setBundleEnabled', {
        name: 'auriga-dsh',
        enabled: false,
      });
      check(
        blocked.error?.code === 'management-required',
        'native manager protects core enablement',
      );
      const added = await install(tgz);
      check(
        added.bundle === 'e2e-tiny-plugin' &&
          !added.error &&
          existsSync(join(profile, 'node_modules', 'e2e-tiny-plugin')),
        'native plugin installer created the business dependency',
        added.application,
      );
      const refused = await install(failingTar);
      check(
        refused.application === 'failed' &&
          refused.packageResult?.output.includes('missing-dependency'),
        'native dependency installation failure was reported',
      );
      const collision = await install(collisionTar);
      check(
        collision.application === 'failed' &&
          collision.packageResult?.output.includes(
            'reserved for the release core',
          ) &&
          !Object.hasOwn(
            readJson(join(profile, 'package.json')).dependencies,
            'auriga-dsh',
          ),
        'native package manager refuses a same-name package',
      );
      await pluginChecks(dsh, expectOrigin ?? '', 'after-install-failure');
    });
    check(
      treeDigest(versionDir, DESKTOP_CORE_DIR).sha256 === before.sha256 &&
        !existsSync(profileCore),
      'business install and failure preserved the release core and dependency closure',
    );
  } else {
    const added = spawnSync(
      join(versionDir, 'upstream', 'node', 'node.exe'),
      [dshBin, 'plugin', '--profile', 'auriga', 'add', tgz],
      {
        env: launchEnvironment,
        cwd: sandbox,
        encoding: 'utf8',
        timeout: 600000,
      },
    );
    writeFileSync(join(sandbox, 'plugin-add.log'), added.stdout + added.stderr);
    check(
      added.status === 0 &&
        existsSync(join(profile, 'node_modules', 'e2e-tiny-plugin')) &&
        existsSync(profileCore),
      'legacy plugin install through bundled pnpm',
      String(added.status),
    );
  }

  // 4. launch, plugin loaded, default platform address, configurable
  const defaultOrigin = expectOrigin ?? '';
  await withDsh(async (dsh) => {
    await pluginChecks(dsh, defaultOrigin, 'old');
    if (expectOrigin) {
      const probe = await dsh.call('/auriga/settings/test', {
        method: 'POST',
        headers: {
          'content-type': 'application/json',
          'x-auriga-settings': '1',
        },
        body: JSON.stringify({ platformOrigin: expectOrigin }),
      });
      const body = await probe.text();
      writeFileSync(join(sandbox, 'platform-probe.json'), body);
      check(
        probe.status === 200 && JSON.parse(body).reachable === true,
        'platform capability probe reached the platform',
        body.slice(0, 300),
      );
      const login = await dsh.call('/auriga/local-login/status');
      const loginBody = await login.text();
      writeFileSync(join(sandbox, 'login-status.json'), loginBody);
      check(
        login.status === 200 &&
          JSON.parse(loginBody).available === true &&
          JSON.parse(loginBody).loggedIn === false,
        'Auriga login surface answers (available, signed out)',
        loginBody.slice(0, 300),
      );
      const page = await dsh.call('/auriga/settings/view.js');
      check(page.status === 200, 'settings page script served');
    }
    const save = await dsh.call('/auriga/settings/save', {
      method: 'POST',
      headers: {
        'content-type': 'application/json',
        'x-auriga-settings': '1',
      },
      body: JSON.stringify({
        platformOrigin: 'https://team.example.org',
        deployment: 'cloud',
      }),
    });
    check(
      save.status === 200,
      'configure platform address through plugin settings',
      `HTTP ${save.status}`,
    );
    writeFileSync(
      join(profile, 'auriga-connect', 'e2e-login-marker.txt'),
      'login-state-marker',
    );
    mkdirSync(join(dataHome, 'sessions'), { recursive: true });
    writeFileSync(
      join(dataHome, 'sessions', 'e2e-session.json'),
      '{"id":"e2e"}',
    );
    writeFileSync(join(sandbox, 'launch-old.log'), dsh.out());
  });

  // 5. broken upgrade must fail and leave the old version fully working
  if (brokenPkg) {
    const stash = join(sandbox, 'stash-dsh-bin.js');
    const victim = join(
      brokenPkg,
      'payload',
      'upstream',
      'app',
      'node_modules',
      '@deepseek-ai',
      'dsh',
      'lib',
      'bin.js',
    );
    renameSync(victim, stash);
    let failed;
    try {
      failed = install(brokenPkg);
    } finally {
      renameSync(stash, victim);
    }
    writeFileSync(
      join(sandbox, 'broken-upgrade.log'),
      `${failed.stdout}\n${failed.stderr}`,
    );
    check(failed.status !== 0, 'broken upgrade reports failure');
    const state = readJson(join(root, 'install.json'));
    check(
      state.current === manifestOld.id && state.previous === null,
      'pointers unchanged after failed upgrade',
    );
    const leftovers = run('cmd.exe', [
      '/c',
      'dir',
      '/b',
      join(root, 'versions'),
    ]);
    check(
      leftovers.stdout.trim().split(/\r?\n/).join(',') === manifestOld.id,
      'failed version directory removed',
      leftovers.stdout.trim(),
    );
    check(
      pluginVersion() === manifestOld.plugin.version &&
        readFileSync(
          join(profile, 'auriga-connect', 'e2e-login-marker.txt'),
          'utf8',
        ) === 'login-state-marker',
      'profile and data intact after failed upgrade',
    );
    await withDsh(async (dsh) =>
      pluginChecks(dsh, 'https://team.example.org', 'after-failed-upgrade'),
    );
  }

  if (newPkg) {
    // 6. upgrade
    const second = mustInstall(newPkg);
    const manifestNew = readJson(
      join(root, 'versions', second.id, 'manifest.json'),
    );
    check(
      second.upgraded === true && second.previous === manifestOld.id,
      'upgrade to new package',
      `${manifestOld.id} -> ${second.id}`,
    );
    const state = readJson(join(root, 'install.json'));
    check(
      state.current === manifestNew.id && state.previous === manifestOld.id,
      'current/previous pointers',
    );
    check(
      readFileSync(
        join(profile, 'auriga-connect', 'e2e-login-marker.txt'),
        'utf8',
      ) === 'login-state-marker',
      'login-state data preserved',
    );
    check(
      existsSync(join(dataHome, 'sessions', 'e2e-session.json')),
      'session data preserved',
    );
    const dshPkg = readJson(
      join(
        root,
        'versions',
        manifestNew.id,
        'upstream/app/node_modules/@deepseek-ai/dsh/package.json',
      ),
    );
    check(
      dshPkg.version === manifestNew.upstream.version,
      'upstream component replaced',
      dshPkg.version,
    );
    await withDsh(async (dsh) => {
      await pluginChecks(dsh, 'https://team.example.org', 'new');
      writeFileSync(join(sandbox, 'launch-new.log'), dsh.out());
    });
    // 7. rollback
    const rb = run(join(root, 'bin', 'rollback.cmd'), [], { shell: true });
    const crossesLayout =
      !scoped && manifestNew.plugin.scope === 'installation';
    check(
      crossesLayout
        ? rb.status !== 0 &&
            readJson(join(root, 'install.json')).current === manifestNew.id
        : rb.status === 0 &&
            readJson(join(root, 'install.json')).current === manifestOld.id,
      crossesLayout
        ? 'rollback into legacy mixed layout refused'
        : 'rollback to old version',
    );
    await withDsh(async (dsh) =>
      pluginChecks(dsh, 'https://team.example.org', 'rolled-back'),
    );
  }

  // 8. uninstall (keep data), then verify shell integration is gone
  const shellFile = join(root, 'shell.json');
  const foreignLink = join(startMenu, 'Another application.lnk');
  const foreignTarget = join(sandbox, 'another-app.cmd');
  const checksOwnership = existsSync(join(root, 'bin', 'maintenance.ps1'));
  if (checksOwnership) {
    writeFileSync(foreignTarget, '@exit /b 0\r\n');
    const created = shortcut(foreignLink, foreignTarget);
    check(
      created.status === 0,
      'create a foreign shortcut for the uninstall ownership check',
      created.stderr.trim(),
    );
    const shell = readJson(shellFile);
    // Simulate a recorded link whose real target changed after setup.
    shell.shortcuts = [...shell.shortcuts, foreignLink];
    writeFileSync(shellFile, JSON.stringify(shell));
    const powershell = join(
      process.env.SystemRoot,
      'System32',
      'WindowsPowerShell',
      'v1.0',
      'powershell.exe',
    );
    const startup = `"${powershell}" -NoProfile -ExecutionPolicy Bypass -WindowStyle Hidden -File "${join(root, 'bin', 'auriga-autostart.ps1')}"`;
    const saved = run('reg.exe', [
      'add',
      runPath,
      '/v',
      registryKey,
      '/t',
      'REG_SZ',
      '/d',
      startup,
      '/f',
    ]);
    const found = run('reg.exe', ['query', runPath, '/v', registryKey]);
    check(
      saved.status === 0 &&
        found.status === 0 &&
        found.stdout.includes(startup),
      'save this installation’s stable autostart value',
      saved.stderr.trim() || found.stderr.trim(),
    );
  }
  const active = await startTrayForUninstall(
    readFileSync(join(root, 'current.txt'), 'utf8').trim(),
  );
  const un = run('powershell.exe', [
    '-NoProfile',
    '-ExecutionPolicy',
    'Bypass',
    '-File',
    join(root, 'bin', 'uninstall.ps1'),
    '-Quiet',
  ]);
  check(
    un.status === 0 && !existsSync(root),
    'uninstall removes install root',
    un.stdout.trim(),
  );
  if (active) {
    const { first, processTree } = active;
    check(
      [
        first.pid,
        processTree.launcher.pid,
        processTree.root.pid,
        ...processTree.children.map(({ pid }) => pid),
      ].every((pid) => !running(pid)),
      'uninstall stops the running tray, launcher and DSH tree',
    );
  }
  check(
    !existsSync(join(startMenu, 'Auriga DSH.lnk')) &&
      !existsSync(join(startMenu, '卸载 Auriga DSH.lnk')) &&
      !existsSync(join(desktop, 'Auriga DSH.lnk')) &&
      regQuery().status !== 0,
    'uninstall removes shortcuts and registry entry',
  );
  if (checksOwnership) {
    checkShortcut(
      'uninstall leaves a recorded link owned by another installation',
      foreignLink,
      foreignTarget,
    );
    check(
      run('reg.exe', ['query', runPath, '/v', registryKey]).status !== 0,
      'uninstall removes this installation’s stable autostart value',
    );
  }
  check(
    existsSync(join(dataHome, 'sessions', 'e2e-session.json')),
    'uninstall keeps user data by default',
  );

  // 9. layout guard: a data folder inside the install folder is refused
  if (newPkg) {
    const guarded = join(sandbox, 'guarded');
    const refused = run('powershell.exe', [
      '-NoProfile',
      '-ExecutionPolicy',
      'Bypass',
      '-File',
      join(newPkg, 'install.ps1'),
      '-InstallDir',
      guarded,
      '-DataHome',
      join(guarded, 'home'),
    ]);
    check(
      refused.status !== 0 &&
        !existsSync(guarded) &&
        /inside the install directory/.test(refused.stdout + refused.stderr),
      'install refuses a data folder inside the install folder',
    );
  }
  writeFileSync(
    join(sandbox, 'result.json'),
    JSON.stringify({ ok: true, steps }, null, 2),
  );
} catch (error) {
  writeFileSync(
    join(sandbox, 'result.json'),
    JSON.stringify({ ok: false, error: String(error), steps }, null, 2),
  );
  process.exitCode = 1;
  console.error(error);
} finally {
  for (const c of started)
    if (c.exitCode === null)
      spawnSync('taskkill', ['/PID', String(c.pid), '/T', '/F']);
  spawnSync('reg.exe', ['delete', registryPath, '/f'], { stdio: 'ignore' });
  spawnSync('reg.exe', ['delete', runPath, '/v', registryKey, '/f'], {
    stdio: 'ignore',
  });
  if (args.clean === 'true') rmSync(sandbox, { recursive: true, force: true });
}
