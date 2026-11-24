import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  mkdirSync,
  mkdtempSync,
  realpathSync,
  rmSync,
  symlinkSync,
  writeFileSync,
} from 'node:fs';
import { tmpdir } from 'node:os';
import { delimiter, dirname, join } from 'node:path';
import {
  resolveEsbuild,
  resolvePluginDependency,
  resolvePluginNodePaths,
  resolveWithNodePaths,
} from '../build.mjs';
import { resolveViteCli } from '../../dsh-plugin/scripts/package-ui.mjs';

function makeTempDir(t, prefix) {
  const dir = mkdtempSync(join(tmpdir(), prefix));
  t.after(() =>
    rmSync(dir, {
      recursive: true,
      force: true,
      maxRetries: 3,
      retryDelay: 100,
    }),
  );
  return dir;
}

function writeFakePkg(dir, name, mainFile = 'index.js', content = '') {
  mkdirSync(join(dir, dirname(mainFile)), { recursive: true });
  writeFileSync(
    join(dir, 'package.json'),
    JSON.stringify({ name, version: '1.0.0', main: mainFile }),
  );
  writeFileSync(
    join(dir, mainFile),
    content || `module.exports = { marker: ${JSON.stringify(name)} };\n`,
  );
}

test('resolveEsbuild falls back to web/node_modules when root node_modules lacks vite/esbuild', (t) => {
  const repo = makeTempDir(t, 'auriga-esbuild-web-');
  writeFileSync(join(repo, 'package.json'), JSON.stringify({ name: 'root' }));
  writeFakePkg(
    join(repo, 'web', 'node_modules', 'vite'),
    'vite',
    'index.js',
    'module.exports = {};\n',
  );
  writeFakePkg(
    join(repo, 'web', 'node_modules', 'esbuild'),
    'esbuild',
    'index.js',
    'module.exports = { marker: "web-esbuild" };\n',
  );

  const esbuild = resolveEsbuild(repo);
  assert.equal(esbuild.marker, 'web-esbuild');
});

test('resolveEsbuild falls back to web/node_modules/.pnpm virtual store', (t) => {
  const repo = makeTempDir(t, 'auriga-esbuild-pnpm-');
  writeFileSync(join(repo, 'package.json'), JSON.stringify({ name: 'root' }));
  writeFakePkg(
    join(
      repo,
      'web',
      'node_modules',
      '.pnpm',
      'esbuild@0.27.7',
      'node_modules',
      'esbuild',
    ),
    'esbuild',
    'index.js',
    'module.exports = { marker: "pnpm-virtual-esbuild" };\n',
  );

  const esbuild = resolveEsbuild(repo);
  assert.equal(esbuild.marker, 'pnpm-virtual-esbuild');
});

test('resolveEsbuild falls back to web/node_modules/.pnpm/node_modules/esbuild and other virtual store packages', (t) => {
  const repo = makeTempDir(t, 'auriga-esbuild-pnpm-hoist-');
  writeFileSync(join(repo, 'package.json'), JSON.stringify({ name: 'root' }));
  writeFakePkg(
    join(repo, 'web', 'node_modules', '.pnpm', 'node_modules', 'esbuild'),
    'esbuild',
    'index.js',
    'module.exports = { marker: "pnpm-hoisted-esbuild" };\n',
  );
  assert.equal(resolveEsbuild(repo).marker, 'pnpm-hoisted-esbuild');

  const repo2 = makeTempDir(t, 'auriga-esbuild-pnpm-nested-');
  writeFileSync(join(repo2, 'package.json'), JSON.stringify({ name: 'root' }));
  writeFakePkg(
    join(
      repo2,
      'web',
      'node_modules',
      '.pnpm',
      '@vitejs+plugin-vue@6.0.10_vite@7.3.1',
      'node_modules',
      'esbuild',
    ),
    'esbuild',
    'index.js',
    'module.exports = { marker: "pnpm-nested-esbuild" };\n',
  );
  assert.equal(resolveEsbuild(repo2).marker, 'pnpm-nested-esbuild');
});

