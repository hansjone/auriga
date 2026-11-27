/**
 * Package manifest verification (local integrity check, no certificate needed).
 * Used by the build (to compute the package id) and by install (before any
 * file is copied into place).
 */
import { createHash } from 'node:crypto';
import { Buffer } from 'node:buffer';
import {
  closeSync,
  existsSync,
  openSync,
  readFileSync,
  readSync,
  readdirSync,
  statSync,
} from 'node:fs';
import { open as openAsync, stat as statAsync } from 'node:fs/promises';
import { isAbsolute, join, relative, sep } from 'node:path';
import { satisfiesMin } from './semver-lite.mjs';

export const PRODUCT = 'Auriga Desktop';
export const DESKTOP_CORE_DIR = 'upstream/app/node_modules/auriga-dsh';
const LEGACY_CORE_DIR = 'auriga/plugin/node_modules/auriga-dsh';

/** Trees pinned by registry integrity (upstream lock / pnpm) instead of per-file hashes. */
export const PINNED_TREES = [
  'upstream/app/node_modules/',
  'tools/pnpm/node_modules/',
];

export const isPinnedPath = (path) =>
  !path.startsWith(DESKTOP_CORE_DIR + '/') &&
  PINNED_TREES.some((prefix) => path.startsWith(prefix));

// Reuse a single 1 MiB scratch buffer for synchronous hashing so verifying
// thousands of small files does not allocate gigabytes of short-lived buffers.
const syncHashBuffer = Buffer.allocUnsafe(1 << 20);

export function sha256File(file) {
  const hash = createHash('sha256');
  const fd = openSync(file, 'r');
  try {
    for (;;) {
      const read = readSync(fd, syncHashBuffer, 0, syncHashBuffer.length, null);
      if (read === 0) break;
      hash.update(syncHashBuffer.subarray(0, read));
    }
  } finally {
    closeSync(fd);
  }
  return hash.digest('hex');
}

async function sha256FileAsync(file, buffer) {
  const hash = createHash('sha256');
  const handle = await openAsync(file, 'r');
  try {
    for (;;) {
      const { bytesRead } = await handle.read(buffer, 0, buffer.length, null);
      if (bytesRead === 0) break;
      hash.update(buffer.subarray(0, bytesRead));
    }
  } finally {
    await handle.close();
  }
  return hash.digest('hex');
}

async function mapConcurrent(items, concurrency, fn) {
  const results = new Array(items.length);
  let next = 0;
  let firstError = null;
  const workerCount = Math.min(Math.max(1, concurrency), items.length);
  const workers = [];
  for (let w = 0; w < workerCount; w++) {
    const buffer = Buffer.allocUnsafe(256 * 1024);
    workers.push(
      (async () => {
        for (;;) {
          if (firstError) return;
          const i = next++;
          if (i >= items.length) return;
          try {
            results[i] = await fn(items[i], i, buffer);
          } catch (error) {
            if (!firstError) firstError = error;
            return;
          }
        }
      })(),
    );
  }
  await Promise.allSettled(workers);
  if (firstError) throw firstError;
  return results;
}

async function treeDigestAsync(payload, root, concurrency = 12) {
  const base = join(payload, ...root.split('/'));
  const files = listFiles(base);
  const entries = await mapConcurrent(files, concurrency, async (f, _, buf) => {
    const full = join(base, f);
    const info = await statAsync(full);
    const digest = await sha256FileAsync(full, buf);
    return `${f}\t${info.size}\t${digest}\n`;
  });
  const hash = createHash('sha256');
  for (const line of entries) hash.update(line);
  return { root, files: files.length, sha256: hash.digest('hex') };
}

/**
 * Digest over every file below a pinned tree (path, size, content hash), so
 * the distributed upstream DSH and pnpm files themselves are checked, not
 * only the lock file that describes them.
 */
