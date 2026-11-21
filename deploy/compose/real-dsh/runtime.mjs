import { createHash } from 'node:crypto';
import { readFileSync, realpathSync, statSync } from 'node:fs';
import { createRequire } from 'node:module';
import { isAbsolute, join, relative, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';
import { installedTooling } from './tooling.mjs';

const json = (path) => JSON.parse(readFileSync(path, 'utf8'));
const hash = (bytes) => createHash('sha256').update(bytes).digest('hex');

// Package metadata is consumed by DSH's native plugin manager even when this
// recipe activates the plugin through the agent's separate managed overlay.
export function pluginPackageFiles(directory) {
  const pkg = json(join(directory, 'package.json'));
  const patch = pkg.dsh?.bundle?.patch;
  const patches = Array.isArray(patch) ? patch : [patch];
  if (!patches.length) throw new Error('Plugin bundle patch is missing');
  const root = realpathSync(directory);
  const files = new Set();
  const outside = (path) =>
    isAbsolute(path) ||
    path === '..' ||
    path.startsWith('../') ||
    path.startsWith('..\\');
  for (const entry of [
    pkg.main,
    ...Object.values(pkg.exports ?? {}),
    ...patches,
  ]) {
    if (typeof entry !== 'string' || !entry.startsWith('./'))
      throw new Error('Plugin entry must be a package-relative file path');
    const file = resolve(root, entry);
    const rel = relative(root, file);
    if (outside(rel))
      throw new Error(`Declared entry is outside the plugin package: ${entry}`);
    const actual = relative(root, realpathSync(file));
    if (outside(actual))
      throw new Error(`Declared entry is outside the plugin package: ${entry}`);
    if (!statSync(file).isFile())
      throw new Error(`Declared plugin entry must be a file: ${entry}`);
    files.add(rel.replaceAll('\\', '/'));
  }
  return [...files].sort();
}

export async function verifyManagedBroker(directory) {
  const { verifyBrokerPackage } = await import(
    pathToFileURL(join(directory, 'src/local/broker-package.js')).href
  );
  return verifyBrokerPackage(join(directory, 'shell-auth'), 'managed-linux');
}

// Load the real packaged managed entry on a read-only filesystem. The verified
// artifact cannot contain the sentinel, and no credential or network is needed.
export async function managedBrokerChecks(directory) {
  await verifyManagedBroker(directory);
  const { createManagedFileConnection } = await import(
    pathToFileURL(join(directory, 'src/managed/file-connection.js')).href
  );
  const file = join(directory, 'shell-auth/.artifact-check-no-credential');
  try {
    await createManagedFileConnection({ file });
  } catch (error) {
    if (error.code === 'ENOENT' && error.path === file) return;
    throw error;
  }
  throw new Error(
    'Managed broker artifact check did not reject missing credential',
  );
}

// This recipe pins the already selected public host minimum, not an upstream
// "latest" range. A changed plugin minimum requires a newly reviewed host lock.
export function runtimeInputs(pkg, lock, versions, plugin) {
  const version = versions.dshVersion;
  if (plugin.engines?.dsh !== `>=${version}`)
    throw new Error('Pinned DSH does not match the plugin public host minimum');
  if (
    pkg.dependencies?.['@deepseek-ai/dsh'] !== version ||
    lock.packages?.['']?.dependencies?.['@deepseek-ai/dsh'] !== version
  )
    throw new Error('DSH package/lock/version pin mismatch');
  const packages = [];
  for (const [path, entry] of Object.entries(lock.packages)) {
    if (!path) continue;
    if (
      !/^sha512-[A-Za-z0-9+/]+=*$/.test(entry.integrity ?? '') ||
      !entry.resolved?.startsWith('https://registry.npmjs.org/')
    )
      throw new Error(`Unlocked DSH dependency: ${path}`);
    const match = /(?:^|\/)node_modules\/(@deepseek-ai\/dsh(?:-[^/]+)?)$/.exec(
      path,
    );
    if (!match) continue;
    if (entry.version !== version)
      throw new Error(`Mixed DSH package group: ${match[1]}`);
    packages.push({ path, name: match[1], version: entry.version });
  }
  if (!packages.some((entry) => entry.name === '@deepseek-ai/dsh'))
    throw new Error('Locked DSH host package is missing');
  return packages.sort((a, b) => a.path.localeCompare(b.path));
}

export function installedRuntime(directory, versions, plugin) {
  const bytes = readFileSync(join(directory, 'package-lock.json'));
  if (hash(bytes) !== versions.lockSha256)
    throw new Error('DSH lock SHA-256 mismatch');
  const packages = runtimeInputs(
    json(join(directory, 'package.json')),
    JSON.parse(bytes),
    versions,
    plugin,
  );
  for (const entry of packages) {
    const actual = json(join(directory, entry.path, 'package.json'));
    if (actual.name !== entry.name || actual.version !== entry.version)
      throw new Error(`Installed DSH package differs from lock: ${entry.name}`);
  }
  const host = join(directory, 'node_modules/@deepseek-ai/dsh');
  const pkg = json(join(host, 'package.json'));
  const declaredBin = typeof pkg.bin === 'string' ? pkg.bin : pkg.bin?.dsh;
  if (typeof declaredBin !== 'string' || !declaredBin)
    throw new Error('DSH does not declare its public CLI');
  const bin = resolve(host, declaredBin),
    rel = relative(host, bin);
  if (
    isAbsolute(rel) ||
    rel === '..' ||
    rel.startsWith('../') ||
    rel.startsWith('..\\') ||
    !statSync(bin).isFile()
  )
    throw new Error('DSH public CLI escapes its package or is missing');
  return { dshVersion: pkg.version, packages, publicCLI: declaredBin, bin };
}

export async function runtimeChecks(directory, versions, plugin) {
  const runtime = installedRuntime(directory, versions, plugin);
  const require = createRequire(join(directory, 'package.json'));
  // Resolve only published package roots, never private DSH implementation paths.
  const connection = await import(
    pathToFileURL(require.resolve('@deepseek-ai/dsh-client-connection')).href
  );
  for (const method of ['admit', 'authorizeIndex', 'isTrusted'])
    if (
      typeof connection.HostConnectionService?.prototype[method] !== 'function'
    )
      throw new Error(`DSH public connection admission API missing: ${method}`);
  const nativeModules = [];
  for (const name of [
    'node-pty',
    'sharp',
    '@deepseek-ai/node-addon-system/flock',
    '@deepseek-ai/dsh-session-query-sqlite',
  ]) {
    await import(pathToFileURL(require.resolve(name)).href);
    nativeModules.push(name);
  }
  const metadata = { ...runtime };
  delete metadata.bin;
  return {
    ...metadata,
    nativeModules,
    admissionMethods: ['admit', 'authorizeIndex', 'isTrusted'],
  };
}

if (
  process.argv[1] &&
  import.meta.url === pathToFileURL(resolve(process.argv[1])).href
) {
  const versions = json('/opt/fixture/versions.json');
  const plugin = json('/opt/auriga-dsh/package.json');
  const result = await runtimeChecks('/opt/dsh', versions, plugin);
  await managedBrokerChecks('/opt/auriga-dsh');
  const manifest = json('/opt/fixture/manifest.json');
  const tooling = installedTooling(
    '/opt/auriga-tools/pnpm',
    json('/opt/fixture/runtime-pins.json'),
  );
  if (
    manifest.dshVersion !== result.dshVersion ||
    JSON.stringify(manifest.runtimePackages) !==
      JSON.stringify(result.packages) ||
    JSON.stringify(manifest.runtimeTooling) !== JSON.stringify(tooling)
  )
    throw new Error(
      'Runtime manifest differs from installed DSH package group',
    );
  console.log(JSON.stringify(result));
}