test('resolvePluginNodePaths and resolvePluginDependency resolve plugin deps from AURIGA_CLIENT_PLUGIN_MODULES', (t) => {
  const repo = makeTempDir(t, 'auriga-plugin-deps-');
  const externalModules = join(repo, 'external-plugin-modules');
  const webNm = join(repo, 'web', 'node_modules');
  mkdirSync(join(repo, 'clients', 'dsh-plugin'), { recursive: true });
  writeFileSync(
    join(repo, 'clients', 'dsh-plugin', 'package.json'),
    JSON.stringify({ name: 'auriga-dsh', version: '0.1.0' }),
  );
  mkdirSync(webNm, { recursive: true });

  writeFakePkg(
    join(externalModules, '@nats-io', 'nats-core'),
    '@nats-io/nats-core',
  );
  writeFakePkg(join(externalModules, 'ws'), 'ws');
  mkdirSync(join(externalModules, 'ajv', 'dist'), { recursive: true });
  writeFileSync(
    join(externalModules, 'ajv', 'package.json'),
    JSON.stringify({ name: 'ajv', version: '8.20.0' }),
  );
  writeFileSync(
    join(externalModules, 'ajv', 'dist', '2020.js'),
    'module.exports = {};\n',
  );

  const nodePaths = resolvePluginNodePaths({
    repoRoot: repo,
    env: {
      AURIGA_CLIENT_PLUGIN_MODULES: [externalModules, webNm].join(delimiter),
    },
  });
  assert.deepEqual(nodePaths, [externalModules, webNm]);

  assert.equal(
    resolvePluginDependency('@nats-io/nats-core', {
      repoRoot: repo,
      nodePaths,
    }),
    join(externalModules, '@nats-io', 'nats-core', 'index.js'),
  );
  assert.equal(
    resolvePluginDependency('ws', { repoRoot: repo, nodePaths }),
    join(externalModules, 'ws', 'index.js'),
  );
  assert.equal(
    resolvePluginDependency('ajv/dist/2020.js', { repoRoot: repo, nodePaths }),
    realpathSync(join(externalModules, 'ajv', 'dist', '2020.js')),
  );
});

test('resolveWithNodePaths resolves transitive ajv deps via realpath and virtual store fallback', (t) => {
  const repo = makeTempDir(t, 'auriga-ajv-transitive-');
  const pluginDir = join(repo, 'clients', 'dsh-plugin');
  mkdirSync(pluginDir, { recursive: true });
  writeFileSync(
    join(pluginDir, 'package.json'),
    JSON.stringify({ name: 'auriga-dsh', version: '0.1.0' }),
  );
  const pnpmAjvRoot = join(
    repo,
    'web',
    'web',
    'node_modules',
    '.pnpm',
    'ajv@8.20.0',
    'node_modules',
  );
  writeFakePkg(join(pnpmAjvRoot, 'ajv'), 'ajv');
  writeFakePkg(join(pnpmAjvRoot, 'fast-deep-equal'), 'fast-deep-equal');

  // Case 1: physical copy in clients/dsh-plugin/node_modules/ajv without sibling fast-deep-equal
  const physicalAjv = join(pluginDir, 'node_modules', 'ajv');
  writeFakePkg(physicalAjv, 'ajv');
  const nodePaths = resolvePluginNodePaths({ repoRoot: repo, env: {} });
  assert.equal(
    resolveWithNodePaths(
      join(physicalAjv, 'package.json'),
      'fast-deep-equal/package.json',
      nodePaths,
    ),
    realpathSync(join(pnpmAjvRoot, 'fast-deep-equal', 'package.json')),
  );

  // Case 2: junction/symlink to virtual store
  rmSync(physicalAjv, { recursive: true, force: true });
  symlinkSync(
    join(pnpmAjvRoot, 'ajv'),
    physicalAjv,
    process.platform === 'win32' ? 'junction' : 'dir',
  );
  const nodePathsWithLink = resolvePluginNodePaths({ repoRoot: repo, env: {} });
  assert.equal(nodePathsWithLink.includes(realpathSync(pnpmAjvRoot)), true);
  assert.equal(
    resolveWithNodePaths(
      join(physicalAjv, 'package.json'),
      'fast-deep-equal/package.json',
      nodePathsWithLink,
    ),
    realpathSync(join(pnpmAjvRoot, 'fast-deep-equal', 'package.json')),
  );
});

