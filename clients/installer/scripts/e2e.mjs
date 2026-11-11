/**
 * Isolated end-to-end check of built packages (Windows).
 *
 *   node scripts/e2e.mjs --old <package dir> [--new <package dir>] --sandbox <dir>
 *
 * Installs into a sandbox (install root, DSH_HOME and USERPROFILE/APPDATA all
 * under --sandbox), starts DSH on a random port, checks that the plugin is
 * loaded and configurable, upgrades to --new keeping data, rolls back and
 * uninstalls. Every started process is killed at the end.
 */
import { spawn, spawnSync } from 'node:child_process';
import { existsSync, mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { createServer } from 'node:net';
import { join, resolve } from 'node:path';

const args = Object.fromEntries(
  process.argv.slice(2).reduce((acc, v, i, all) => {
    if (v.startsWith('--')) acc.push([v.slice(2), all[i + 1]]);
    return acc;
  }, []),
);
const sandbox = resolve(args.sandbox);
const oldPkg = resolve(args.old);
const newPkg = args.new ? resolve(args.new) : null;
const root = join(sandbox, 'root');
const dataHome = join(sandbox, 'data');
const env = {
  ...process.env,
  USERPROFILE: join(sandbox, 'home'),
  HOME: join(sandbox, 'home'),
  APPDATA: join(sandbox, 'appdata'),
  LOCALAPPDATA: join(sandbox, 'local'),
};
delete env.DSH_HOME;
for (const d of ['home', 'appdata', 'local']) mkdirSync(join(sandbox, d), { recursive: true });

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
const powershell = (file, extra) =>
  spawnSync(
    'powershell.exe',
    ['-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', file, ...extra],
    { env, cwd: sandbox, encoding: 'utf8', timeout: 900000 },
  );

function install(pkg, extra = []) {
  const r = powershell(join(pkg, 'install.ps1'), ['-InstallDir', root, '-DataHome', dataHome, ...extra]);
  if (r.status !== 0) throw new Error(`install failed: ${r.stdout}\n${r.stderr}`);
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
  const child = spawn('cmd.exe', ['/c', join(root, 'bin', 'auriga-dsh.cmd'), '--port', String(port), '--no-open'], { env });
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
    const login = await globalThis.fetch(`${base}/?token=${token}`, { redirect: 'manual' });
    const cookie = login.headers.getSetCookie().map((c) => c.split(';')[0]).join('; ');
    const call = (path, init = {}) =>
      globalThis.fetch(base + path, { ...init, headers: { cookie, origin: base, ...init.headers }, redirect: 'manual' });
    return await fn({ base, call, out: () => out.replace(/token=[A-Za-z0-9_-]+/g, 'token=REDACTED') });
  } finally {
    spawnSync('taskkill', ['/PID', String(child.pid), '/T', '/F']);
  }
}

async function pluginChecks({ base, call }, expectedOrigin, label) {
  const anon = await globalThis.fetch(`${base}/auriga/`, { redirect: 'manual' });
  const missing = await globalThis.fetch(`${base}/nonexistent-zzz`, { redirect: 'manual' });
  check(anon.status === 401 && missing.status === 404, `${label}: plugin route registered`, `/auriga/=${anon.status}, unknown=${missing.status}`);
  const state = await (await call('/auriga/settings/state')).json();
  check(state.mode === 'connect' && state.platformOrigin === expectedOrigin, `${label}: plugin enabled, platform address`, JSON.stringify(state));
}

try {
  // 1. install the old package
  const first = install(oldPkg, ['-PlatformOrigin', 'https://portal.example.com']);
  check(first.upgraded === false, 'install old package', first.id);
  const manifestOld = readJson(join(oldPkg, 'payload', 'manifest.json'));
  const profile = join(dataHome, 'profiles', 'auriga');
  check(existsSync(join(profile, 'node_modules', 'auriga-dsh', 'package.json')), 'plugin copied into profile');

  await withDsh(async (dsh) => {
    await pluginChecks(dsh, 'https://portal.example.com', 'old');
    const save = await dsh.call('/auriga/settings/save', {
      method: 'POST',
      headers: { 'content-type': 'application/json', 'x-auriga-settings': '1' },
      body: JSON.stringify({ platformOrigin: 'https://team.example.org', deployment: 'cloud' }),
    });
    check(save.status === 200, 'configure platform address through plugin settings', `HTTP ${save.status}`);
    writeFileSync(join(profile, 'auriga-connect', 'e2e-login-marker.txt'), 'login-state-marker');
    mkdirSync(join(dataHome, 'sessions'), { recursive: true });
    writeFileSync(join(dataHome, 'sessions', 'e2e-session.json'), '{"id":"e2e"}');
    writeFileSync(join(sandbox, 'launch-old.log'), dsh.out());
  });

  if (newPkg) {
    // 2. upgrade
    const second = install(newPkg);
    const manifestNew = readJson(join(newPkg, 'payload', 'manifest.json'));
    check(second.upgraded === true && second.previous === manifestOld.id, 'upgrade to new package', `${manifestOld.id} -> ${second.id}`);
    const state = readJson(join(root, 'install.json'));
    check(state.current === manifestNew.id && state.previous === manifestOld.id, 'current/previous pointers');
    check(readFileSync(join(profile, 'auriga-connect', 'e2e-login-marker.txt'), 'utf8') === 'login-state-marker', 'login-state data preserved');
    check(existsSync(join(dataHome, 'sessions', 'e2e-session.json')), 'session data preserved');
    const dshPkg = readJson(join(root, 'versions', manifestNew.id, 'upstream/app/node_modules/@deepseek-ai/dsh/package.json'));
    check(dshPkg.version === manifestNew.upstream.version, 'upstream component replaced', dshPkg.version);
    await withDsh(async (dsh) => {
      await pluginChecks(dsh, 'https://team.example.org', 'new');
      writeFileSync(join(sandbox, 'launch-new.log'), dsh.out());
    });
    // 3. rollback
    const rb = spawnSync(join(root, 'bin', 'rollback.cmd'), [], { env, encoding: 'utf8', shell: true, timeout: 600000 });
    check(rb.status === 0 && readFileSync(join(root, 'current.txt'), 'utf8').trim() === manifestOld.id, 'rollback to old version', rb.stdout.trim());
    await withDsh(async (dsh) => pluginChecks(dsh, 'https://team.example.org', 'rolled-back'));
  }

  // 4. uninstall (keep data), then verify
  const un = powershell(join(root, 'bin', 'uninstall.ps1'), []);
  check(un.status === 0 && !existsSync(root), 'uninstall removes install root', un.stdout.trim());
  check(existsSync(join(dataHome, 'sessions', 'e2e-session.json')), 'uninstall keeps user data by default');
  writeFileSync(join(sandbox, 'result.json'), JSON.stringify({ ok: true, steps }, null, 2));
} catch (error) {
  writeFileSync(join(sandbox, 'result.json'), JSON.stringify({ ok: false, error: String(error), steps }, null, 2));
  process.exitCode = 1;
  console.error(error);
} finally {
  for (const c of started) spawnSync('taskkill', ['/PID', String(c.pid), '/T', '/F']);
  if (args.clean === 'true') rmSync(sandbox, { recursive: true, force: true });
}
