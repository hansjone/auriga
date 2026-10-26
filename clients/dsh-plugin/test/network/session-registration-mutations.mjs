// Explicit mutation proof. Mutants live only in isolated worktree evidence;
// original production sources are never edited by this runner.
import assert from 'node:assert/strict';
import { cp, mkdir, mkdtemp, readFile, writeFile } from 'node:fs/promises';
import { spawnSync } from 'node:child_process';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { join } from 'node:path';

const plugin = fileURLToPath(new URL('../../', import.meta.url));
const root = join(plugin, 'node_modules/.cache/auriga-registration-mutations');
await mkdir(root, { recursive: true });
const source = await readFile(
  new URL('../../src/network/session-registration.js', import.meta.url),
  'utf8',
);
const originalTest = new URL('./session-registration.test.js', import.meta.url);
const testSource = (await readFile(originalTest, 'utf8')).replace(
  /from '([^']+)'/g,
  (whole, specifier) =>
    specifier.startsWith('.')
      ? `from '${new URL(specifier, originalTest).href}'`
      : whole,
);
const mutations = [
  {
    name: 'omit-awaited-durability',
    from: 'const flushed = await host.flush(id);',
    to: 'const flushed = true;',
    pattern: 'join rejects flush false',
  },
  {
    name: 'omit-old-generation-fence',
    from: "if (closed) throw new Error('registration retired');\n    assertCurrent();",
    to: 'void closed; void assertCurrent;',
    pattern: 'old generation register response cannot commit',
  },
];
for (const mutation of mutations) {
  assert.equal(
    source.split(mutation.from).length,
    2,
    'mutation must match exactly once',
  );
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
  const mutant = join(dir, 'src/network/session-registration.js');
  await writeFile(mutant, source.replace(mutation.from, mutation.to));
  const test = join(dir, 'mutation.test.mjs');
  await writeFile(
    test,
    testSource.replace(
      new URL('../../src/network/session-registration.js', originalTest).href,
      pathToFileURL(mutant).href,
    ),
  );
  const compile = spawnSync(
    process.execPath,
    [
      '--jitless',
      '--v8-pool-size=1',
      join(plugin, '../../node_modules/typescript/bin/tsc'),
      '--noEmit',
      '-p',
      join(dir, 'tsconfig.json'),
    ],
    { encoding: 'utf8', windowsHide: true },
  );
  await writeFile(join(dir, 'compile.log'), compile.stdout + compile.stderr);
  assert.equal(
    compile.status,
    0,
    'mutant must typecheck: ' + compile.stdout + compile.stderr,
  );
  const result = spawnSync(
    process.execPath,
    ['--test', '--test-name-pattern=' + mutation.pattern, test],
    { encoding: 'utf8', windowsHide: true, timeout: 30000 },
  );
  await writeFile(join(dir, 'test.log'), result.stdout + result.stderr);
  assert.equal(result.status, 1, 'mutant must fail by assertion');
  assert.match(result.stdout, /ERR_ASSERTION/);
  assert.match(result.stdout, /# cancelled 0/);
  process.stdout.write(
    `${mutation.name}: typecheck=0, test=1, assertion detected\n`,
  );
}
