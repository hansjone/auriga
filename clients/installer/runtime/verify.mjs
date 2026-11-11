/**
 * Package manifest verification (local integrity check, no certificate needed).
 * Used by the build (to compute the package id) and by install (before any
 * file is copied into place).
 */
import { createHash } from 'node:crypto';
import { existsSync, readFileSync, readdirSync, statSync } from 'node:fs';
import { isAbsolute, join, relative, sep } from 'node:path';
import { satisfiesMin } from './semver-lite.mjs';

export const PRODUCT = 'Auriga Desktop';

/** Trees pinned by registry integrity (upstream lock / pnpm) instead of per-file hashes. */
export const PINNED_TREES = [
  'upstream/app/node_modules/',
  'tools/pnpm/node_modules/',
];

export const isPinnedPath = (path) =>
  PINNED_TREES.some((prefix) => path.startsWith(prefix));

export function sha256File(file) {
  return createHash('sha256').update(readFileSync(file)).digest('hex');
}

/** All files below dir as sorted posix-style relative paths. */
export function listFiles(dir) {
  const out = [];
  const walk = (current) => {
    for (const entry of readdirSync(current, { withFileTypes: true })) {
      const full = join(current, entry.name);
      if (entry.isDirectory()) walk(full);
      else out.push(relative(dir, full).split(sep).join('/'));
    }
  };
  walk(dir);
  return out.sort();
}

/**
 * Package id: bundle version, upstream version and a digest over every
 * component (our files, upstream lock, Node and pnpm), so a change to Node,
 * pnpm or the installer core yields a new id even when versions stay equal.
 */
export function computeId(manifest) {
  const material = JSON.stringify({
    files: [...manifest.files]
      .map((f) => [f.path, f.sha256])
      .sort((a, b) => (a[0] < b[0] ? -1 : 1)),
    upstream: [
      manifest.upstream.version,
      manifest.upstream.integrity,
      manifest.upstream.lockSha256,
      manifest.upstream.nodeVersion,
      manifest.upstream.nodeZipSha256,
    ],
    pnpm: [manifest.pnpm.version, manifest.pnpm.integrity],
    plugin: [manifest.plugin.version, manifest.plugin.tgzSha256],
  });
  const digest = createHash('sha256').update(material).digest('hex');
  return `${manifest.bundleVersion}-dsh${manifest.upstream.version}-${digest.slice(0, 12)}`;
}

function unsafe(path) {
  return (
    typeof path !== 'string' ||
    path === '' ||
    isAbsolute(path) ||
    path.includes('\\') ||
    path.split('/').some((p) => p === '..' || p === '' || p === '.')
  );
}

const readJson = (file) => JSON.parse(readFileSync(file, 'utf8'));

/**
 * Verify a package directory against its manifest. Throws on the first
 * problem; returns the parsed manifest.
 */
export function verifyPackage(payload) {
  const manifestFile = join(payload, 'manifest.json');
  if (!existsSync(manifestFile)) throw new Error('manifest.json missing');
  let manifest;
  try {
    manifest = readJson(manifestFile);
  } catch {
    throw new Error('manifest.json is not valid JSON');
  }
  if (
    manifest.schema !== 1 ||
    manifest.product !== PRODUCT ||
    typeof manifest.id !== 'string' ||
    !Array.isArray(manifest.files) ||
    !manifest.upstream ||
    !manifest.plugin ||
    !manifest.pnpm
  )
    throw new Error('manifest is malformed');
  if (computeId(manifest) !== manifest.id)
    throw new Error('manifest id does not match its component digest');

  const listed = new Set();
  for (const f of manifest.files) {
    if (unsafe(f.path)) throw new Error(`unsafe manifest path: ${f.path}`);
    if (listed.has(f.path))
      throw new Error(`duplicate manifest path: ${f.path}`);
    listed.add(f.path);
    const full = join(payload, ...f.path.split('/'));
    if (!existsSync(full)) throw new Error(`listed file missing: ${f.path}`);
    if (statSync(full).size !== f.size)
      throw new Error(`size mismatch: ${f.path}`);
    if (sha256File(full) !== f.sha256)
      throw new Error(`checksum mismatch: ${f.path}`);
  }
  for (const path of listFiles(payload)) {
    if (path === 'manifest.json' || isPinnedPath(path)) continue;
    if (!listed.has(path)) throw new Error(`unlisted file in package: ${path}`);
  }

  const dshPkg = join(
    payload,
    'upstream/app/node_modules/@deepseek-ai/dsh/package.json',
  );
  if (!existsSync(dshPkg)) throw new Error('upstream dsh package missing');
  const dsh = readJson(dshPkg);
  if (dsh.version !== manifest.upstream.version)
    throw new Error(
      `upstream dsh is ${dsh.version}, manifest says ${manifest.upstream.version}`,
    );
  const lock = readJson(join(payload, 'upstream/app/package-lock.json'));
  if (
    lock.packages?.['node_modules/@deepseek-ai/dsh']?.integrity !==
    manifest.upstream.integrity
  )
    throw new Error('upstream lock integrity differs from manifest');

  const pluginPkg = readJson(
    join(payload, 'auriga/plugin/node_modules/auriga-dsh/package.json'),
  );
  if (pluginPkg.version !== manifest.plugin.version)
    throw new Error('plugin version differs from manifest');
  if (!satisfiesMin(manifest.upstream.version, pluginPkg.engines?.dsh))
    throw new Error(
      `plugin requires dsh ${pluginPkg.engines?.dsh}, package has ${manifest.upstream.version}`,
    );
  if (
    sha256File(join(payload, 'auriga/plugin/auriga-dsh.tgz')) !==
    manifest.plugin.tgzSha256
  )
    throw new Error('plugin tarball checksum differs from manifest');
  const pnpmPkg = join(payload, 'tools/pnpm/node_modules/pnpm/package.json');
  if (
    !existsSync(pnpmPkg) ||
    readJson(pnpmPkg).version !== manifest.pnpm.version
  )
    throw new Error('pnpm version differs from manifest');
  return manifest;
}
