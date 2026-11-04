import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, mkdir, rm, symlink } from 'node:fs/promises';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import { guardDirectoryPicker } from '../src/managed/directories.js';

test('R7 public browse capability starts at workspace, confines creation and selection, and hides outer crumbs', async (t) => {
  const base = await mkdtemp(join(tmpdir(), 'auriga-directory-'));
  t.after(() => rm(base, { recursive: true, force: true }));
  const root = join(base, 'workspace');
  await mkdir(root);
  await mkdir(join(base, 'outside'));
  await mkdir(join(root, 'inside'));
  const calls = [];
  const capability = {
    kind: 'browse',
    async list(path) {
      calls.push(path);
      return {
        path,
        home: base,
        crumbs: [
          { name: 'outside', path: base },
          { name: 'workspace', path: root },
        ],
        entries: [
          { name: 'outside', path: base },
          { name: 'inside', path: join(root, 'inside') },
        ],
        truncated: false,
      };
    },
    async createDirectory(path, name) {
      calls.push(join(path, name));
      await mkdir(join(path, name));
      return join(path, name);
    },
  };
  const picker = { capability: () => capability };
  const dispose = guardDirectoryPicker(picker, root);
  t.after(dispose);
  const guarded = picker.capability();
  const listing = await guarded.list();
  assert.equal(calls[0], root);
  assert.equal(listing.home, root);
  assert.deepEqual(
    listing.crumbs.map((p) => p.path),
    [root],
  );
  assert.equal(listing.entries.length, 1);
  await guarded.createDirectory(root, 'new-project');
  for (const candidate of [base, root + '-other', join(root, '..')])
    await assert.rejects(guarded.list(candidate), { code: 'common.forbidden' });
  await assert.rejects(guarded.createDirectory(base, 'blocked'), {
    code: 'common.forbidden',
  });
  await assert.rejects(guarded.createDirectory(root, '../blocked'), {
    code: 'common.forbidden',
  });
  if (process.platform !== 'win32') {
    await symlink(join(base, 'outside'), join(root, 'escape'));
    await assert.rejects(guarded.list(join(root, 'escape')), {
      code: 'common.forbidden',
    });
  }
  dispose();
  assert.equal(picker.capability(), capability);
});
