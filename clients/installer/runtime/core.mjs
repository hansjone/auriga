/* global setInterval, clearInterval */
import { BRAND_NAME } from './brand.mjs';
import { updateOrigin, normalizePlatformBinding } from './platform-origin.mjs';
import {
  cleanupDownloads,
  holdsTransfer,
  transferLock,
  withUpdateStateLock,
} from './update-state.mjs';
import { createRequire } from 'node:module';
import { upgradeSkillPatch, yamlParser } from './skill-config.mjs';
import {
  ProcessTree,
  processIdentity,
  sameProcess,
  assertNTFS,
  notifyPrivateOnly,
  notifySharedRestart,
} from './process-tree.mjs';
import { Supervisor, packagedTrust } from './supervisor.mjs';
import { superviseDsh } from './desktop-supervision.mjs';
import { startShellAuth } from './shell-auth.mjs';
import { assertLocalPath } from './host-lock.mjs';
import { pathOperationLock, processAlive } from './operation-lock.mjs';
/**
 * Installer core: pure Node (no dependencies) so it runs under the bundled
 * Node runtime on any platform and under plain `node --test`.
 *
 * Layout under <root> (the install directory, owned via a marker file):
 *   .auriga-desktop-install  ownership marker; uninstall only removes known entries
 *   versions/<id>/           immutable component set (upstream/ + tools/ + auriga/)
 *   install.json             { dataHome, current, previous }  (authoritative pointer)
 *   current.txt              id of the active version (plain text for the .cmd launcher)
 *   bin/                     launchers of the active version
 *   txn.json                 present only while a change is in flight (crash recovery)
 * User data lives in <dataHome>, never inside <root> and never deleted by default.
 *
 * Every change (install, upgrade, manual rollback) is a transaction: the
 * profile is snapshotted first, a journal is written, and any failure or
 * crash before the pointers are committed restores the pre-change state.
 */
import {
  cpSync,
  existsSync,
  mkdirSync,
  openSync,
  closeSync,
  readdirSync,
  readFileSync,
  realpathSync,
  renameSync,
  rmSync,
  rmdirSync,
  statSync,
  writeFileSync,
} from 'node:fs';
import {
  delimiter,
  dirname,
  isAbsolute,
  join,
  parse as parsePath,
  relative,
  resolve,
  sep,
} from 'node:path';
import { spawnSync, spawn } from 'node:child_process';
import { randomUUID } from 'node:crypto';
import { verifyPackage } from './verify.mjs';

export const PROFILE = 'auriga';
const PLUGIN = 'auriga-dsh';
const CLIENT_ID = 'auriga-dsh';
const PROVIDER_ID = 'discover';
// The profile points at a tarball inside itself so the bundled pnpm can
// reinstall the plugin (it is not on the npm registry) without breaking it.
const PLUGIN_SPEC = 'file:./.auriga/auriga-dsh.tgz';
const BUNDLES = ['@deepseek-ai/dsh-base', '@deepseek-ai/dsh-web-app', PLUGIN];
const INSTALL_MARKER = '.auriga-desktop-install';
const UNINSTALL_MARKER = '.auriga-desktop-uninstall-pending';
const DATA_MARKER = '.auriga-desktop';
const DATA_OWNER = '.auriga-desktop-data-owner';
/** Everything uninstall may remove from the install directory. */
const OWNED_ENTRIES = [
  'versions',
  'bin',
  'bin.prev',
  'bin.prev.partial',
  'current.txt',
  'install.json',
  'shell.json',
  'txn.json',
  'desktop-update.json',
  'desktop-update.json.tmp',
  'desktop-update.log',
  'desktop-pending',
  '.desktop-resume',
  '.desktop-update-lock',
  'desktop-origin.json',
  'desktop-tray.json',
  'desktop-tray.json.tmp',
  'shell.json.tmp',
];

export class CrashError extends Error {}

const readJson = (file) => JSON.parse(readFileSync(file, 'utf8'));

/** Write via a temp file and rename so readers never see a partial file. */
function writeAtomic(file, content) {
  const tmp = `${file}.tmp`;
  writeFileSync(tmp, content);
  renameSync(tmp, file);
}
const writeJsonAtomic = (file, value) =>
  writeAtomic(file, JSON.stringify(value, null, 2) + '\n');

export function versionDir(root, id) {
  return join(root, 'versions', id);
}

export function readState(root) {
  const file = join(root, 'install.json');
  return existsSync(file) ? readJson(file) : null;
}

function writeState(root, state) {
  // install.json is authoritative; current.txt is the launcher's plain-text
  // copy. If a crash separates the two writes, recover() sees the journal and
  // restores the previous state.
  writeJsonAtomic(join(root, 'install.json'), state);
  writeAtomic(join(root, 'current.txt'), state.current + '\n');
}

export function profileDir(dataHome) {
  return join(dataHome, 'profiles', PROFILE);
}

function backupsDir(dataHome) {
  return join(dataHome, 'profile-backups', PROFILE);
}

/** Resolve the upstream `dsh` bin script inside a version directory. */
export function dshBin(dir) {
  const pkgDir = join(
    dir,
    'upstream',
    'app',
    'node_modules',
    '@deepseek-ai',
    'dsh',
  );
  const pkg = readJson(join(pkgDir, 'package.json'));
  const bin = typeof pkg.bin === 'string' ? pkg.bin : pkg.bin?.dsh;
  if (!bin) throw new Error('upstream dsh package declares no dsh bin');
  return join(pkgDir, bin);
}

export function nodeBin(dir) {
  return join(
    dir,
    'upstream',
    'node',
    process.platform === 'win32' ? 'node.exe' : 'bin/node',
  );
}

// ---------------------------------------------------------------- layout --

const norm = (p) =>
  process.platform === 'win32' ? resolve(p).toLowerCase() : resolve(p);

function isInside(parent, child) {
  const rel = relative(norm(parent), norm(child));
  return (
    rel === '' ||
    (rel !== '..' && !rel.startsWith('..' + sep) && !isAbsolute(rel))
  );
}

