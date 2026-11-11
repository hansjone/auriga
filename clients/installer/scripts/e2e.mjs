/**
 * Isolated end-to-end check of built packages (Windows).
 *
 *   node scripts/e2e.mjs --old <package dir|setup.exe> --sandbox <dir>
 *        [--new <package dir>] [--broken <package dir>] [--expect-origin <origin>]
 *
 * Installs into a sandbox (install root, DSH_HOME, USERPROFILE/APPDATA, start
 * menu and desktop folders and a test-only registry key all live under or are
 * named for --sandbox), starts DSH on a random port, checks the plugin, shell
 * integration and pnpm, tries a broken upgrade (must roll back), upgrades,
 * rolls back and uninstalls. Every started process is killed at the end and
 * the test registry key is removed.
 */
import { spawn, spawnSync } from 'node:child_process';
import {
  existsSync,
  mkdirSync,
  readFileSync,
  renameSync,
  rmSync,
  writeFileSync,
} from 'node:fs';
import { createServer } from 'node:net';
import { join, resolve } from 'node:path';
import { launchEnv } from '../runtime/core.mjs';

const args = Object.fromEntries(
  process.argv.slice(2).reduce((acc, v, i, all) => {
    if (v.startsWith('--')) acc.push([v.slice(2), all[i + 1]]);
    return acc;
  }, []),
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
const registryKey = 'AurigaDesktop-e2e-test';
const registryPath = `HKCU\\Software\\Microsoft\\Windows\\CurrentVersion\\Uninstall\\${registryKey}`;
const env = {
  ...process.env,
  USERPROFILE: join(sandbox, 'home'),
  HOME: join(sandbox, 'home'),
  APPDATA: join(sandbox, 'appdata'),
  LOCALAPPDATA: join(sandbox, 'local'),
  AURIGA_INSTALL_DIR: root,
  AURIGA_DATA_HOME: dataHome,
  AURIGA_START_MENU_DIR: startMenu,
  AURIGA_DESKTOP_DIR: desktop,
  AURIGA_REGISTRY_KEY: registryKey,
};
delete env.DSH_HOME;
delete env.AURIGA_PLATFORM_ORIGIN;
for (const d of ['home', 'appdata', 'local'])
  mkdirSync(join(sandbox, d), { recursive: true });

const steps = [];
const note = (name, detail) => {
  steps.push({ name, ok: true, detail });
  console.log(`ok   ${name}${detail ? ` - ${detail}` : ''}`);
};
function check(condition, name, detail) {
  if (!condition) {
    console.log(`FAIL ${name}${detail ? ` - ${detail}` : ''}`);
    throw new Error(`check failed: ${name}`);
  }
  note(name, detail);
}
const readJson = (f) => JSON.parse(readFileSync(f, 'utf8'));
const run = (cmd, argv, extra = {}) =>
  spawnSync(cmd, argv, {
    env,
    cwd: sandbox,
    encoding: 'utf8',
    timeout: 1800000,
    ...extra,
  });
const regQuery = () => run('reg.exe', ['query', registryPath]);

/** Install (or upgrade) from a package directory or a single-file setup exe. */
function install(pkg, extra = []) {
  const r = pkg.endsWith('.exe')
    ? run(pkg, [])
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
  return JSON.parse(r.stdout.split('\n').find((l) => l.startsWith('{')));
}

async function freePort() {
  return new Promise((ok) => {
    const s = createServer().listen(0, '127.0.0.1', () => {
      const { port } = s.address();
      s.close(() => ok(port));
    });
  });
}

const started = [];
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

const pluginVersion = () =>
  readJson(
    join(
      dataHome,
      'profiles',
      'auriga',
      'node_modules',
      'auriga-dsh',
      'package.json',
    ),
  ).version;

try {
  // 1. install the old package (zip directory or single-file exe)
  const first = mustInstall(oldPkg);
  check(first.upgraded === false, 'install old package', first.id);
  const manifestOld = readJson(
    join(root, 'versions', first.id, 'manifest.json'),
  );
  const profile = join(dataHome, 'profiles', 'auriga');
  const versionDir = join(root, 'versions', first.id);
  check(
    existsSync(join(profile, 'node_modules', 'auriga-dsh', 'package.json')),
    'plugin copied into profile',
  );

  // 2. shell integration: shortcuts and Apps & features entry (current user)
  check(
    existsSync(join(startMenu, 'Auriga Desktop.lnk')) &&
      existsSync(join(desktop, 'Auriga Desktop.lnk')),
    'start menu and desktop shortcuts created',
  );
  const reg = regQuery();
  check(
    reg.status === 0 &&
      /DisplayName\s+REG_SZ\s+Auriga Desktop/.test(reg.stdout) &&
      /UninstallString\s+REG_SZ\s+.*uninstall\.ps1/.test(reg.stdout),
    'HKCU uninstall entry written',
    registryKey,
  );

  // 3. bundled pnpm on the launch PATH
  const launchEnvironment = launchEnv(versionDir, dataHome, env);
  const pnpm = spawnSync('pnpm', ['--version'], {
    env: launchEnvironment,
    cwd: sandbox,
    encoding: 'utf8',
    shell: true,
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
    }),
  );
  writeFileSync(join(tiny, 'index.js'), 'module.exports = {};\n');
  const pack = spawnSync('npm', ['pack', '--silent'], {
    cwd: tiny,
    env,
    encoding: 'utf8',
    shell: true,
  });
  const tgz = join(tiny, pack.stdout.trim().split(/\r?\n/).pop());
  const added = spawnSync(
    join(versionDir, 'upstream', 'node', 'node.exe'),
    [dshBin, 'plugin', '--profile', 'auriga', 'add', tgz],
    { env: launchEnvironment, cwd: sandbox, encoding: 'utf8', timeout: 600000 },
  );
  writeFileSync(join(sandbox, 'plugin-add.log'), added.stdout + added.stderr);
  check(
    added.status === 0 &&
      existsSync(join(profile, 'node_modules', 'e2e-tiny-plugin')) &&
      existsSync(join(profile, 'node_modules', 'auriga-dsh', 'package.json')),
    'dsh plugin add through bundled pnpm keeps the Auriga plugin',
    String(added.status),
  );

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
    check(
      rb.status === 0 &&
        readFileSync(join(root, 'current.txt'), 'utf8').trim() ===
          manifestOld.id,
      'rollback to old version',
      rb.stdout.trim(),
    );
    await withDsh(async (dsh) =>
      pluginChecks(dsh, 'https://team.example.org', 'rolled-back'),
    );
  }

  // 8. uninstall (keep data), then verify shell integration is gone
  const un = run('powershell.exe', [
    '-NoProfile',
    '-ExecutionPolicy',
    'Bypass',
    '-File',
    join(root, 'bin', 'uninstall.ps1'),
  ]);
  check(
    un.status === 0 && !existsSync(root),
    'uninstall removes install root',
    un.stdout.trim(),
  );
  check(
    !existsSync(join(startMenu, 'Auriga Desktop.lnk')) &&
      !existsSync(join(desktop, 'Auriga Desktop.lnk')) &&
      regQuery().status !== 0,
    'uninstall removes shortcuts and registry entry',
  );
  check(
    existsSync(join(dataHome, 'sessions', 'e2e-session.json')),
    'uninstall keeps user data by default',
  );
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
    spawnSync('taskkill', ['/PID', String(c.pid), '/T', '/F']);
  spawnSync('reg.exe', ['delete', registryPath, '/f'], { stdio: 'ignore' });
  if (args.clean === 'true') rmSync(sandbox, { recursive: true, force: true });
}