test('resolveViteCli resolves real vite binary and includes peer node_modules in nodePaths', (t) => {
  const repo = makeTempDir(t, 'auriga-vite-cli-');
  writeFileSync(join(repo, 'package.json'), JSON.stringify({ name: 'root' }));
  const pnpmViteNm = join(
    repo,
    'web',
    'web',
    'node_modules',
    '.pnpm',
    'vite@7.3.1',
    'node_modules',
  );
  writeFakePkg(
    join(pnpmViteNm, 'vite'),
    'vite',
    'bin/vite.js',
    '// vite cli\n',
  );
  writeFakePkg(join(pnpmViteNm, 'rollup'), 'rollup');
  mkdirSync(join(repo, 'node_modules'), { recursive: true });
  symlinkSync(
    join(pnpmViteNm, 'vite'),
    join(repo, 'node_modules', 'vite'),
    process.platform === 'win32' ? 'junction' : 'dir',
  );

  const { viteBin, nodePaths } = resolveViteCli(repo);
  assert.equal(
    viteBin,
    realpathSync(join(pnpmViteNm, 'vite', 'bin', 'vite.js')),
  );
  assert.equal(nodePaths.includes(realpathSync(pnpmViteNm)), true);
});

test('resolveViteCli skips root vite without rollup and selects web/node_modules/vite with rollup', (t) => {
  const repo = makeTempDir(t, 'auriga-vite-rollup-web-');
  writeFileSync(join(repo, 'package.json'), JSON.stringify({ name: 'root' }));
  mkdirSync(join(repo, 'web', 'panel'), { recursive: true });
  writeFileSync(
    join(repo, 'web', 'panel', 'package.json'),
    JSON.stringify({ name: 'panel' }),
  );
  // Root node_modules has vite but no rollup (matches lockfileDir: web layout).
  writeFakePkg(
    join(repo, 'node_modules', 'vite'),
    'vite',
    'bin/vite.js',
    '// root vite\n',
  );
  // web/node_modules has both vite and rollup.
  writeFakePkg(
    join(repo, 'web', 'node_modules', 'vite'),
    'vite',
    'bin/vite.js',
    '// web vite\n',
  );
  writeFakePkg(join(repo, 'web', 'node_modules', 'rollup'), 'rollup');

  const { viteBin, nodePaths } = resolveViteCli(repo);
  assert.equal(
    viteBin,
    realpathSync(join(repo, 'web', 'node_modules', 'vite', 'bin', 'vite.js')),
  );
  assert.equal(
    nodePaths.includes(realpathSync(join(repo, 'web', 'node_modules'))),
    true,
  );
});

test('resolveViteCli falls back to .pnpm vite with rollup and retains first candidate when none resolve rollup', (t) => {
  const repo = makeTempDir(t, 'auriga-vite-rollup-pnpm-');
  writeFileSync(join(repo, 'package.json'), JSON.stringify({ name: 'root' }));
  writeFakePkg(
    join(repo, 'node_modules', 'vite'),
    'vite',
    'bin/vite.js',
    '// root vite\n',
  );
  const pnpmViteNm = join(
    repo,
    'web',
    'node_modules',
    '.pnpm',
    'vite@7.3.1',
    'node_modules',
  );
  writeFakePkg(
    join(pnpmViteNm, 'vite'),
    'vite',
    'bin/vite.js',
    '// pnpm vite\n',
  );
  writeFakePkg(join(pnpmViteNm, 'rollup'), 'rollup');

  assert.equal(
    resolveViteCli(repo).viteBin,
    realpathSync(join(pnpmViteNm, 'vite', 'bin', 'vite.js')),
  );

  // When no candidate can resolve rollup, keep the first vite candidate as fallback.
  const repoFallback = makeTempDir(t, 'auriga-vite-rollup-fallback-');
  writeFileSync(
    join(repoFallback, 'package.json'),
    JSON.stringify({ name: 'root' }),
  );
  writeFakePkg(
    join(repoFallback, 'node_modules', 'vite'),
    'vite',
    'bin/vite.js',
    '// root vite\n',
  );
  assert.equal(
    resolveViteCli(repoFallback).viteBin,
    realpathSync(join(repoFallback, 'node_modules', 'vite', 'bin', 'vite.js')),
  );
});