/**
 * Install directory and data directory must be separate trees, and an
 * existing install directory must be empty or already ours.
 */
export function assertLayout(root, dataHome) {
  assertLocalPath(root);
  assertLocalPath(dataHome);
  if (parsePath(root).root === resolve(root))
    throw new Error('install directory must not be a drive or filesystem root');
  if (isInside(root, dataHome))
    throw new Error('data directory must not be inside the install directory');
  if (isInside(dataHome, root))
    throw new Error('install directory must not be inside the data directory');
  if (
    dataHome
      .split(/[\\/]/)
      .some((part) =>
        /^\.(?:uninstall|auriga-data-purge)-[0-9a-f-]{36}$/i.test(part),
      )
  )
    throw new Error('data directory may not be an uninstall quarantine');
  for (let path = dataHome; path !== dirname(path); path = dirname(path))
    if (
      existsSync(join(path, INSTALL_MARKER)) ||
      existsSync(join(path, UNINSTALL_MARKER))
    )
      throw new Error('data directory may not be an installation directory');
  if (existsSync(root)) {
    const entries = readdirSync(root);
    if (entries.includes(UNINSTALL_MARKER))
      throw new Error('卸载清理尚未完成，请重新登录 Windows 后再安装。');
    if (entries.length > 0 && !entries.includes(INSTALL_MARKER))
      throw new Error(
        `install directory is not empty and is not an ${BRAND_NAME} installation`,
      );
  }
}

function assertOwned(root) {
  if (!existsSync(join(root, INSTALL_MARKER)))
    throw new Error(`not an ${BRAND_NAME} installation: ${root}`);
}

// --------------------------------------------------------------- profile --

/** Seed profile manifest and (only when absent) the patch with the platform address. */
function seedProfile(dataHome, { platformOrigin, target }) {
  const dir = profileDir(dataHome);
  mkdirSync(join(dir, 'node_modules'), { recursive: true });
  writeFileSync(join(dir, DATA_MARKER), 'Auriga Desktop profile\n');
  const manifestFile = join(dir, 'package.json');
  const existing = existsSync(manifestFile) ? readJson(manifestFile) : {};
  const bundles = [...(existing.dsh?.profile?.bundles ?? [])];
  for (const b of BUNDLES) if (!bundles.includes(b)) bundles.push(b);
  writeFileSync(
    manifestFile,
    JSON.stringify(
      {
        ...existing,
        name: existing.name ?? 'auriga-desktop-profile',
        private: true,
        dependencies: { ...existing.dependencies, [PLUGIN]: PLUGIN_SPEC },
        dsh: {
          ...existing.dsh,
          profile: { ...existing.dsh?.profile, bundles },
        },
      },
      null,
      2,
    ) + '\n',
  );
  const patch = join(dir, 'cordis.patch.yml');
  if (!existsSync(patch)) {
    const rows = ['- id: auriga-dsh', '  disabled: false'];
    if (platformOrigin) {
      const origin = new URL(platformOrigin).origin;
      rows.push(
        // The loader row must be complete: with only platformOrigin the plugin
        // rejects the config (issuer/clientId/providerId required) and the
        // login runtime never starts.
        `  config: ${JSON.stringify({ connect: { ...(origin.startsWith('http:') ? { allowLocalHttp: true } : {}), platformOrigin: origin, issuer: origin, clientId: CLIENT_ID, providerId: PROVIDER_ID } })}`,
      );
    } else {
      rows.push('  config: {}');
    }
    writeFileSync(patch, rows.join('\n') + '\n');
  }
  // Use the locked upstream parser, preserving !!js as inert scalar text.
  // Invalid/conflicting patches keep their original bytes and cannot enable sharing.
  try {
    const yaml = createRequire(join(target, 'upstream', 'app', 'package.json'))(
      'yaml',
    );
    upgradeSkillPatch(patch, yamlParser(yaml), dataHome);
  } catch (error) {
    console.error(`${BRAND_NAME} 共享技能配置未启用：${error.message}`);
  }
}

function assertGuardianCompatible(target, dataHome) {
  const stateFile = join(dataHome, 'auriga', 'guardian', 'state.json');
  if (
    !existsSync(stateFile) ||
    existsSync(join(target, 'auriga', 'runtime', 'supervisor.mjs'))
  )
    return;
  const state = readJson(stateFile);
  const current = join(dataHome, 'auriga', 'skills-active', 'current');
  if (
    state.guardian?.state !== 'private_only' ||
    state.pendingIsolation ||
    (existsSync(current) && readdirSync(current).length)
  )
    throw new Error(
      '目标版本缺少可信 guardian；请先停用并隔离共享技能再回滚。',
    );
}

const copyTree = (from, to) =>
  cpSync(from, to, { recursive: true, verbatimSymlinks: true });

/**
 * Snapshot the whole profile. The snapshot only counts once its `.complete`
 * marker exists and the directory has its final name.
 */
function snapshotProfile(dataHome, label) {
  const dir = profileDir(dataHome);
  const base = backupsDir(dataHome);
  const finalDir = join(base, label);
  const partial = `${finalDir}.partial`;
  mkdirSync(base, { recursive: true });
  writeFileSync(join(base, DATA_MARKER), 'Auriga Desktop backups\n');
  rmSync(partial, { recursive: true, force: true });
  copyTree(dir, join(partial, 'profile'));
  writeFileSync(join(partial, '.complete'), label);
  rmSync(finalDir, { recursive: true, force: true });
  renameSync(partial, finalDir);
}

function completeSnapshot(dataHome, label) {
  const dir = join(backupsDir(dataHome), label);
  return existsSync(join(dir, '.complete')) ? join(dir, 'profile') : null;
}

/** Put the whole profile back exactly as it was in the snapshot. */
function restoreProfile(dataHome, label) {
  const source = completeSnapshot(dataHome, label);
  if (!source) throw new Error(`no complete profile backup for ${label}`);
  const dir = profileDir(dataHome);
  rmSync(dir, { recursive: true, force: true });
  copyTree(source, dir);
}

