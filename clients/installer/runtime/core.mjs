/**
 * Installer core: pure Node (no dependencies) so it runs under the bundled
 * Node runtime on any platform and under plain `node --test`.
 *
 * Layout under <root>:
 *   versions/<id>/   immutable component set (upstream/ + auriga/), one per bundle id
 *   current.txt      id of the active version (plain text for the .cmd launcher)
 *   install.json     { dataHome, current, previous }
 * User data lives in <dataHome> and is never inside <root>.
 */
import {
  cpSync,
  existsSync,
  mkdirSync,
  readdirSync,
  readFileSync,
  renameSync,
  rmSync,
  writeFileSync,
} from 'node:fs';
import { delimiter, dirname, join, resolve } from 'node:path';
import { spawnSync } from 'node:child_process';

export const PROFILE = 'auriga';
const PLUGIN = 'auriga-dsh';
const CLIENT_ID = 'auriga-dsh';
const PROVIDER_ID = 'discover';
// The profile points at a tarball inside itself so the bundled pnpm can
// reinstall the plugin (it is not on the npm registry) without breaking it.
const PLUGIN_SPEC = 'file:./.auriga/auriga-dsh.tgz';
const BUNDLES = ['@deepseek-ai/dsh-base', '@deepseek-ai/dsh-web-app', PLUGIN];

const readJson = (file) => JSON.parse(readFileSync(file, 'utf8'));
const writeJson = (file, value) =>
  writeFileSync(file, JSON.stringify(value, null, 2) + '\n');

export function versionDir(root, id) {
  return join(root, 'versions', id);
}

export function readState(root) {
  const file = join(root, 'install.json');
  return existsSync(file) ? readJson(file) : null;
}

function writeState(root, state) {
  writeJson(join(root, 'install.json'), state);
  writeFileSync(join(root, 'current.txt'), state.current + '\n');
}

export function profileDir(dataHome) {
  return join(dataHome, 'profiles', PROFILE);
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

/** Seed profile manifest and (only when absent) the patch with the platform address. */
function seedProfile(dataHome, { platformOrigin }) {
  const dir = profileDir(dataHome);
  mkdirSync(join(dir, 'node_modules'), { recursive: true });
  const manifestFile = join(dir, 'package.json');
  const existing = existsSync(manifestFile) ? readJson(manifestFile) : {};
  const bundles = [...(existing.dsh?.profile?.bundles ?? [])];
  for (const b of BUNDLES) if (!bundles.includes(b)) bundles.push(b);
  writeJson(manifestFile, {
    ...existing,
    name: existing.name ?? 'auriga-desktop-profile',
    private: true,
    dependencies: { ...existing.dependencies, [PLUGIN]: PLUGIN_SPEC },
    dsh: { ...existing.dsh, profile: { ...existing.dsh?.profile, bundles } },
  });
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
}

function backupDir(dataHome) {
  return join(dataHome, 'profile-backups', PROFILE);
}

/** Copy the plugin closure into the profile; back up previous profile files first. */
function syncPlugin(dir, dataHome, backupLabel) {
  const profile = profileDir(dataHome);
  const modules = join(profile, 'node_modules');
  if (backupLabel && existsSync(join(profile, 'package.json'))) {
    const backup = join(backupDir(dataHome), backupLabel);
    rmSync(backup, { recursive: true, force: true });
    mkdirSync(backup, { recursive: true });
    cpSync(join(profile, 'package.json'), join(backup, 'package.json'));
    if (existsSync(modules))
      cpSync(modules, join(backup, 'node_modules'), { recursive: true });
    if (existsSync(join(profile, '.auriga')))
      cpSync(join(profile, '.auriga'), join(backup, '.auriga'), {
        recursive: true,
      });
  }
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
        cpSync(join(source, entry, sub), target, { recursive: true });
      }
    } else {
      const target = join(modules, entry);
      rmSync(target, { recursive: true, force: true });
      cpSync(join(source, entry), target, { recursive: true });
    }
  }
}

