/** Child-only assertions, also run under an administrator token without restore bypass. */
import assert from 'node:assert/strict';
import { existsSync, mkdirSync, writeFileSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import { join } from 'node:path';

const runtime = process.argv[2];
for (const [name, operation] of [
  ['overwrite', () => writeFileSync(join(runtime, 'runtime.json'), '{}')],
  [
    'append',
    () => writeFileSync(join(runtime, 'python.exe'), 'append', { flag: 'a' }),
  ],
  ['create file', () => writeFileSync(join(runtime, 'unlisted.pyc'), 'cache')],
  ['create directory', () => mkdirSync(join(runtime, '__pycache__'))],
  [
    'nested overwrite',
    () =>
      writeFileSync(join(runtime, 'Lib/site-packages/fixture.py'), 'modified'),
  ],
  [
    'nested create',
    () => writeFileSync(join(runtime, 'Lib/site-packages/extra.pyc'), 'cache'),
  ],
])
  assert.throws(operation, /EPERM|EACCES/, `${name} must be denied`);

const child = spawnSync(
  process.execPath,
  [
    '-e',
    'require("node:fs").writeFileSync(process.argv[1], "cache")',
    join(runtime, 'child.pyc'),
  ],
  {
    env: { ...process.env, PYTHONDONTWRITEBYTECODE: '' },
    encoding: 'utf8',
    windowsHide: true,
  },
);
assert.notEqual(child.status, 0, child.stderr);
assert.equal(existsSync(join(runtime, 'child.pyc')), false);
console.log('all seven Node write-denial assertions passed');