/** Restore only the plugin-related parts (manual rollback keeps newer user settings). */
function restorePluginParts(dataHome, label) {
  const source = completeSnapshot(dataHome, label);
  if (!source) return false;
  const dir = profileDir(dataHome);
  rmSync(join(dir, 'node_modules'), { recursive: true, force: true });
  rmSync(join(dir, '.auriga'), { recursive: true, force: true });
  cpSync(join(source, 'package.json'), join(dir, 'package.json'));
  for (const part of ['node_modules', '.auriga'])
    if (existsSync(join(source, part)))
      copyTree(join(source, part), join(dir, part));
  return true;
}

/** Copy the plugin closure into the profile. */
function syncPlugin(dir, dataHome) {
  const profile = profileDir(dataHome);
  const modules = join(profile, 'node_modules');
  const tarball = join(dir, 'auriga', 'plugin', 'auriga-dsh.tgz');
  if (existsSync(tarball)) {
    mkdirSync(join(profile, '.auriga'), { recursive: true });
    cpSync(tarball, join(profile, '.auriga', 'auriga-dsh.tgz'));
  }
  const source = join(dir, 'auriga', 'plugin', 'node_modules');
  mkdirSync(modules, { recursive: true });
  for (const entry of readdirSync(source)) {
    if (entry === '.package-lock.json') continue;
    if (entry.startsWith('@')) {
      for (const sub of readdirSync(join(source, entry))) {
        const target = join(modules, entry, sub);
        rmSync(target, { recursive: true, force: true });
        mkdirSync(join(modules, entry), { recursive: true });
        copyTree(join(source, entry, sub), target);
      }
    } else {
      const target = join(modules, entry);
      rmSync(target, { recursive: true, force: true });
      copyTree(join(source, entry), target);
    }
  }
}

// ----------------------------------------------------------------- smoke --

/** Environment for upstream DSH: own data home; bundled node and pnpm first on PATH. */
export function launchEnv(dir, dataHome, base = process.env) {
  const pathKey =
    Object.keys(base).find((k) => k.toLowerCase() === 'path') ?? 'PATH';
  const extra = [join(dir, 'tools', 'bin'), dirname(nodeBin(dir))];
  return {
    ...base,
    DSH_HOME: dataHome,
    // Keep the pnpm package store (plugin manager) with the user data, not the system.
    npm_config_store_dir: join(dataHome, 'pnpm-store'),
    [pathKey]: [...extra, base[pathKey]].filter(Boolean).join(delimiter),
  };
}

/** Default smoke check: the upstream CLI must load the profile with the plugin row. */
export function defaultSmoke(dir, dataHome) {
  const result = spawnSync(
    nodeBin(dir),
    [dshBin(dir), '--profile', PROFILE, '--dump-config'],
    { env: launchEnv(dir, dataHome), encoding: 'utf8', timeout: 120000 },
  );
  if (result.status !== 0)
    throw Object.assign(
      new Error(
        `smoke failed (exit ${result.status}): ${(result.stderr || result.stdout || '').slice(-600)}`,
      ),
      { code: 'desktop.smoke_failed' },
    );
  if (!/auriga-dsh/.test(result.stdout))
    throw Object.assign(
      new Error('smoke failed: effective config does not list auriga-dsh'),
      { code: 'desktop.smoke_failed' },
    );
}

// ----------------------------------------------------------- transaction --

const txnFile = (root) => join(root, 'txn.json');

/**
 * Launchers are never swapped as a directory: that would leave a window with
 * no entry points. They are version-independent scripts that read current.txt,
 * so each file is replaced atomically on its own and the old set is kept in
 * bin.prev until the change is final.
 */
function writeLaunchers(root, dir, fault) {
  const source = join(dir, 'auriga', 'launcher');
  if (!existsSync(source)) return;
  const bin = join(root, 'bin');
  const prev = join(root, 'bin.prev');
  mkdirSync(bin, { recursive: true });
  rmSync(`${prev}.partial`, { recursive: true, force: true });
  rmSync(prev, { recursive: true, force: true });
  mkdirSync(`${prev}.partial`);
  for (const name of readdirSync(bin))
    cpSync(join(bin, name), join(`${prev}.partial`, name));
  renameSync(`${prev}.partial`, prev);
  const keepMaintenance =
    existsSync(join(bin, 'maintenance.ps1')) &&
    !existsSync(join(source, 'maintenance.ps1'));
  const maintenance = [
    'uninstall.cmd',
    'uninstall.ps1',
    'rollback.cmd',
    'rollback.ps1',
    'maintenance.ps1',
    'shell-integration.ps1',
    'DesktopShortcut.cs',
    'uninstall-cleanup.ps1',
    'auriga-autostart.ps1',
  ];
  for (const name of readdirSync(source)) {
    if (keepMaintenance && maintenance.includes(name)) continue;
    writeAtomic(join(bin, name), readFileSync(join(source, name)));
    fault('launcher-file');
  }
}

function restoreLaunchers(root) {
  const prev = join(root, 'bin.prev');
  rmSync(`${prev}.partial`, { recursive: true, force: true });
  if (!existsSync(prev)) return;
  const bin = join(root, 'bin');
  const old = new Set(readdirSync(prev));
  for (const name of existsSync(bin) ? readdirSync(bin) : [])
    if (!old.has(name)) rmSync(join(bin, name), { force: true });
  mkdirSync(bin, { recursive: true });
  for (const name of old)
    writeAtomic(join(bin, name), readFileSync(join(prev, name)));
}

/** Put everything back as it was before the transaction started. */
function abort(root, dataHome, txn) {
  restoreLaunchers(root);
  if (txn.profileExisted) restoreProfile(dataHome, txn.label);
  else rmSync(profileDir(dataHome), { recursive: true, force: true });
  if (txn.from) writeState(root, { dataHome, ...txn.from });
  else {
    rmSync(join(root, 'install.json'), { force: true });
    rmSync(join(root, 'current.txt'), { force: true });
    rmSync(join(root, 'bin'), { recursive: true, force: true });
  }
  rmSync(join(root, 'bin.prev'), { recursive: true, force: true });
  if (txn.freshVersion)
    rmSync(versionDir(root, txn.to), { recursive: true, force: true });
  rmSync(txnFile(root), { force: true });
}

