import { gzipSync } from 'node:zlib';
import { matchesGlob } from 'node:path';
import { validateArchive } from './archive.js';
import { parseStrictJson } from './json.js';
import { schemaValid } from './schema.js';
import { excluded, invalid, safePath } from './submission-files.js';
import { encodeUstar } from './ustar.js';

const packageJsonLimit = 1048576;
const tarLimit = 268435456;
const compressedLimit = 67108864;
const packageNamePattern =
  /^(?:@[a-z0-9-][a-z0-9._-]*\/)?[a-z0-9-][a-z0-9._-]*$/;
const nativeBuildFields = [
  'onlyBuiltDependencies',
  'allowBuilds',
  'onlyBuiltDependenciesFile',
  'dangerouslyAllowAllBuilds',
];

/** @param {unknown} name @returns {name is string} */
function packageNameValid(name) {
  return (
    typeof name === 'string' &&
    name.length <= 214 &&
    packageNamePattern.test(name) &&
    !['node_modules', 'favicon.ico'].includes(name)
  );
}

/** @param {unknown} value @returns {value is Record<string, any>} */
function object(value) {
  return value !== null && typeof value === 'object' && !Array.isArray(value);
}

/** @param {string} path @param {Uint8Array} bytes */
function parsePackage(path, bytes) {
  let pkg;
  try {
    pkg = parseStrictJson(bytes);
  } catch {
    invalid(
      path,
      'package manifest must be valid UTF-8 JSON without duplicate keys',
    );
  }
  if (!object(pkg)) invalid(path, 'package manifest must be an object');
  return pkg;
}

/** @param {string} path @param {Record<string, any>} pkg @param {boolean} root */
function bundledPackages(path, pkg, root) {
  if (Object.hasOwn(pkg, 'gypfile'))
    invalid(path, 'gypfile declares a native build');
  if (pkg.pnpm != null) {
    if (!object(pkg.pnpm)) invalid(path, 'pnpm must be an object');
    for (const field of nativeBuildFields)
      if (Object.hasOwn(pkg.pnpm, field))
        invalid(path, 'pnpm.' + field + ' declares native builds');
  }
  if (
    !root &&
    pkg.scripts != null &&
    (!object(pkg.scripts) || Object.keys(pkg.scripts).length)
  )
    invalid(path, 'nested package contains scripts');
  for (const field of [
    'dependencies',
    'optionalDependencies',
    'peerDependencies',
  ])
    if (pkg[field] != null && !object(pkg[field]))
      invalid(path, field + ' must be an object');
  for (const peer of Object.keys(pkg.peerDependencies ?? {}))
    if (!/^@deepseek-ai\/dsh(?:$|-)/.test(peer))
      invalid(path, 'unsupported peer dependency ' + peer);

  const bundled = Object.hasOwn(pkg, 'bundledDependencies')
    ? pkg.bundledDependencies
    : pkg.bundleDependencies;
  const dependencies = Object.keys({
    ...pkg.dependencies,
    ...pkg.optionalDependencies,
  });
  for (const dep of dependencies) {
    if (!packageNameValid(dep)) invalid(path, 'invalid dependency name ' + dep);
    if (bundled !== true && !(Array.isArray(bundled) && bundled.includes(dep)))
      invalid(path, 'dependency must be bundled: ' + dep);
  }
  return dependencies;
}

