import test from 'node:test';
import assert from 'node:assert/strict';
import {
  mkdtempSync,
  mkdirSync,
  writeFileSync,
  existsSync,
  readFileSync,
  symlinkSync,
  rmSync,
} from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { removeProjectReferences } from './prepare.mjs';

function fixture(t) {
  const root = mkdtempSync(join(tmpdir(), 'auriga-project-refs-'));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const store = join(root, 'store');
  const projects = join(store, 'v11/projects');
  mkdirSync(projects, { recursive: true });
  return { root, store, projects };
}

test('missing projects directory is a no-op', (t) => {
  const { root } = fixture(t);
  assert.doesNotThrow(() => removeProjectReferences(join(root, 'absent')));
});

test('project junction removal never traverses the external target', (t) => {
  const { root, store, projects } = fixture(t);
  const target = join(root, 'external');
  mkdirSync(target);
  writeFileSync(join(target, 'keep'), 'unchanged');
  symlinkSync(
    target,
    join(projects, 'reference'),
    process.platform === 'win32' ? 'junction' : 'dir',
  );
  removeProjectReferences(store);
  assert.equal(existsSync(join(projects, 'reference')), false);
  assert.equal(readFileSync(join(target, 'keep'), 'utf8'), 'unchanged');
});

test('nonempty ordinary directories are rejected and preserved', (t) => {
  const { store, projects } = fixture(t);
  const unknown = join(projects, 'unknown');
  mkdirSync(unknown);
  writeFileSync(join(unknown, 'keep'), 'unchanged');
  assert.throws(() => removeProjectReferences(store), /pnpm projects/);
  assert.equal(readFileSync(join(unknown, 'keep'), 'utf8'), 'unchanged');
});

test('ordinary files are rejected and preserved', (t) => {
  const { store, projects } = fixture(t);
  const unknown = join(projects, 'unknown');
  writeFileSync(unknown, 'unchanged');
  assert.throws(() => removeProjectReferences(store), /pnpm projects/);
  assert.equal(readFileSync(unknown, 'utf8'), 'unchanged');
});

test('empty directory fallback uses nonrecursive removal', (t) => {
  const { store, projects } = fixture(t);
  const empty = join(projects, 'empty');
  mkdirSync(empty);
  removeProjectReferences(store);
  assert.equal(existsSync(empty), false);
});

for (const [linkedPart, storeParts, targetParts] of [
  ['store', [], ['v11', 'projects']],
  ['v11', ['store'], ['projects']],
  ['projects', ['store', 'v11'], []],
]) {
  test(`refuses to enumerate projects through a linked ${linkedPart}`, (t) => {
    const root = mkdtempSync(join(tmpdir(), 'auriga-project-refs-'));
    t.after(() => rmSync(root, { recursive: true, force: true }));
    const target = join(root, 'external');
    const externalProject = join(target, ...targetParts, 'keep');
    const parent = join(root, ...storeParts);
    mkdirSync(externalProject, { recursive: true });
    mkdirSync(parent, { recursive: true });
    const link = join(parent, linkedPart);
    symlinkSync(
      target,
      link,
      process.platform === 'win32' ? 'junction' : 'dir',
    );
    assert.throws(
      () => removeProjectReferences(join(root, 'store')),
      /pnpm projects 的路径包含非普通目录/,
    );
    assert.equal(existsSync(externalProject), true);
    assert.equal(existsSync(link), true);
  });
}