function finish(root) {
  rmSync(join(root, 'bin.prev'), { recursive: true, force: true });
  rmSync(join(root, 'bin.prev.partial'), { recursive: true, force: true });
  rmSync(txnFile(root), { force: true });
  try {
    const state = readState(root);
    const keep = new Set([state.current, state.previous].filter(Boolean));
    for (const entry of readdirSync(join(root, 'versions')))
      if (!keep.has(entry))
        rmSync(join(root, 'versions', entry), { recursive: true, force: true });
  } catch {
    // Pruning is best effort and happens after the change is final.
  }
}

/**
 * Finish or undo a change that was interrupted (crash, power loss).
 * Called at the start of every command.
 */
export function recover(root) {
  root = resolve(root);
  if (!existsSync(txnFile(root))) return false;
  const txn = readJson(txnFile(root));
  const state = readState(root);
  const currentTxt = existsSync(join(root, 'current.txt'))
    ? readFileSync(join(root, 'current.txt'), 'utf8').trim()
    : null;
  if (
    txn.phase === 'commit' &&
    state?.current === txn.to &&
    currentTxt === txn.to
  ) {
    finish(root);
    return true;
  }
  abort(root, txn.dataHome, txn);
  return true;
}

/**
 * Run one change as a transaction. `apply` mutates the profile and runs the
 * smoke check; it is undone by `abort` on any failure.
 */
function transition({
  root,
  dataHome,
  from,
  toId,
  fresh,
  target,
  apply,
  fault,
  dataOwner = from?.dataOwner,
}) {
  const profile = profileDir(dataHome);
  const profileExisted = existsSync(profile);
  const label = from?.current ?? 'initial';
  if (profileExisted) snapshotProfile(dataHome, label);
  const txn = {
    phase: 'prepare',
    dataHome,
    from: from
      ? {
          current: from.current,
          previous: from.previous ?? null,
          ...(from.dataOwner && { dataOwner: from.dataOwner }),
        }
      : null,
    to: toId,
    label,
    profileExisted,
    freshVersion: fresh,
  };
  writeJsonAtomic(txnFile(root), txn);
  try {
    fault('after-backup');
    apply();
    fault('after-apply');
    writeJsonAtomic(txnFile(root), { ...txn, phase: 'commit' });
    writeLaunchers(root, target, fault);
    fault('after-launchers');
    const previous =
      from && from.current !== toId ? from.current : (from?.previous ?? null);
    writeJsonAtomic(join(root, 'install.json'), {
      dataHome,
      current: toId,
      previous,
      ...(dataOwner && { dataOwner }),
    });
    fault('after-install-json');
    writeAtomic(join(root, 'current.txt'), toId + '\n');
    fault('after-current-txt');
  } catch (error) {
    if (!(error instanceof CrashError)) abort(root, dataHome, txn);
    throw error;
  }
  finish(root);
}

const noFault = () => {};

/** Thrown at a cancellation checkpoint; the normal failure path then rolls back. */
export class CancelledError extends Error {
  constructor() {
    super('installation cancelled');
    this.code = 'desktop.install_cancelled';
  }
}

/**
 * Copy a payload in many small units so a long copy can report progress and
 * stop at a checkpoint. Directories are expanded down to package contents.
 */
function copyTreeSteps(from, to, step) {
  const units = [];
  const walk = (rel, depth) => {
    mkdirSync(join(to, rel), { recursive: true });
    for (const entry of readdirSync(join(from, rel), { withFileTypes: true })) {
      const path = rel ? join(rel, entry.name) : entry.name;
      if (entry.isDirectory() && !entry.isSymbolicLink() && depth < 4)
        walk(path, depth + 1);
      else units.push(path);
    }
  };
  walk('', 0);
  let reported = -1;
  units.forEach((path, index) => {
    copyTree(join(from, path), join(to, path));
    const fraction = Math.floor(((index + 1) / units.length) * 100);
    if (fraction !== reported) {
      reported = fraction;
      step(fraction / 100);
    }
  });
}

/** Remove only the empty directories and marker a failed first install created. */
function cleanupFreshInstall(root, dataHome, created) {
  const removeEmpty = (dir) => {
    try {
      rmdirSync(dir);
    } catch {
      // Not empty (user files) or absent: keep it.
    }
  };
  for (const entry of OWNED_ENTRIES)
    rmSync(join(root, entry), { recursive: true, force: true });
  rmSync(join(root, INSTALL_MARKER), { force: true });
  if (created.root) removeEmpty(root);
  if (created.profile) {
    rmSync(join(backupsDir(dataHome), DATA_MARKER), { force: true });
    removeEmpty(backupsDir(dataHome));
    removeEmpty(dirname(backupsDir(dataHome)));
    removeEmpty(dirname(profileDir(dataHome)));
  }
  if (created.dataHome) {
    rmSync(join(dataHome, DATA_OWNER), { force: true });
    removeEmpty(dataHome);
  }
}

function dataIdentity(path) {
  const info = statSync(path, { bigint: true });
  return `${info.dev}:${info.ino}:${info.birthtimeNs}`;
}

function claimDataHome(root, dataHome, existing, created) {
  const marker = join(dataHome, DATA_OWNER);
  if (!existsSync(dataHome)) {
    mkdirSync(dirname(dataHome), { recursive: true });
    mkdirSync(dataHome);
    created.dataHome = true;
    const owner = {
      root: norm(root),
      token: randomUUID(),
      identity: dataIdentity(dataHome),
    };
    writeFileSync(marker, JSON.stringify(owner), { flag: 'wx', mode: 0o600 });
    return owner.token;
  }
  if (ownsDataHome(root, dataHome, existing?.dataOwner))
    return existing.dataOwner;
  // Reuse by another installation revokes the original installation's claim.
  revokeForeignDataClaim(root, dataHome, existing?.dataOwner);
  return null;
}