export function treeDigest(payload, root) {
  const base = join(payload, ...root.split('/'));
  const files = listFiles(base);
  const hash = createHash('sha256');
  for (const f of files) {
    const full = join(base, f);
    hash.update(`${f}	${statSync(full).size}	${sha256File(full)}
`);
  }
  return { root, files: files.length, sha256: hash.digest('hex') };
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
    trees: (manifest.trees ?? []).map((t) => [t.root, t.files, t.sha256]),
    plugin: [
      manifest.plugin.version,
      manifest.plugin.tgzSha256,
      ...(manifest.plugin.scope === 'installation' ? ['installation'] : []),
    ],
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

function readPackagedPlugin(payload, manifest) {
  const scoped = manifest.plugin.scope === 'installation';
  if (manifest.plugin.scope !== undefined && !scoped)
    throw new Error('unsupported desktop plugin installation scope');
  const directory = scoped ? DESKTOP_CORE_DIR : LEGACY_CORE_DIR;
  const plugin = readJson(join(payload, directory, 'package.json'));
  if (scoped) {
    const app = readJson(join(payload, 'upstream/app/package.json'));
    if (
      plugin.name !== 'auriga-dsh' ||
      app.dependencies?.['auriga-dsh'] !== plugin.version
    )
      throw new Error('desktop core is missing from the installation anchor');
  }
  return plugin;
}

function packageFileError(path, reason) {
  if (path.startsWith('runtime/python/'))
    return Object.assign(
      new Error(
        `desktop.python_runtime_modified：Python运行时校验失败（${reason}: ${path}）。请退出客户端，重新运行同版本安装包修复受管运行时。`,
      ),
      {
        code: 'desktop.python_runtime_modified',
        repair: 'reinstall',
        pathCategory: 'python-runtime',
      },
    );
  return new Error(`${reason}: ${path}`);
}

/**
 * Verify a package directory against its manifest. Throws on the first
 * problem; returns the parsed manifest.
 */
export function verifyPackage(payload, { trees = true } = {}) {
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
    !manifest.pnpm ||
    !Array.isArray(manifest.trees) ||
    PINNED_TREES.some(
      (p) => !manifest.trees.some((t) => t.root === p.slice(0, -1)),
    )
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
    if (!existsSync(full))
      throw packageFileError(f.path, 'listed file missing');
    if (statSync(full).size !== f.size)
      throw packageFileError(f.path, 'size mismatch');
    if (sha256File(full) !== f.sha256)
      throw packageFileError(f.path, 'checksum mismatch');
  }
  for (const path of listFiles(payload)) {
    if (path === 'manifest.json' || isPinnedPath(path)) continue;
    if (!listed.has(path))
      throw packageFileError(path, 'unlisted file in package');
  }

  if (trees) {
    for (const t of manifest.trees) {
      if (!PINNED_TREES.includes(t.root + '/'))
        throw new Error(`unexpected tree in manifest: ${t.root}`);
      const actual = treeDigest(payload, t.root);
      if (actual.files !== t.files || actual.sha256 !== t.sha256)
        throw new Error(
          `distributed files differ from the manifest: ${t.root} (${actual.files} files, expected ${t.files})`,
        );
    }
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

  const pluginPkg = readPackagedPlugin(payload, manifest);
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

/**
 * Concurrent async variant of verifyPackage(): identical security checks and
 * error codes, but hashes files in parallel (default 12 workers) to speed up
 * startup, install and update verification on Windows NTFS/NVMe.
 */
export async function verifyPackageAsync(
  payload,
  { trees = true, concurrency = 12 } = {},
) {
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
    !manifest.pnpm ||
    !Array.isArray(manifest.trees) ||
    PINNED_TREES.some(
      (p) => !manifest.trees.some((t) => t.root === p.slice(0, -1)),
    )
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
  }
  await mapConcurrent(manifest.files, concurrency, async (f, _, buf) => {
    const full = join(payload, ...f.path.split('/'));
    if (!existsSync(full))
      throw packageFileError(f.path, 'listed file missing');
    const info = await statAsync(full);
    if (info.size !== f.size) throw packageFileError(f.path, 'size mismatch');
    if ((await sha256FileAsync(full, buf)) !== f.sha256)
      throw packageFileError(f.path, 'checksum mismatch');
  });
  for (const path of listFiles(payload)) {
    if (path === 'manifest.json' || isPinnedPath(path)) continue;
    if (!listed.has(path))
      throw packageFileError(path, 'unlisted file in package');
  }

  if (trees) {
    for (const t of manifest.trees) {
      if (!PINNED_TREES.includes(t.root + '/'))
        throw new Error(`unexpected tree in manifest: ${t.root}`);
      const actual = await treeDigestAsync(payload, t.root, concurrency);
      if (actual.files !== t.files || actual.sha256 !== t.sha256)
        throw new Error(
          `distributed files differ from the manifest: ${t.root} (${actual.files} files, expected ${t.files})`,
        );
    }
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

  const pluginPkg = readPackagedPlugin(payload, manifest);
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