/** @param {unknown} value @param {string} path @param {string} field */
function namedPath(value, path, field) {
  if (typeof value !== 'string')
    invalid(path, field + ' must be one relative file path');
  const name = value.replace(/^\.\//, '');
  if (!safePath(name) || name.split('/').some(excluded))
    invalid(path, field + ' must be a safe release path');
  return name;
}

/** @param {ReturnType<import('./submission-files.js').submissionFiles>} source */
export async function packPluginDirectory(source) {
  let tarSize = 1024;
  let metadataCount = 0;
  let metadataSize = 0;
  /** @type {Record<string, Buffer>} */
  const files = Object.create(null);
  /** @type {Map<string,string>} */
  const names = new Map();
  /** @type {Map<string,Record<string,any>>} */
  const packages = new Map();
  let fileCount = 0;

  /** @param {string} path */
  function validatePath(path) {
    const parts = path.split('/');
    if (
      !safePath(path) ||
      Buffer.byteLength('package/' + path) > 240 ||
      parts.length >= 16
    )
      invalid(path, 'unsafe or overlong release path');
    if (parts.at(-1)?.toLowerCase() === 'binding.gyp')
      invalid(path, 'binding.gyp declares a native build');
    let prefix = '';
    for (const part of parts) {
      prefix += (prefix ? '/' : '') + part;
      const key = prefix
        .normalize('NFC')
        .toUpperCase()
        .toLowerCase()
        .normalize('NFC');
      const existing = names.get(key);
      if (existing && existing !== prefix)
        invalid(path, 'release path collides with ' + existing);
      names.set(key, prefix);
    }
  }

  /** @param {string} path @param {import('node:fs').BigIntStats} info */
  async function addFile(path, info) {
    if (Object.hasOwn(files, 'package/' + path)) return;
    if (++fileCount > 4096) invalid(path, 'archive entry limit exceeded');
    const packageJson =
      path === 'package.json' || path.endsWith('/package.json');
    const room = tarLimit - tarSize - 512;
    if (room < 0 || info.size > BigInt(room))
      invalid(path, 'archive size limit exceeded');
    if (
      packageJson &&
      (++metadataCount > 512 || metadataSize + Number(info.size) > 16777216)
    )
      invalid(path, 'package manifest count or size limit exceeded');
    let bytes = await source.read(
      path,
      info,
      packageJson ? Math.min(room, packageJsonLimit) : room,
    );
    if (packageJson) {
      metadataSize += bytes.length;
      const pkg = parsePackage(path, bytes);
      bundledPackages(path, pkg, path === 'package.json');
      packages.set(path, pkg);
      if (path === 'package.json' && Object.hasOwn(pkg, 'scripts')) {
        delete pkg.scripts;
        bytes = Buffer.from(JSON.stringify(pkg) + '\n');
      }
    }
    tarSize += 512 + Math.ceil(bytes.length / 512) * 512;
    if (tarSize > tarLimit) invalid(path, 'archive size limit exceeded');
    files['package/' + path] = bytes;
  }

  /** @param {string} path @param {boolean} [optional] @returns {Promise<void>} */
  async function visit(path, optional = false) {
    validatePath(path);
    const info = await source.stat(path, optional);
    if (!info) return;
    if (!info.isDirectory()) {
      await addFile(path, info);
      return;
    }
    for await (const name of source.children(path))
      if (!excluded(name)) await visit(path + '/' + name);
  }

  /** @param {unknown} entry @param {string} base @param {string} manifestPath */
  async function collectReleaseEntry(entry, base, manifestPath) {
    if (typeof entry !== 'string' || !entry.includes('*')) {
      await visit(base + namedPath(entry, manifestPath, 'files entry'));
      return;
    }
    // Support a filename pattern inside an explicit directory, without recursive globs.
    const path = entry.replace(/^\.\//, '');
    const split = path.lastIndexOf('/');
    const parent = path.slice(0, split);
    const pattern = path.slice(split + 1);
    if (
      split < 1 ||
      parent.includes('*') ||
      pattern.includes('**') ||
      !/^[a-z0-9.*_-]+$/i.test(pattern) ||
      !/[a-z0-9]/i.test(pattern)
    )
      invalid(
        manifestPath,
        'files patterns must select filenames in an explicit directory',
      );
    const directory = base + namedPath(parent, manifestPath, 'files directory');
    validatePath(directory);
    let matched = false;
    for await (const name of source.children(directory)) {
      if (!excluded(name) && matchesGlob(name, pattern)) {
        matched = true;
        await visit(directory + '/' + name);
      }
    }
    if (!matched)
      invalid(directory, 'release files pattern matched no entries');
  }

  /** @param {string} base @returns {Promise<void>} */
  async function collectPackage(base) {
    const manifestPath = base + 'package.json';
    await visit(manifestPath);
    const pkg = packages.get(manifestPath);
    if (!pkg) invalid(manifestPath, 'package.json must be a regular file');
    if (pkg.files !== undefined && !Array.isArray(pkg.files))
      invalid(manifestPath, 'files must list explicit paths');
    if (Array.isArray(pkg.files)) {
      for (const entry of pkg.files)
        await collectReleaseEntry(entry, base, manifestPath);
    } else if (base) {
      // A dependency already selected through bundledDependencies may be a published npm package without a files field.
      for await (const name of source.children(base.slice(0, -1)))
        if (!excluded(name)) await visit(base + name);
    } else {
      await visit(
        namedPath(pkg.dsh?.bundle?.patch, manifestPath, 'dsh.bundle.patch'),
      );
      if (pkg.main === undefined) await visit('index.js', true);
    }
    if (pkg.main !== undefined)
      await visit(base + namedPath(pkg.main, manifestPath, 'main'), true);
    if (!base || Array.isArray(pkg.files))
      for await (const name of source.children(base.slice(0, -1)))
        if (
          /^(?:readme|licen[cs]e|copying|notice)(?:\.[a-z0-9]+)?$/i.test(name)
        )
          await visit(base + name);

    for (const dep of bundledPackages(manifestPath, pkg, !base))
      await collectPackage(base + 'node_modules/' + dep + '/');
  }

  await collectPackage('');
  const pkg = packages.get('package.json');
  if (
    !pkg ||
    !packageNameValid(pkg.name) ||
    pkg.name === 'auriga-dsh' ||
    pkg.name.startsWith('@deepseek-ai/')
  )
    invalid('package.json', 'invalid or reserved npm package name');
  if (!schemaValid('package', 'Version', pkg.version))
    invalid('package.json', 'version must be strict SemVer');
  if (pkg.engines !== undefined && !object(pkg.engines))
    invalid('package.json', 'engines must be an object');
  if (
    pkg.engines &&
    Object.hasOwn(pkg.engines, 'dsh') &&
    typeof pkg.engines.dsh !== 'string'
  )
    invalid('package.json', 'engines.dsh must be a version range');
  const patch = namedPath(
    pkg.dsh?.bundle?.patch,
    'package.json',
    'dsh.bundle.patch',
  );
  if (!Object.hasOwn(files, 'package/' + patch))
    invalid(patch, 'dsh.bundle.patch is absent from the release files');

  for (const [path, nested] of packages) {
    const base = path.slice(0, -'package.json'.length);
    for (const dep of bundledPackages(path, nested, path === 'package.json')) {
      const dependencyPath = base + 'node_modules/' + dep + '/package.json';
      if (!Object.hasOwn(files, 'package/' + dependencyPath))
        invalid(
          dependencyPath,
          'expected a physical bundled dependency listed in the release',
        );
    }
  }

  const tar = encodeUstar(files, (path) =>
    invalid(path.slice(8), 'path does not fit POSIX USTAR'),
  );
  const archive = gzipSync(tar);
  if (archive.length > compressedLimit || tar.length > archive.length * 100)
    invalid(
      '.',
      'plugin compressed size or compression ratio exceeds the archive limit',
    );
  try {
    await validateArchive(archive, { name: pkg.name, version: pkg.version });
  } catch {
    invalid('.', 'plugin archive failed the installation preflight');
  }
  return {
    pkg,
    archive,
    files: Object.keys(files).map((p) => p.replace(/^package\//, '')),
  };
}