function revokeForeignDataClaim(root, dataHome, token) {
  const previous = readDataOwner(dataHome);
  if (
    previous &&
    (previous.root !== norm(root) || previous.token !== token) &&
    previous.identity === dataIdentity(dataHome)
  )
    rmSync(join(dataHome, DATA_OWNER));
}

function ownsDataHome(root, dataHome, token) {
  const owner = token && readDataOwner(dataHome);
  return Boolean(
    owner &&
    owner.root === norm(root) &&
    owner.token === token &&
    owner.identity === dataIdentity(dataHome),
  );
}

function readDataOwner(dataHome) {
  try {
    const marker = join(dataHome, DATA_OWNER);
    assertLocalPath(marker);
    const info = statSync(marker);
    if (!info.isFile() || info.size > 2048) return null;
    const owner = readJson(marker);
    return typeof owner?.root === 'string' &&
      /^[0-9a-f-]{36}$/.test(owner.token)
      ? owner
      : null;
  } catch {
    return null;
  }
}

const versionSessions = new WeakMap();

function desktopWriters(root, dataHome) {
  const file = join(dataHome, 'auriga', 'guardian', 'process.json');
  const record = existsSync(file) ? new ProcessTree(dataHome).record : null;
  const identities = new Map();
  const current = (pid) => {
    if (!identities.has(pid)) identities.set(pid, processIdentity(pid));
    return identities.get(pid);
  };
  const recorded = record
    ? [record.launcher, record.root, ...record.children].filter(Boolean)
    : [];
  const active = record
    ? [record.root, ...record.children].filter(Boolean)
    : [];
  if (record?.launcher && existsSync(join(dirname(file), 'supervisor.lock.d')))
    active.push(record.launcher);
  return {
    active: () =>
      active.some((entry) => {
        if (!processAlive(entry.pid)) return false;
        if (process.platform !== 'win32') return true;
        const actual = current(entry.pid);
        return !actual || sameProcess(actual, entry);
      }),
    staleSession({ pid, createdAt }) {
      if (process.platform !== 'win32') return false;
      const actual = current(pid);
      if (!actual) return false;
      if (Number.isFinite(Date.parse(createdAt)))
        return !sameProcess(actual, { pid, createdAt });
      if (recorded.some((entry) => sameProcess(actual, entry))) return false;
      // Known bundled Windows launchers use versions/<id>/upstream/node/node.exe.
      // A legacy lock has no creation time of its own. A different historical
      // journal entry for the same PID cannot disprove a live bundled process.
      if (!actual.executable || !/^[a-z]:\\/i.test(actual.executable))
        return false;
      try {
        const versions = realpathSync.native(join(root, 'versions'));
        const executable = realpathSync.native(actual.executable);
        const rel = relative(norm(versions), norm(executable));
        return !/^[^\\/]+[\\/]upstream[\\/]node[\\/]node\.exe$/i.test(rel);
      } catch {
        return false;
      }
    },
  };
}

// Both the installation root and the separate data directory are locked.
// The old launcher also observes desktop-session.json during bridge upgrades.
export function openVersionSession(root, { dataHome, launching = false } = {}) {
  root = resolve(root);
  assertLocalPath(root);
  const releaseRoot = pathOperationLock(root);
  let releaseData, legacy;
  try {
    const current = readState(root);
    dataHome = resolve(dataHome ?? current?.dataHome);
    if (current && norm(dataHome) !== norm(current.dataHome))
      throw new Error(
        'this installation already uses the data directory ' +
          current.dataHome +
          '; moving data is not supported',
      );
    assertLocalPath(dataHome);
    releaseData = pathOperationLock(dataHome);
    if (!launching) {
      const writers = desktopWriters(root, dataHome);
      if (existsSync(join(root, INSTALL_MARKER)))
        legacy = desktopSession(root, writers.staleSession);
      if (writers.active())
        throw Object.assign(
          new Error('Auriga DSH 的数据仍在使用，请先退出。'),
          { code: 'desktop.in_use' },
        );
    }
  } catch (error) {
    try {
      legacy?.close();
    } finally {
      try {
        releaseData?.();
      } finally {
        releaseRoot();
      }
    }
    throw error;
  }
  const session = {
    close() {
      versionSessions.delete(session);
      try {
        legacy?.close();
      } finally {
        try {
          releaseData();
        } finally {
          releaseRoot();
        }
      }
    },
  };
  versionSessions.set(session, { root: norm(root), dataHome: norm(dataHome) });
  return session;
}

function useVersionSession(root, dataHome, session, run) {
  if (session) {
    const held = versionSessions.get(session);
    if (held?.root !== norm(root) || held.dataHome !== norm(dataHome))
      throw new Error('installation session ownership lost');
    return run();
  }
  const own = openVersionSession(root, { dataHome });
  try {
    return run();
  } finally {
    own.close();
  }
}

/**
 * Install or upgrade from a staged payload directory.
 * Returns { id, previous, upgraded }.
 *
 * `progress(stage, fraction)` reports the current step; `cancelled()` is
 * polled at checkpoints before the commit. A failed or cancelled first install
 * leaves no installation behind; an upgrade is rolled back to the old version.
 */
export function install(options) {
  const { root, dataHome, session, transfer } = options;
  assertLayout(resolve(root), resolve(dataHome));
  return useVersionSession(root, dataHome, session, () => {
    if (transfer && !holdsTransfer(root, transfer))
      throw new Error('installer update ownership lost');
    const release = transfer ? null : transferLock(root);
    try {
      return installStopped(options);
    } finally {
      release?.();
    }
  });
}

