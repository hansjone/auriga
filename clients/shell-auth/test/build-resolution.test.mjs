import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { delimiter, join } from 'node:path';
import {
  resolveEsbuild,
  resolvePluginDependency,
  resolvePluginNodePaths,
} from '../build.mjs';

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
  mkdirSync(dir, { recursive: true });
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
    join(externalModules, 'ajv', 'dist', '2020.js'),
  );
});
