import {
  mkdirSync,
  readdirSync,
  realpathSync,
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
function tryRealpath(filePath) {
  try {
    return realpathSync(filePath);
  } catch {
    return filePath;
  }
}
export function pnpmVirtualStoreDirs(repoRoot = resolve(source, '../..')) {
  const candidates = [
    join(repoRoot, 'web', 'node_modules', '.pnpm'),
    join(repoRoot, 'web', 'web', 'node_modules', '.pnpm'),
    join(repoRoot, 'node_modules', '.pnpm'),
  ];
  const seen = new Set();
  const dirs = [];
  for (const dir of candidates) {
    if (!existsSync(dir)) continue;
    const key = process.platform === 'win32' ? dir.toLowerCase() : dir;
    if (seen.has(key)) continue;
    seen.add(key);
    dirs.push(dir);
  }
  return dirs;
}
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
      return createRequire(tryRealpath(loader.resolve('vite/package.json')))(
        'esbuild',
      );
    } catch (error) {
      errors.push(`${base} -> vite -> esbuild: ${error.message}`);
    }
    try {
      return loader('esbuild');
    } catch (error) {
      errors.push(`${base} -> esbuild: ${error.message}`);
    }
  }
  for (const pnpmDir of pnpmVirtualStoreDirs(repoRoot)) {
    const hoisted = join(pnpmDir, 'node_modules', 'esbuild');
    if (existsSync(join(hoisted, 'package.json'))) {
      try {
        return createRequire(join(hoisted, 'package.json'))(hoisted);
      } catch (error) {
        errors.push(`${hoisted}: ${error.message}`);
      }
    }
    for (const entry of readdirSync(pnpmDir, { withFileTypes: true })) {
      if (!entry.isDirectory() || entry.name === 'node_modules') continue;
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
  const expanded = [];
  for (const dir of [...configured, ...auto]) {
    expanded.push(dir);
    for (const pkgName of ['ajv', '@nats-io/nats-core', 'undici', 'ws']) {
      const pkgFile = join(dir, pkgName, 'package.json');
      if (!existsSync(pkgFile)) continue;
      const realPkgDir = dirname(tryRealpath(pkgFile));
      const peerNodeModules = dirname(
        pkgName.startsWith('@') ? dirname(realPkgDir) : realPkgDir,
      );
      if (basename(peerNodeModules) === 'node_modules')
        expanded.push(peerNodeModules);
    }
  }
  for (const pnpmDir of pnpmVirtualStoreDirs(repoRoot)) {
    const hoisted = join(pnpmDir, 'node_modules');
    if (existsSync(hoisted)) expanded.push(hoisted);
  }
  const seen = new Set();
  const paths = [];
  for (const dir of expanded) {
    const key = process.platform === 'win32' ? dir.toLowerCase() : dir;
    if (seen.has(key)) continue;
    seen.add(key);
    paths.push(dir);
  }
  return paths;
}
function findRepoRoots(startFile, nodePaths = []) {
  const roots = [];
  const seen = new Set();
  const add = (dir) => {
    const key = process.platform === 'win32' ? dir.toLowerCase() : dir;
    if (seen.has(key)) return;
    seen.add(key);
    roots.push(dir);
  };
  for (const seed of [dirname(startFile), ...nodePaths]) {
    let cur = resolve(seed);
    for (let depth = 0; depth < 8; depth++) {
      if (pnpmVirtualStoreDirs(cur).length) add(cur);
      const parent = dirname(cur);
      if (parent === cur) break;
      cur = parent;
    }
  }
  add(resolve(source, '../..'));
  return roots;
}
export function resolveWithNodePaths(baseFile, specifier, nodePaths = []) {
  const realBaseFile = tryRealpath(baseFile);
  const loader = createRequire(realBaseFile);
  const realDirs = [];
  for (const dir of nodePaths) {
    realDirs.push(dir);
    const realDir = tryRealpath(dir);
    if (realDir !== dir) realDirs.push(realDir);
  }
  const searchRoots = [
    dirname(realBaseFile),
    dirname(baseFile),
    ...realDirs.map((dir) =>
      basename(dir) === 'node_modules' ? dirname(dir) : dir,
    ),
  ];
  try {
    return tryRealpath(loader.resolve(specifier, { paths: searchRoots }));
  } catch (primaryError) {
    for (const dir of realDirs) {
      const candidate = join(dir, specifier);
      try {
        return tryRealpath(loader.resolve(candidate));
      } catch {
        /* Try next configured nodePaths directory. */
      }
    }
    for (const repoCandidate of findRepoRoots(realBaseFile, realDirs)) {
      for (const pnpmDir of pnpmVirtualStoreDirs(repoCandidate)) {
        for (const entry of readdirSync(pnpmDir, { withFileTypes: true })) {
          if (!entry.isDirectory() || entry.name === 'node_modules') continue;
          const candidate = join(
            pnpmDir,
            entry.name,
            'node_modules',
            specifier,
          );
          try {
            return tryRealpath(loader.resolve(candidate));
          } catch {
            /* Try next virtual store package directory. */
          }
        }
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
export function buildBroker(
  output,
  { managedLinux = false, skipWindowsHelper = false } = {},
) {
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
    if (!skipWindowsHelper) {
      const helper = 'windows/shell-helper.exe';
      buildHelper(join(output, helper));
      files.push(helper);
    }
    // ipc.mjs statically imports the Linux Unix-socket peer; ship it in every
    // Desktop broker so Windows packages still resolve the module graph.
    mkdirSync(join(output, 'linux'), { recursive: true });
    const linuxIpc = 'linux/ipc-server.mjs';
    copyFileSync(join(source, linuxIpc), join(output, linuxIpc));
    files.push(linuxIpc);
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
  const [output, ...flags] = process.argv.slice(2);
  const managedLinux = flags.includes('--managed-linux');
  const skipWindowsHelper = flags.includes('--skip-windows-helper');
  if (
    !output ||
    flags.some(
      (flag) => flag !== '--managed-linux' && flag !== '--skip-windows-helper',
    ) ||
    (managedLinux && skipWindowsHelper)
  )
    throw new Error(
      'usage: build.mjs <output> [--managed-linux | --skip-windows-helper]',
    );
  buildBroker(output, { managedLinux, skipWindowsHelper });
}
