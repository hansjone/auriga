import { createHash } from 'node:crypto';
import { execFileSync } from 'node:child_process';
import { lstatSync, readFileSync, readdirSync, rmSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';
import { productionInputs } from './inputs.mjs';
import { installedRuntime } from './runtime.mjs';

const hash = (bytes) => createHash('sha256').update(bytes).digest('hex');
const json = (path) => JSON.parse(readFileSync(path, 'utf8'));

function same(actual, expected, label) {
  if (!readFileSync(actual).equals(readFileSync(expected)))
    throw new Error(`Incremental foundation differs: ${label}; use full build`);
}

// The operator verifies the base's signed release before choosing its digest.
// Check the manifest against the actual immutable base, including extra files.
function verifyManifest(opt) {
  const manifest = json(join(opt, 'fixture/manifest.json'));
  if (!Array.isArray(manifest.files) || !manifest.files.length)
    throw new Error('Incremental base manifest is missing');
  const expected = new Map();
  for (const entry of manifest.files) {
    if (
      typeof entry.path !== 'string' ||
      !/^(?:dsh|auriga-dsh|fixture)\/(?:[^/\\]+\/)*[^/\\]+$/.test(entry.path) ||
      entry.path.split('/').some((part) => part === '.' || part === '..') ||
      entry.path === 'fixture/manifest.json' ||
      !/^[a-f0-9]{64}$/.test(entry.sha256 ?? '') ||
      expected.has(entry.path)
    )
      throw new Error('Incremental base manifest contains invalid entries');
    expected.set(entry.path, entry.sha256);
  }
  function walk(directory) {
    for (const name of readdirSync(directory)) {
      const path = join(directory, name);
      if (path === join(opt, 'fixture/manifest.json')) continue;
      const stat = lstatSync(path);
      if (stat.isSymbolicLink()) continue; // Same inventory rule as full build.
      if (stat.isDirectory()) {
        walk(path);
        continue;
      }
      const relative = path.slice(opt.length + 1).replaceAll('\\', '/');
      if (!stat.isFile() || expected.get(relative) !== hash(readFileSync(path)))
        throw new Error('Incremental base manifest differs from actual files');
      expected.delete(relative);
    }
  }
  for (const name of ['dsh', 'auriga-dsh', 'fixture']) {
    const directory = join(opt, name);
    const stat = lstatSync(directory);
    if (!stat.isDirectory() || stat.isSymbolicLink())
      throw new Error('Incremental base manifest roots must be directories');
    walk(directory);
  }
  if (expected.size)
    throw new Error('Incremental base manifest contains missing files');
}

/** Verify all stable inputs before removing the previous plugin source snapshot. */
export function prepareIncremental({ opt, expected, baseImage, toolchain }) {
  if (
    typeof baseImage !== 'string' ||
    !/^[A-Za-z0-9][A-Za-z0-9._:/-]*@sha256:[a-f0-9]{64}$/.test(baseImage) ||
    baseImage.includes('://')
  )
    throw new Error('Incremental base must use a complete image digest');
  opt = resolve(opt);
  expected = resolve(expected);
  verifyManifest(opt);
  for (const name of ['package.json', 'package-lock.json'])
    same(join(opt, 'dsh', name), join(expected, 'recipe', name), `dsh/${name}`);
  for (const name of [
    'versions.json',
    'build.mjs',
    'inputs.mjs',
    'runtime.mjs',
    'start.mjs',
  ])
    same(join(opt, 'fixture', name), join(expected, 'recipe', name), name);
  same(
    join(opt, 'fixture/plugin-package.json'),
    join(expected, 'plugin/package.json'),
    'plugin source package',
  );
  if (
    readFileSync(join(opt, 'fixture/plugin-source-lock.sha256'), 'utf8') !==
    hash(readFileSync(join(expected, 'plugin/package-lock.json')))
  )
    throw new Error('Incremental plugin source lock mismatch; use full build');
  const versions = json(join(expected, 'recipe/versions.json'));
  if (
    toolchain.platform !== 'linux' ||
    toolchain.arch !== 'x64' ||
    toolchain.nodeVersion !== versions.nodeVersion ||
    toolchain.npmVersion !== versions.npmVersion
  )
    throw new Error('Incremental base toolchain mismatch; use full build');
  const sourcePlugin = json(join(expected, 'plugin/package.json'));
  const production = productionInputs(
    sourcePlugin,
    json(join(expected, 'plugin/package-lock.json')),
  );
  for (const [name, value] of [
    ['package.json', production.pkg],
    ['package-lock.json', production.lock],
  ])
    if (
      readFileSync(join(opt, 'auriga-dsh', name), 'utf8') !==
      JSON.stringify(value, null, 2) + '\n'
    )
      throw new Error(
        `Incremental installed plugin ${name} differs; use full build`,
      );
  installedRuntime(join(opt, 'dsh'), versions, production.pkg);
  // Docker COPY merges directories; explicitly removing this fixed image path
  // first ensures files deleted in the new source cannot survive from the base.
  const source = join(opt, 'auriga-dsh/src');
  const stat = lstatSync(source);
  if (!stat.isDirectory() || stat.isSymbolicLink())
    throw new Error('Incremental plugin source must be a directory');
  rmSync(source, { recursive: true });
}

if (
  process.argv[1] &&
  import.meta.url === pathToFileURL(resolve(process.argv[1])).href
) {
  if (process.argv.length !== 2)
    throw new Error('Incremental guard accepts no arguments');
  prepareIncremental({
    opt: '/opt',
    expected: '/tmp/auriga-incremental',
    baseImage: process.env.BASE_RUNTIME_IMAGE,
    toolchain: {
      platform: process.platform,
      arch: process.arch,
      nodeVersion: process.versions.node,
      npmVersion: execFileSync('npm', ['--version'], {
        encoding: 'utf8',
      }).trim(),
    },
  });
}