function installStopped({
  payload,
  root,
  dataHome,
  platformOrigin,
  platformBinding = {},
  smoke = defaultSmoke,
  fault = noFault,
  progress = () => {},
  cancelled = () => false,
  beginCommit = () => {},
}) {
  const checkpoint = () => {
    if (cancelled()) throw new CancelledError();
  };
  const explicitConfirmation = platformBinding.confirmedOrigin !== undefined;
  const selectedBinding = platformOrigin
    ? normalizePlatformBinding(platformOrigin, platformBinding)
    : undefined;
  if (explicitConfirmation && !selectedBinding)
    throw new Error('explicit confirmation requires a platform address');
  platformOrigin = selectedBinding?.origin;
  root = resolve(root);
  dataHome = resolve(dataHome);
  assertLayout(root, dataHome);
  const bindingFile = join(root, 'desktop-origin.json');
  const previousBinding =
    !explicitConfirmation && existsSync(bindingFile)
      ? readJson(bindingFile)
      : null;
  if (previousBinding) {
    updateOrigin(previousBinding.origin, previousBinding);
    if (selectedBinding && previousBinding.origin !== selectedBinding.origin)
      throw new Error(
        'platform binding differs; reinstall with explicit address confirmation',
      );
  }
  // An existing installation keeps its data directory: moving data is not
  // supported, so a different one is refused before anything changes.
  const existing = existsSync(join(root, 'install.json'))
    ? readState(root)
    : null;
  if (existing && norm(existing.dataHome) !== norm(dataHome))
    throw new Error(
      `this installation already uses the data directory ${existing.dataHome}; ` +
        'moving data is not supported, uninstall first or keep the same directory',
    );
  // Verify before anything is copied into place or executed.
  progress('verify', 0);
  const manifest = verifyPackage(payload, { trees: false });
  assertGuardianCompatible(payload, dataHome);
  checkpoint();
  const created = {
    root: !existsSync(root),
    dataHome: false,
    profile: false,
  };
  const firstInstall = !existing;
  mkdirSync(join(root, 'versions'), { recursive: true });
  writeFileSync(
    join(root, INSTALL_MARKER),
    JSON.stringify({ product: 'Auriga Desktop', schema: 1 }) + '\n',
  );
  let id, previous;
  try {
    recover(root);
    id = manifest.id;
    const state = readState(root);
    const dataOwner = claimDataHome(root, dataHome, state, created);
    previous = state?.current ?? null;
    const target = versionDir(root, id);
    let fresh = !existsSync(target);
    if (!fresh) {
      try {
        verifyPackage(target);
      } catch {
        rmSync(target, { recursive: true, force: true });
        fresh = true;
      }
    }
    if (fresh) {
      const staging = `${target}.partial`;
      rmSync(staging, { recursive: true, force: true });
      try {
        copyTreeSteps(payload, staging, (fraction) => {
          progress('copy', fraction);
          checkpoint();
        });
        progress('verify-copy', 0);
        verifyPackage(staging);
        checkpoint();
      } catch (error) {
        rmSync(staging, { recursive: true, force: true });
        throw error;
      }
      renameSync(staging, target);
    }
    transition({
      root,
      dataHome,
      from: state,
      toId: id,
      fresh,
      target,
      fault,
      dataOwner,
      apply() {
        progress('profile', 0);
        created.profile = !existsSync(profileDir(dataHome));
        seedProfile(dataHome, { platformOrigin, target });
        syncPlugin(target, dataHome);
        checkpoint();
        progress('smoke', 0);
        smoke(target, dataHome);
        checkpoint();
        beginCommit();
        progress('commit', 0);
      },
    });
  } catch (error) {
    if (firstInstall && !(error instanceof CrashError))
      cleanupFreshInstall(root, dataHome, created);
    throw error;
  }
  progress('done', 1);
  if (selectedBinding && (explicitConfirmation || !previousBinding))
    writeJsonAtomic(bindingFile, selectedBinding);
  return { id, previous, upgraded: Boolean(previous && previous !== id) };
}

/** Switch back to the previous version and restore its plugin files. */
export function rollback(options) {
  const root = resolve(options.root);
  assertOwned(root);
  const session = openVersionSession(root);
  try {
    const release = transferLock(root);
    try {
      return rollbackStopped(options);
    } finally {
      release();
    }
  } finally {
    session.close();
  }
}

function rollbackStopped({ root, smoke = defaultSmoke, fault = noFault }) {
  root = resolve(root);
  assertOwned(root);
  recover(root);
  const state = readState(root);
  if (!state?.previous) throw new Error('no previous version to roll back to');
  const dir = versionDir(root, state.previous);
  if (!existsSync(dir))
    throw new Error(`previous version missing: ${state.previous}`);
  verifyPackage(dir);
  assertGuardianCompatible(dir, state.dataHome);
  revokeForeignDataClaim(root, state.dataHome, state.dataOwner);
  transition({
    root,
    dataHome: state.dataHome,
    from: state,
    toId: state.previous,
    fresh: false,
    target: dir,
    fault,
    apply() {
      if (!restorePluginParts(state.dataHome, state.previous)) {
        seedProfile(state.dataHome, { target: dir });
        syncPlugin(dir, state.dataHome);
      }
      smoke(dir, state.dataHome);
    },
  });
  return { current: state.previous };
}

/**
 * Remove what this installer created in the install directory. Unknown files
 * are left alone; the directory itself goes only if nothing else is in it.
 * User data is kept unless purgeData is set, and then only our own marked
 * profile and backup folders are removed.
 */
export function uninstall({ root, purgeData = false }) {
  root = resolve(root);
  assertOwned(root);
  const session = openVersionSession(root);
  let result;
  try {
    const release = transferLock(root);
    try {
      result = withUpdateStateLock(root, () =>
        uninstallStopped(root, purgeData),
      );
    } finally {
      release();
    }
  } finally {
    session.close();
  }
  if (!result.pending.length) {
    try {
      rmdirSync(root);
    } catch {
      /* Keep unrelated files and directories. */
    }
  }
  return result;
}