function restoreBackup(dataHome, label) {
  const backup = join(backupDir(dataHome), label);
  if (!existsSync(backup)) return false;
  const profile = profileDir(dataHome);
  rmSync(join(profile, 'node_modules'), { recursive: true, force: true });
  cpSync(join(backup, 'package.json'), join(profile, 'package.json'));
  if (existsSync(join(backup, '.auriga')))
    cpSync(join(backup, '.auriga'), join(profile, '.auriga'), {
      recursive: true,
    });
  if (existsSync(join(backup, 'node_modules')))
    cpSync(join(backup, 'node_modules'), join(profile, 'node_modules'), {
      recursive: true,
    });
  return true;
}

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
}) {
  const manifest = readJson(join(payload, 'manifest.json'));
  const id = manifest.id;
  root = resolve(root);
  dataHome = resolve(dataHome);
  const state = readState(root);
  const previous = state?.current ?? null;
  const target = versionDir(root, id);
  const fresh = !existsSync(target);
  mkdirSync(join(root, 'versions'), { recursive: true });
  const staging = `${target}.partial`;
  if (fresh) {
    rmSync(staging, { recursive: true, force: true });
    cpSync(payload, staging, { recursive: true });
    renameSync(staging, target);
  }
  try {
    seedProfile(dataHome, { platformOrigin });
    syncPlugin(target, dataHome, previous && previous !== id ? previous : null);
    smoke(target, dataHome);
  } catch (error) {
    if (previous && previous !== id) restoreBackup(dataHome, previous);
    if (fresh) rmSync(target, { recursive: true, force: true });
    throw error;
  }
  writeState(root, {
    dataHome,
    current: id,
    previous:
      previous && previous !== id ? previous : (state?.previous ?? null),
  });
  installLaunchers(root, target);
  pruneVersions(root);
  return { id, previous, upgraded: Boolean(previous && previous !== id) };
}

function installLaunchers(root, dir) {
  const source = join(dir, 'auriga', 'launcher');
  if (existsSync(source))
    cpSync(source, join(root, 'bin'), { recursive: true, force: true });
}

function pruneVersions(root) {
  const state = readState(root);
  const keep = new Set([state.current, state.previous].filter(Boolean));
  for (const entry of readdirSync(join(root, 'versions'))) {
    if (!keep.has(entry))
      rmSync(join(root, 'versions', entry), { recursive: true, force: true });
  }
}

/** Switch back to the previous version and restore its plugin files. */
export function rollback({ root, smoke = defaultSmoke }) {
  root = resolve(root);
  const state = readState(root);
  if (!state?.previous) throw new Error('no previous version to roll back to');
  const dir = versionDir(root, state.previous);
  if (!existsSync(dir))
    throw new Error(`previous version missing: ${state.previous}`);
  if (!restoreBackup(state.dataHome, state.previous))
    syncPlugin(dir, state.dataHome, null);
  smoke(dir, state.dataHome);
  installLaunchers(root, dir);
  writeState(root, {
    dataHome: state.dataHome,
    current: state.previous,
    previous: state.current,
  });
  return { current: state.previous };
}

/** Remove the install root; user data is kept unless purgeData is set. */
export function uninstall({ root, purgeData = false }) {
  root = resolve(root);
  const state = readState(root);
  if (!state) throw new Error(`not an installation: ${root}`);
  if (purgeData) {
    const profile = profileDir(state.dataHome);
    rmSync(profile, { recursive: true, force: true });
    rmSync(backupDir(state.dataHome), { recursive: true, force: true });
  }
  rmSync(root, { recursive: true, force: true });
  return { dataKept: !purgeData, dataHome: state.dataHome };
}

export function launch({ root, args = [], env = process.env }) {
  root = resolve(root);
  const state = readState(root);
  if (!state) throw new Error(`not an installation: ${root}`);
  const dir = versionDir(root, state.current);
  const child = spawnSync(
    nodeBin(dir),
    [dshBin(dir), '--profile', PROFILE, ...args],
    { stdio: 'inherit', env: launchEnv(dir, state.dataHome, env) },
  );
  return child.status ?? 1;
}
