import { test } from 'node:test';
import assert from 'node:assert/strict';
import { writeFile, symlink } from 'node:fs/promises';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { join } from 'node:path';
import { fixture } from './fixtures.js';
import { readResourceRoots } from '../../src/resources/trust.js';

const execute = promisify(execFile);

test(
  'Windows trust roots accept an existing path with different casing',
  { skip: process.platform !== 'win32' },
  async (t) => {
    const f = await fixture(t);
    const file = await f.writeRoots();
    const roots = await readResourceRoots(file.toUpperCase());
    assert.equal(roots.releaseKeys.size, 1);
    assert.equal(roots.revocationKeys.size, 1);
  },
);

test(
  'Windows trust roots accept a native 8.3 path alias',
  { skip: process.platform !== 'win32' },
  async (t) => {
    const f = await fixture(t);
    const file = await f.writeRoots();
    const script = join(f.directory, 'short-path.js');
    await writeFile(
      script,
      'var fs = new ActiveXObject("Scripting.FileSystemObject");\n' +
        'WScript.Echo(fs.GetFile(WScript.Arguments(0)).ShortPath);\n',
    );
    const { stdout } = await execute(
      'cscript.exe',
      ['//nologo', '//E:JScript', script, file],
      { timeout: 10000 },
    );
    const shortPath = stdout.trim();
    if (shortPath.toLowerCase() === file.toLowerCase()) {
      t.skip('The temporary volume has no 8.3 alias for this file.');
      return;
    }
    assert.match(shortPath, /~/);
    assert.equal((await readResourceRoots(shortPath)).releaseKeys.size, 1);
  },
);

test('trust roots reject directory links and Windows junctions', async (t) => {
  const f = await fixture(t);
  const file = await f.writeRoots();
  const link = join(f.directory, 'linked-deployment');
  await symlink(
    f.directory,
    link,
    process.platform === 'win32' ? 'junction' : 'dir',
  );
  await assert.rejects(readResourceRoots(join(link, 'roots.json')), {
    code: 'resource.storage_unavailable',
  });
  if (process.platform !== 'win32') {
    const leaf = join(f.directory, 'linked-roots.json');
    await symlink(file, leaf);
    await assert.rejects(readResourceRoots(leaf), {
      code: 'resource.storage_unavailable',
    });
  }
});