function uninstallStopped(root, purgeData) {
  recover(root);
  const state = readState(root);
  if (!state) throw new Error(`no installation state in ${root}`);
  const entries = [
    ...OWNED_ENTRIES,
    ...readdirSync(root).filter((entry) =>
      /^(?:\.desktop-download-[A-Za-z0-9-]+|desktop-update\.json\.[a-f0-9-]{36}\.tmp)$/.test(
        entry,
      ),
    ),
    INSTALL_MARKER,
  ].filter((entry) => existsSync(join(root, entry)));
  for (const entry of entries) {
    const source = join(root, entry);
    assertLocalPath(source);
    if (isInside(source, state.dataHome))
      throw new Error('the data directory overlaps program files');
  }
  const cleanupId = randomUUID();
  const trash = join(root, `.uninstall-${cleanupId}`);
  const moved = [];
  mkdirSync(trash);
  try {
    for (const entry of entries) {
      renameSync(join(root, entry), join(trash, entry));
      moved.push(entry);
    }
  } catch (cause) {
    for (const entry of moved.reverse())
      renameSync(join(trash, entry), join(root, entry));
    rmdirSync(trash);
    throw Object.assign(
      new Error(`${BRAND_NAME} 的文件正在使用，请先退出后再卸载。`, { cause }),
      { code: 'desktop.in_use' },
    );
  }

  const dataKept = !purgeData || !removeDedicatedData(root, state);
  const result = { dataKept, dataHome: state.dataHome, pending: [] };
  try {
    rmSync(trash, { recursive: true, force: true, maxRetries: 3 });
    return result;
  } catch {
    // Only the unique program quarantine is eligible for delayed cleanup.
    writeFileSync(join(root, UNINSTALL_MARKER), cleanupId, { flag: 'wx' });
    return { ...result, pending: [trash], cleanupId };
  }
}

/**
 * A matching install-time claim is necessary. Foreign content or another install
 * revoking the claim keeps the entire data directory. Data is never queued.
 */
function removeDedicatedData(root, { dataHome, dataOwner }) {
  let detached;
  try {
    if (
      isInside(root, dataHome) ||
      isInside(dataHome, root) ||
      !ownsDataHome(root, dataHome, dataOwner)
    )
      return false;
    const known = [
      DATA_OWNER,
      'auriga',
      'profiles',
      'profile-backups',
      'sessions',
      'pnpm-store',
    ];
    if (readdirSync(dataHome).some((name) => !known.includes(name)))
      return false;
    for (const dir of ['profiles', 'profile-backups']) {
      const path = join(dataHome, dir);
      assertLocalPath(path);
      if (
        existsSync(path) &&
        readdirSync(path).some((name) => name !== PROFILE)
      )
        return false;
    }
    assertLocalPath(join(profileDir(dataHome), DATA_MARKER));
    if (!existsSync(join(profileDir(dataHome), DATA_MARKER))) return false;
    const journal = join(dataHome, 'auriga', 'guardian', 'process.json');
    if (existsSync(journal)) {
      assertLocalPath(journal);
      const { launcher } = readJson(journal);
      if (
        launcher &&
        (!launcher.executable || !isInside(root, launcher.executable))
      )
        return false;
    }
    const quarantine = join(
      dirname(dataHome),
      `.auriga-data-purge-${randomUUID()}`,
    );
    renameSync(dataHome, quarantine);
    detached = quarantine;
    if (!ownsDataHome(root, detached, dataOwner))
      throw new Error('data ownership changed during removal');
    rmSync(detached, { recursive: true, force: true, maxRetries: 3 });
    return true;
  } catch {
    // Never give the remaining data path to a deferred cleanup job.
    if (detached && !existsSync(dataHome)) {
      try {
        renameSync(detached, dataHome);
      } catch {
        /* Preserve the quarantined remainder. */
      }
    }
    return false;
  }
}

/** One running DSH per installation: deferred updates may only mutate a stopped profile. */
export function desktopSession(root, staleSession = () => false) {
  const file = join(root, 'desktop-session.json');
  const token = randomUUID();
  const claim = (pid) => {
    const identity = process.platform === 'win32' ? processIdentity(pid) : null;
    return { pid, token, createdAt: identity?.createdAt };
  };
  const mine = JSON.stringify(claim(process.pid));
  for (let attempt = 0; attempt < 2; attempt++) {
    try {
      const fd = openSync(file, 'wx', 0o600);
      try {
        writeFileSync(fd, mine);
      } finally {
        closeSync(fd);
      }
      return {
        child(pid) {
          writeJsonAtomic(file, claim(pid));
        },
        close() {
          if (existsSync(file) && readJson(file).token === token)
            rmSync(file, { force: true });
        },
      };
    } catch (error) {
      if (error.code !== 'EEXIST') throw error;
      const previous = readFileSync(file, 'utf8');
      const record = JSON.parse(previous);
      const { pid } = record;
      if (!Number.isSafeInteger(pid) || pid < 1)
        throw new Error('invalid desktop session lock');
      if (
        (!processAlive(pid) || staleSession(record)) &&
        readFileSync(file, 'utf8') === previous
      ) {
        rmSync(file);
        continue;
      }
      throw Object.assign(new Error(`${BRAND_NAME} 已在运行，请先退出。`), {
        code: 'desktop.in_use',
      });
    }
  }
  throw new Error('desktop session busy');
}

/** Secret-free platform status for the tray: state and fixed reason codes only. */
export function platformStatus(shellAuth, failure) {
  if (!shellAuth)
    return { state: 'unavailable', reason: failure ?? 'unavailable' };
  try {
    const { state, reason } = shellAuth.snapshot();
    return reason ? { state, reason } : { state };
  } catch {
    return { state: 'unavailable', reason: 'snapshot_failed' };
  }
}

/**
 * `control` (tray mode): `{ requested, report(event), release() }`. A stop
 * request ends DSH cleanly, applies a queued update and returns; status
 * events are reported without tokens, identities or paths.
 */
export async function launch(options) {
  const root = resolve(options.root);
  assertOwned(root);
  const versionSession = openVersionSession(root, { launching: true });
  try {
    return await launchLocked({ ...options, versionSession });
  } finally {
    versionSession.close();
  }
}

