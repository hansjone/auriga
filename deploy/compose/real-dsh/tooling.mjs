import { createHash } from 'node:crypto';
import { execFileSync } from 'node:child_process';
import { lstatSync, readFileSync, realpathSync } from 'node:fs';
import { dirname, isAbsolute, join, relative, resolve, sep } from 'node:path';
import { pathToFileURL } from 'node:url';

export const runtimePnpmDirectory = '/opt/auriga-tools/pnpm';
export const runtimePnpmEntry = 'node_modules/pnpm/bin/pnpm.cjs';
const json = (path) => {
  const file = regular(path);
  if (lstatSync(file).size > 32 * 1024 ** 2)
    throw new Error('Runtime tooling metadata is too large');
  return JSON.parse(readFileSync(file, 'utf8'));
};
const hash = (bytes, algorithm = 'sha256') =>
  createHash(algorithm)
    .update(bytes)
    .digest(algorithm === 'sha512' ? 'base64' : 'hex');

export function toolingInputs(pkg, lock, upstream) {
  const version = upstream.pnpm,
    integrity = upstream.pnpmIntegrity;
  const entry = lock.packages?.['node_modules/pnpm'];
  if (
    !/^\d+\.\d+\.\d+$/.test(version ?? '') ||
    !/^sha512-[A-Za-z0-9+/]{86}==$/.test(integrity ?? '') ||
    pkg.dependencies?.pnpm !== version ||
    lock.packages?.['']?.dependencies?.pnpm !== version ||
    Object.keys(pkg.dependencies).length !== 1 ||
    Object.keys(lock.packages).sort().join(',') !== ',node_modules/pnpm' ||
    entry.version !== version ||
    entry.integrity !== integrity ||
    entry.resolved !== `https://registry.npmjs.org/pnpm/-/pnpm-${version}.tgz`
  )
    throw new Error('Runtime pnpm package/lock/upstream pin mismatch');
  return { version, integrity, resolved: entry.resolved };
}

function regular(path, directory = false) {
  const absolute = resolve(path);
  let current = absolute;
  while (true) {
    if (lstatSync(current).isSymbolicLink())
      throw new Error('Runtime tooling links are refused');
    const parent = dirname(current);
    if (parent === current) break;
    current = parent;
  }
  if (
    directory
      ? !lstatSync(absolute).isDirectory()
      : !lstatSync(absolute).isFile()
  )
    throw new Error('Runtime tooling file is missing');
  return absolute;
}

function tarballBytes(path) {
  const file = regular(path),
    stat = lstatSync(file);
  if (!stat.size || stat.size > 64 * 1024 ** 2)
    throw new Error('Runtime pnpm tarball size rejected');
  return readFileSync(file);
}

export function verifyToolingTarball(path, pin) {
  const bytes = tarballBytes(path);
  if ('sha512-' + hash(bytes, 'sha512') !== pin.integrity)
    throw new Error('Runtime pnpm tarball integrity mismatch');
  return { sha256: hash(bytes), bytes: bytes.length };
}

// Only the approved original distribution is accepted. A kit/catalog merely
// locates the bytes; the committed npm SHA512 remains the execution authority.
export function findToolingTarball(root, explicit) {
  if (explicit) {
    if (!isAbsolute(explicit))
      throw new Error('Runtime tooling input must be absolute');
    return regular(explicit);
  }
  const binding = json(join(root, '.auriga-devkit.json'));
  if (!isAbsolute(binding.root ?? ''))
    throw new Error('Runtime tooling devkit binding is missing');
  const kit = binding.root;
  let file, expected;
  try {
    const catalog = json(join(kit, 'devkit-catalog.json'));
    const entry = catalog.versions?.[binding.baseline?.version];
    expected = entry?.files?.['runtime-tooling.tgz'];
    if (
      !/^[a-f0-9]{64}$/.test(expected ?? '') ||
      entry.kit?.release?.build?.runtimeTooling?.sha256 !== expected
    )
      throw new Error('Runtime tooling is absent from the matching devkit');
    file = join(kit, 'blobs', expected);
  } catch (error) {
    if (error.code !== 'ENOENT') throw error;
    const manifest = json(join(kit, 'devkit.json'));
    const artifact = manifest.release?.build?.runtimeTooling;
    if (
      artifact?.url !== 'runtime-tooling.tgz' ||
      !/^[a-f0-9]{64}$/.test(artifact.sha256 ?? '')
    )
      throw new Error('Runtime tooling is absent from the matching devkit');
    expected = artifact.sha256;
    file = join(kit, 'cache/runtime-tooling.tgz');
  }
  if (hash(tarballBytes(file)) !== expected)
    throw new Error('Runtime tooling devkit bytes differ');
  return file;
}

export function installedTooling(
  directory,
  upstream,
  { run = execFileSync } = {},
) {
  const root = realpathSync(regular(directory, true));
  const pin = toolingInputs(
    json(join(root, 'package.json')),
    json(join(root, 'package-lock.json')),
    upstream,
  );
  const pkg = json(join(root, 'node_modules/pnpm/package.json'));
  if (pkg.name !== 'pnpm' || pkg.version !== pin.version)
    throw new Error('Installed runtime pnpm differs from pin');
  const entry = regular(join(root, runtimePnpmEntry));
  const actual = relative(root, realpathSync(entry));
  if (isAbsolute(actual) || actual === '..' || actual.startsWith('..' + sep))
    throw new Error('Runtime pnpm entry escapes installation');
  const version = run(process.execPath, [entry, '--version'], {
    encoding: 'utf8',
    timeout: 30000,
    maxBuffer: 1024 * 1024,
    env: {
      PATH: dirname(process.execPath),
      HOME: '/tmp',
      COREPACK_ENABLE_NETWORK: '0',
      npm_config_update_notifier: 'false',
      PNPM_DISABLE_SELF_UPDATE_CHECK: '1',
    },
    cwd: root,
    windowsHide: true,
  }).trim();
  if (version !== pin.version)
    throw new Error('Runtime pnpm executable version differs from pin');
  return { ...pin, entry: runtimePnpmEntry };
}

if (
  process.argv[1] &&
  import.meta.url === pathToFileURL(resolve(process.argv[1])).href
) {
  const [action, ...args] = process.argv.slice(2);
  if (action === 'source-input' && args.length === 2) {
    const root = resolve(args[0]);
    const pin = toolingInputs(
      json(join(root, 'deploy/compose/real-dsh/tooling/package.json')),
      json(join(root, 'deploy/compose/real-dsh/tooling/package-lock.json')),
      json(join(root, 'clients/installer/upstream.json')),
    );
    console.log(
      JSON.stringify({ ...pin, ...verifyToolingTarball(args[1], pin) }),
    );
  } else if (action === 'input' && args.length === 1) {
    const upstream = json('/opt/fixture/runtime-pins.json');
    const pin = toolingInputs(
      json('/opt/auriga-tools/pnpm/package.json'),
      json('/opt/auriga-tools/pnpm/package-lock.json'),
      upstream,
    );
    verifyToolingTarball(args[0], pin);
  } else if (action === 'installed' && !args.length) {
    const upstream = json('/opt/fixture/runtime-pins.json');
    console.log(
      JSON.stringify(installedTooling(runtimePnpmDirectory, upstream)),
    );
  } else throw new Error('Unknown runtime tooling action');
}
