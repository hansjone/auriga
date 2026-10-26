// Compilable bad implementations are isolated under this worktree's cache.
import assert from 'node:assert/strict';
import { cp, mkdir, mkdtemp, readFile, writeFile } from 'node:fs/promises';
import { spawnSync } from 'node:child_process';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { join } from 'node:path';

const plugin = fileURLToPath(new URL('../../../', import.meta.url));
const root = join(plugin, 'node_modules/.cache/auriga-source-mutations');
await mkdir(root, { recursive: true });
const originalTest = new URL('./controller.test.js', import.meta.url);
const originalFixture = new URL('./fixtures.js', import.meta.url);
const originalSrc = new URL('../../../src/', import.meta.url);
const mutations = [
  {
    name: 'omit-durable-barrier',
    file: 'network/host/ledger.js',
    from: 'await handle.commit(draft);',
    to: 'void handle.commit(draft);',
    pattern: 'source issue waits for the same ledger durable operation barrier',
  },
  {
    name: 'omit-old-generation-fence',
    file: 'network/source-control/controller.js',
    from: "if (closed) throw new Error('source generation retired');\n    assertCurrent();",
    to: 'void closed; void assertCurrent;',
    pattern: 'old generation fence prevents a delayed Get',
  },
];
for (const mutation of mutations) {
  const dir = await mkdtemp(join(root, mutation.name + '-'));
  await cp(join(plugin, 'src'), join(dir, 'src'), { recursive: true });
  await writeFile(join(dir, 'package.json'), '{"type":"module"}\n');
  await writeFile(
    join(dir, 'tsconfig.json'),
    JSON.stringify({
      extends: join(plugin, 'tsconfig.json'),
      include: ['src/**/*.js', 'src/**/*.d.ts'],
      exclude: ['src/client.js'],
    }),
  );
  const target = join(dir, 'src', mutation.file),
    source = await readFile(target, 'utf8');
  assert.equal(
    source.split(mutation.from).length,
    2,
    'exactly one mutation site',
  );
  await writeFile(target, source.replace(mutation.from, mutation.to));
  async function rewrite(file, destination) {
    const source = await readFile(file, 'utf8');
    await writeFile(
      destination,
      source.replace(/from '([^']+)'/g, (whole, specifier) => {
        if (!specifier.startsWith('.')) return whole;
        const url = new URL(specifier, file);
        if (url.href === originalFixture.href)
          return `from '${pathToFileURL(join(dir, 'fixtures.mjs')).href}'`;
        if (url.href.startsWith(originalSrc.href))
          return `from '${pathToFileURL(join(dir, 'src', fileURLToPath(url).slice(fileURLToPath(originalSrc).length))).href}'`;
        return `from '${url.href}'`;
      }),
    );
  }
  await rewrite(originalTest, join(dir, 'mutation.test.mjs'));
  await rewrite(originalFixture, join(dir, 'fixtures.mjs'));
  const compile = spawnSync(
    process.execPath,
    [
      join(plugin, '../../node_modules/typescript/bin/tsc'),
      '--noEmit',
      '-p',
      join(dir, 'tsconfig.json'),
    ],
    { encoding: 'utf8', windowsHide: true, timeout: 60000 },
  );
  await writeFile(join(dir, 'compile.log'), compile.stdout + compile.stderr);
  assert.equal(
    compile.status,
    0,
    'bad implementation must compile: ' + compile.stdout + compile.stderr,
  );
  const result = spawnSync(
    process.execPath,
    [
      '--test',
      '--test-name-pattern=' + mutation.pattern,
      join(dir, 'mutation.test.mjs'),
    ],
    { encoding: 'utf8', windowsHide: true, timeout: 30000 },
  );
  await writeFile(join(dir, 'test.log'), result.stdout + result.stderr);
  assert.equal(result.status, 1, 'bad implementation must fail assertions');
  assert.match(result.stdout, /ERR_ASSERTION/);
  assert.match(result.stdout, /# cancelled 0/);
  process.stdout.write(
    `${mutation.name}: typecheck=0, test=1, assertion detected\n`,
  );
}
