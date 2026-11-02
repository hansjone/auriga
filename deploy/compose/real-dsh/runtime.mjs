import { createHash } from 'node:crypto';
import { readFileSync, statSync } from 'node:fs';
import { createRequire } from 'node:module';
import { isAbsolute, join, relative, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';

const json = (path) => JSON.parse(readFileSync(path, 'utf8'));
const hash = (bytes) => createHash('sha256').update(bytes).digest('hex');

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
  const { bin, ...metadata } = runtime;
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
  const manifest = json('/opt/fixture/manifest.json');
  if (
    manifest.dshVersion !== result.dshVersion ||
    JSON.stringify(manifest.runtimePackages) !== JSON.stringify(result.packages)
  )
    throw new Error(
      'Runtime manifest differs from installed DSH package group',
    );
  console.log(JSON.stringify(result));
}