async function launchLocked({
  root,
  args = [],
  env = process.env,
  control,
  versionSession,
  openLoginOnStart = !args.includes('--no-open'),
}) {
  root = resolve(root);
  const report = (event) => {
    try {
      control?.report(event);
    } catch {
      // A closed tray pipe must not break the launcher.
    }
  };
  assertOwned(root);
  const initial = readState(root);
  if (!initial) throw new Error('installation state missing');
  verifyPackage(versionDir(root, initial.current), { trees: false });
  assertNTFS(initial.dataHome);
  const tree = new ProcessTree(initial.dataHome);
  const writers = desktopWriters(root, initial.dataHome);
  const supervisor = new Supervisor(
    initial.dataHome,
    tree,
    packagedTrust(versionDir(root, initial.current)),
  );
  const releaseSingleton = tree.singleton();
  let session, shellAuth, shellFailure, statusTimer;
  const hasShell = existsSync(
    join(versionDir(root, initial.current), 'auriga/shell-auth/desktop.mjs'),
  );
  if (hasShell) supervisor.shellSnapshot = () => shellAuth?.snapshot();

  try {
    await supervisor.prepare();
    session = desktopSession(root, writers.staleSession);
    recover(root);
    const current = readState(root);
    revokeForeignDataClaim(root, current.dataHome, current.dataOwner);
    cleanupDownloads(root);
    const { applyPending } = await import('./update.mjs');
    const apply = async () => {
      try {
        if (existsSync(join(root, 'desktop-pending')))
          report({ event: 'update', phase: 'installing' });
        if (
          await applyPending(root, {
            session: versionSession,
            onState: (state) => report({ event: 'update', ...state }),
          })
        )
          report({ event: 'update', phase: 'installed' });
      } catch (error) {
        report({
          event: 'update',
          phase: 'failed',
          ...error.updateFailure,
          reason:
            error?.code === 'desktop.smoke_failed'
              ? 'desktop.smoke_failed'
              : 'updateFailed',
        });
        // Keep upstream config/error output private; expose only a fixed reason.
        console.error(
          error?.code === 'desktop.smoke_failed'
            ? `${BRAND_NAME} 更新失败，已保留旧版。[desktop.smoke_failed]`
            : `${BRAND_NAME} 更新失败，已保留旧版。[${error.updateFailure?.code ?? 'updateFailed'}/${error.updateFailure?.stage ?? 'unknown'}]`,
        );
      }
    };
    await apply();
    const state = readState(root);
    const dir = versionDir(root, state.current);
    // The update transaction may have changed packaged trust or profile bytes.
    verifyPackage(dir, { trees: false });
    supervisor.trust = packagedTrust(dir);
    if (existsSync(join(dir, 'auriga/shell-auth/desktop.mjs'))) {
      supervisor.shellSnapshot = () => shellAuth?.snapshot();
      shellAuth = await startShellAuth({
        root,
        dataHome: state.dataHome,
        dir,
        openLoginOnStart,
        onSkipLogin: (localLogin) =>
          report({
            event: 'login',
            opened: false,
            state: localLogin ? 'local_login' : 'skipped',
          }),
        onFailure: (reason) => {
          shellFailure = reason;
        },
      });
    } else shellFailure = 'component_missing';
    if (control) {
      let last = '';
      const publish = () => {
        const platform = platformStatus(shellAuth, shellFailure);
        const text = JSON.stringify(platform);
        if (text === last) return;
        last = text;
        report({ event: 'platform', ...platform });
      };
      publish();
      statusTimer = setInterval(publish, 2000);
      statusTimer.unref();
      // "Open Auriga" while signed out shows the platform sign-in page first.
      control.on?.('login', () => {
        const platform = platformStatus(shellAuth, shellFailure);
        if (!shellAuth?.openLogin || platform.state === 'authenticated') {
          report({ event: 'login', opened: false, state: platform.state });
          return;
        }
        Promise.resolve()
          .then(() => shellAuth.openLogin())
          .then(
            () => report({ event: 'login', opened: true }),
            () => report({ event: 'login', opened: false, state: 'failed' }),
          );
      });
    }
    await supervisor.reconcile(true);
    if (
      supervisor.privateOnly ||
      supervisor.guardian.read()?.guardian.state === 'private_only'
    )
      notifyPrivateOnly();
    const status = await superviseDsh({
      stopRequested: () => Boolean(control?.requested),
      tree,
      supervisor,
      session,
      notify: notifySharedRestart,
      attach: async (child) => {
        if (shellAuth) {
          try {
            await shellAuth.attach(child);
          } catch {
            child.stdio[3]?.destroy();
            console.error('Auriga 平台通道不可用，本地 DSH 继续运行。');
          }
        }
      },
      start: () =>
        spawn(
          nodeBin(dir),
          [
            ...(control
              ? [
                  '--import',
                  new URL('./dsh-shutdown.mjs', import.meta.url).href,
                ]
              : []),
            dshBin(dir),
            '--profile',
            PROFILE,
            ...args,
          ],
          {
            // In tray mode stdin is the tray's control pipe; DSH must not own it.
            stdio: [
              control ? 'ignore' : 'inherit',
              'inherit',
              'inherit',
              ...(control
                ? [shellAuth ? 'pipe' : 'ignore', 'ipc']
                : shellAuth
                  ? ['pipe']
                  : []),
            ],
            env: launchEnv(dir, state.dataHome, {
              ...env,
              AURIGA_DESKTOP_ROOT: root,
              AURIGA_DESKTOP_RUNTIME: join(dir, 'auriga/runtime/update.mjs'),
            }),
          },
        ),
    });
    session.child(process.pid);
    report({ event: 'dsh', state: 'stopped' });
    if (status === 0) await apply();
    return status;
  } finally {
    clearInterval(statusTimer);
    try {
      try {
        await shellAuth?.close();
      } finally {
        await tree.stopTree();
      }
    } finally {
      session?.close();
      releaseSingleton();
    }
  }
}
