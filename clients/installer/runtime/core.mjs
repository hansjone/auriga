import { BRAND_NAME } from './brand.mjs';
import { cleanupDownloads } from './update-state.mjs';
import { createRequire } from 'node:module';
import { upgradeSkillPatch, yamlParser } from './skill-config.mjs';
import {
  ProcessTree,
  assertNTFS,
  notifyPrivateOnly,
  notifySharedRestart,
} from './process-tree.mjs';
import { Supervisor, packagedTrust } from './supervisor.mjs';
import { superviseDsh } from './desktop-supervision.mjs';
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
  renameSync,
  rmSync,
  rmdirSync,
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
const DATA_MARKER = '.auriga-desktop';
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
  'desktop-pending',
  'desktop-origin.json',
  'desktop-session.json',
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
  return rel === '' || (!rel.startsWith('..') && !isAbsolute(rel));
}

/**
 * Install directory and data directory must be separate trees, and an
 * existing install directory must be empty or already ours.
 */
export function assertLayout(root, dataHome) {
  if (parsePath(root).root === resolve(root))
    throw new Error('install directory must not be a drive or filesystem root');
  if (isInside(root, dataHome))
    throw new Error('data directory must not be inside the install directory');
  if (isInside(dataHome, root))
    throw new Error('install directory must not be inside the data directory');
  if (existsSync(root)) {
    const entries = readdirSync(root);
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
        `  config: ${JSON.stringify({ connect: { platformOrigin: origin, issuer: origin, clientId: CLIENT_ID, providerId: PROVIDER_ID } })}`,
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
    throw new Error(
      `smoke failed (exit ${result.status}): ${(result.stderr || result.stdout || '').slice(-600)}`,
    );
  if (!/auriga-dsh/.test(result.stdout))
    throw new Error('smoke failed: effective config does not list auriga-dsh');
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
  for (const name of readdirSync(source)) {
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
}) {
  const profile = profileDir(dataHome);
  const profileExisted = existsSync(profile);
  const label = from?.current ?? 'initial';
  if (profileExisted) snapshotProfile(dataHome, label);
  const txn = {
    phase: 'prepare',
    dataHome,
    from: from
      ? { current: from.current, previous: from.previous ?? null }
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

/**
 * Install or upgrade from a staged payload directory.
 * Returns { id, previous, upgraded }.
 */
export function install({
  payload,
  root,
  dataHome,
  platformOrigin,
  smoke = defaultSmoke,
  fault = noFault,
}) {
  root = resolve(root);
  dataHome = resolve(dataHome);
  assertLayout(root, dataHome);
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
  const manifest = verifyPackage(payload, { trees: false });
  assertGuardianCompatible(payload, dataHome);
  mkdirSync(join(root, 'versions'), { recursive: true });
  writeFileSync(
    join(root, INSTALL_MARKER),
    JSON.stringify({ product: 'Auriga Desktop', schema: 1 }) + '\n',
  );
  recover(root);
  const id = manifest.id;
  const state = readState(root);
  const previous = state?.current ?? null;
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
      copyTree(payload, staging);
      verifyPackage(staging);
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
    apply() {
      seedProfile(dataHome, { platformOrigin, target });
      syncPlugin(target, dataHome);
      smoke(target, dataHome);
    },
  });
  if (platformOrigin && !existsSync(join(root, 'desktop-origin.json')))
    writeJsonAtomic(join(root, 'desktop-origin.json'), {
      origin: new URL(platformOrigin).origin,
    });
  return { id, previous, upgraded: Boolean(previous && previous !== id) };
}

/** Switch back to the previous version and restore its plugin files. */
export function rollback({ root, smoke = defaultSmoke, fault = noFault }) {
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
  recover(root);
  cleanupDownloads(root, { stopped: true });
  const state = readState(root);
  if (!state) throw new Error(`no installation state in ${root}`);
  if (purgeData) {
    for (const dir of [
      profileDir(state.dataHome),
      backupsDir(state.dataHome),
    ]) {
      if (!existsSync(dir)) continue;
      if (!existsSync(join(dir, DATA_MARKER)))
        throw new Error(`refusing to remove unmarked directory: ${dir}`);
    }
    rmSync(profileDir(state.dataHome), { recursive: true, force: true });
    rmSync(backupsDir(state.dataHome), { recursive: true, force: true });
  }
  for (const entry of OWNED_ENTRIES)
    rmSync(join(root, entry), { recursive: true, force: true });
  rmSync(join(root, INSTALL_MARKER), { force: true });
  try {
    rmdirSync(root);
  } catch {
    // Not empty (user files) or still in use: leave it.
  }
  return { dataKept: !purgeData, dataHome: state.dataHome };
}

/** One running DSH per installation: deferred updates may only mutate a stopped profile. */
export function desktopSession(root) {
  const file = join(root, 'desktop-session.json');
  const token = randomUUID();
  for (let attempt = 0; attempt < 2; attempt++) {
    try {
      const fd = openSync(file, 'wx', 0o600);
      try {
        writeFileSync(fd, JSON.stringify({ pid: process.pid, token }));
      } finally {
        closeSync(fd);
      }
      return {
        child(pid) {
          writeJsonAtomic(file, { pid, token });
        },
        close() {
          if (readJson(file).token === token) rmSync(file, { force: true });
        },
      };
    } catch (error) {
      if (error.code !== 'EEXIST') throw error;
      const previous = readFileSync(file, 'utf8');
      const { pid } = JSON.parse(previous);
      if (!Number.isSafeInteger(pid) || pid < 1)
        throw new Error('invalid desktop session lock');
      try {
        process.kill(pid, 0);
      } catch (check) {
        if (check.code === 'ESRCH' && readFileSync(file, 'utf8') === previous) {
          rmSync(file);
          continue;
        }
      }
      throw new Error(`${BRAND_NAME} 已在运行，请先退出。`);
    }
  }
  throw new Error('desktop session busy');
}

export async function launch({ root, args = [], env = process.env }) {
  root = resolve(root);
  assertOwned(root);
  const initial = readState(root);
  if (!initial) throw new Error('installation state missing');
  verifyPackage(versionDir(root, initial.current), { trees: false });
  assertNTFS(initial.dataHome);
  const tree = new ProcessTree(initial.dataHome);
  const oldProcesses = [
    tree.record.launcher,
    tree.record.root,
    ...tree.record.children,
  ].filter(Boolean);
  const supervisor = new Supervisor(
    initial.dataHome,
    tree,
    packagedTrust(versionDir(root, initial.current)),
  );
  const releaseSingleton = tree.singleton();
  let session;

  try {
    await supervisor.prepare();
    const sessionFile = join(root, 'desktop-session.json');
    if (existsSync(sessionFile) && (await tree.isStopped())) {
      const previous = readJson(sessionFile);
      if (oldProcesses.some((p) => p.pid === previous.pid)) rmSync(sessionFile);
    }
    session = desktopSession(root);
    recover(root);
    cleanupDownloads(root, { stopped: true });
    const { applyPending } = await import('./update.mjs');
    const apply = async () => {
      try {
        await applyPending(root);
      } catch {
        console.error(`${BRAND_NAME} 更新失败，已保留旧版。`);
      }
    };
    await apply();
    const state = readState(root);
    const dir = versionDir(root, state.current);
    // The update transaction may have changed packaged trust or profile bytes.
    verifyPackage(dir, { trees: false });
    supervisor.trust = packagedTrust(dir);
    await supervisor.reconcile(true);
    if (
      supervisor.privateOnly ||
      supervisor.guardian.read()?.guardian.state === 'private_only'
    )
      notifyPrivateOnly();
    const status = await superviseDsh({
      tree,
      supervisor,
      session,
      notify: notifySharedRestart,
      start: () =>
        spawn(nodeBin(dir), [dshBin(dir), '--profile', PROFILE, ...args], {
          stdio: 'inherit',
          env: launchEnv(dir, state.dataHome, {
            ...env,
            AURIGA_DESKTOP_ROOT: root,
            AURIGA_DESKTOP_RUNTIME: join(dir, 'auriga/runtime/update.mjs'),
          }),
        }),
    });
    session.child(process.pid);
    await apply();
    return status;
  } finally {
    try {
      await tree.stopTree();
    } finally {
      session?.close();
      releaseSingleton();
    }
  }
}
