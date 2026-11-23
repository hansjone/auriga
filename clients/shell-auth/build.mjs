import {
  mkdirSync,
  readdirSync,
  copyFileSync,
  readFileSync,
  writeFileSync,
  existsSync,
} from 'node:fs';
import { basename, join, dirname, resolve, delimiter } from 'node:path';
import { fileURLToPath } from 'node:url';
import { createHash } from 'node:crypto';
import { createRequire } from 'node:module';
import { safePath } from './vault.mjs';
import { buildHelper } from './windows/build.mjs';
const source = dirname(fileURLToPath(import.meta.url));
export function resolveEsbuild(repoRoot = resolve(source, '../..')) {
  const roots = [
    join(repoRoot, 'package.json'),
    join(repoRoot, 'web', 'package.json'),
    join(repoRoot, 'web', 'admin', 'package.json'),
  ];
  const errors = [];
  for (const base of roots) {
    const loader = createRequire(base);
    try {
      return createRequire(loader.resolve('vite/package.json'))('esbuild');
    } catch (error) {
      errors.push(`${base} -> vite -> esbuild: ${error.message}`);
    }
    try {
      return loader('esbuild');
    } catch (error) {
      errors.push(`${base} -> esbuild: ${error.message}`);
    }
  }
  for (const pnpmDir of [
    join(repoRoot, 'web', 'node_modules', '.pnpm'),
    join(repoRoot, 'node_modules', '.pnpm'),
  ]) {
    if (!existsSync(pnpmDir)) continue;
    for (const entry of readdirSync(pnpmDir, { withFileTypes: true })) {
      if (!entry.isDirectory()) continue;
      if (!entry.name.startsWith('esbuild@') && !entry.name.startsWith('vite@'))
        continue;
      const candidate = join(pnpmDir, entry.name, 'node_modules', 'esbuild');
      if (!existsSync(join(candidate, 'package.json'))) continue;
      try {
        return createRequire(join(candidate, 'package.json'))(candidate);
      } catch (error) {
        errors.push(`${candidate}: ${error.message}`);
      }
    }
  }
  throw new Error(`Cannot resolve esbuild:\n${errors.join('\n')}`);
}
export function resolvePluginNodePaths({
  repoRoot = resolve(source, '../..'),
  env = process.env,
} = {}) {
  const configured = (env.AURIGA_CLIENT_PLUGIN_MODULES ?? '')
    .split(delimiter)
    .map((item) => item.trim())
    .filter(Boolean)
    .map((item) => resolve(item));
  const auto = [
    join(repoRoot, 'clients', 'dsh-plugin', 'node_modules'),
    join(repoRoot, 'web', 'node_modules'),
    join(repoRoot, 'node_modules'),
  ].filter((dir) => existsSync(dir));
  const seen = new Set();
  const paths = [];
  for (const dir of [...configured, ...auto]) {
    const key = process.platform === 'win32' ? dir.toLowerCase() : dir;
    if (seen.has(key)) continue;
    seen.add(key);
    paths.push(dir);
  }
  return paths;
}
function resolveWithNodePaths(baseFile, specifier, nodePaths = []) {
  const loader = createRequire(baseFile);
  const searchRoots = [
    dirname(baseFile),
    ...nodePaths.map((dir) =>
      basename(dir) === 'node_modules' ? dirname(dir) : dir,
    ),
  ];
  try {
    return loader.resolve(specifier, { paths: searchRoots });
  } catch (primaryError) {
    for (const dir of nodePaths) {
      const candidate = join(dir, specifier);
      try {
        return loader.resolve(candidate);
      } catch {
        /* Try next configured nodePaths directory. */
      }
    }
    throw primaryError;
  }
}
export function resolvePluginDependency(
  specifier,
  { repoRoot = resolve(source, '../..'), nodePaths = [] } = {},
) {
  const pluginPkg = join(repoRoot, 'clients', 'dsh-plugin', 'package.json');
  return resolveWithNodePaths(pluginPkg, specifier, nodePaths);
}
export function buildBroker(output, { managedLinux = false } = {}) {
  output = resolve(output);
  safePath(output);
  if (output === source || (existsSync(output) && readdirSync(output).length))
    throw new Error('empty broker output directory required');
  mkdirSync(output, { recursive: true });
  const files = managedLinux
    ? [
        'business.mjs',
        'host.mjs',
        'network.mjs',
        'platform.mjs',
        'schema.mjs',
        'streams.mjs',
        'team.mjs',
        'vault.mjs',
      ]
    : readdirSync(source).filter(
        (f) =>
          f.endsWith('.mjs') &&
          ![
            'build.mjs',
            'eslint.config.mjs',
            'network-core.mjs',
            'device-provider.mjs',
          ].includes(f),
      );
  if (!managedLinux) {
    mkdirSync(join(output, 'public'), { recursive: true });
    for (const file of readdirSync(join(source, 'public')))
      files.push('public/' + file);
  }
  for (const file of files)
    copyFileSync(join(source, file), join(output, file));
  if (!managedLinux) {
    const helper = 'windows/shell-helper.exe';
    buildHelper(join(output, helper));
    files.push(helper);
  }
  const repoRoot = resolve(source, '../..');
  const esbuild = resolveEsbuild(repoRoot);
  const nodePaths = resolvePluginNodePaths({ repoRoot });
  esbuild.buildSync({
    entryPoints: [join(source, 'network-core.mjs')],
    bundle: true,
    platform: 'node',
    format: 'esm',
    target: 'node22',
    outfile: join(output, 'network-core.bundle.mjs'),
    banner: {
      js: "import {createRequire as networkRequire} from 'node:module'; const require=networkRequire(import.meta.url);",
    },
    external: ['bufferutil', 'utf-8-validate'],
    ...(nodePaths.length ? { nodePaths } : {}),
  });
  files.push('network-core.bundle.mjs');
  mkdirSync(join(output, 'schemas', 'identity'), { recursive: true });
  copyFileSync(
    join(source, '../../contracts/identity/steps.schema.json'),
    join(output, 'schemas/identity/steps.schema.json'),
  );
  files.push('schemas/identity/steps.schema.json');
  for (const part of ['auth', 'plugin']) {
    mkdirSync(join(output, 'schemas', part), { recursive: true });
    for (const name of part === 'auth'
      ? [
          'uac-login-info',
          'uac-session-binding',
          'uac-control',
          'standard-device',
        ]
      : [
          'shell-auth',
          'shell-auth-control',
          'shell-business',
          'local-login',
          'managed-device',
          'managed-model',
        ]) {
      const file = `schemas/${part}/${name}.schema.json`;
      copyFileSync(
        join(source, '../../contracts', part, name + '.schema.json'),
        join(output, file),
      );
      files.push(file);
    }
  }
  const operations = 'schemas/plugin/shell-business-operations.json';
  copyFileSync(
    join(source, '../../contracts/plugin/shell-business-operations.json'),
    join(output, operations),
  );
  files.push(operations);
  mkdirSync(join(output, 'schemas', 'network'), { recursive: true });
  for (const name of [
    'register-result',
    'address',
    'envelope',
    'source-control',
    'protocol',
    'keyset',
    'auto-renewal-v2',
    'send',
  ]) {
    const file = 'schemas/network/' + name + '.schema.json';
    copyFileSync(
      join(source, '../../contracts/network', name + '.schema.json'),
      join(output, file),
    );
    files.push(file);
  }
  const ajvEntry = resolvePluginDependency('ajv/dist/2020.js', {
    repoRoot,
    nodePaths,
  });
  const ajvPkg = resolvePluginDependency('ajv/package.json', {
    repoRoot,
    nodePaths,
  });
  // npm pack excludes nested node_modules. Bundle the pinned validator so both
  // distributed consumers contain the complete manifest closure.
  esbuild.buildSync({
    entryPoints: [ajvEntry],
    bundle: true,
    platform: 'node',
    format: 'cjs',
    target: 'node22',
    outfile: join(output, 'vendor-ajv.cjs'),
    ...(nodePaths.length ? { nodePaths } : {}),
  });
  files.push('vendor-ajv.cjs');
  for (const name of [
    'ajv',
    'fast-deep-equal',
    'fast-uri',
    'json-schema-traverse',
    'require-from-string',
  ]) {
    const packageDirectory = dirname(
      name === 'ajv'
        ? ajvPkg
        : resolveWithNodePaths(ajvPkg, name + '/package.json', nodePaths),
    );
    const relative = 'licenses/' + name + '.txt';
    mkdirSync(join(output, 'licenses'), { recursive: true });
    const license = readdirSync(packageDirectory).find((file) =>
      /^licen[sc]e(?:\.txt|\.md)?$/i.test(file),
    );
    if (!license)
      throw new Error('validator dependency license required: ' + name);
    copyFileSync(join(packageDirectory, license), join(output, relative));
    files.push(relative);
  }

  const manifest = {
    version: 1,
    ...(managedLinux ? { target: 'managed-linux' } : {}),
    files: files.sort().map((path) => ({
      path,
      sha256: createHash('sha256')
        .update(readFileSync(join(output, path)))
        .digest('hex'),
    })),
  };
  writeFileSync(
    join(output, 'broker-manifest.json'),
    JSON.stringify(manifest, null, 2),
  );
  return manifest;
}
if (
  process.argv[1] &&
  resolve(process.argv[1]) === fileURLToPath(import.meta.url)
) {
  const [output, target, ...extra] = process.argv.slice(2);
  if (
    !output ||
    (target !== undefined && target !== '--managed-linux') ||
    extra.length
  )
    throw new Error('usage: build.mjs <output> [--managed-linux]');
  buildBroker(output, { managedLinux: target === '--managed-linux' });
}
