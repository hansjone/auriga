import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  mkdtemp,
  mkdir,
  readFile,
  writeFile,
  symlink,
  rm,
  readdir,
} from 'node:fs/promises';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import {
  createPrivateSkills,
  validatePrivateSkill,
} from '../../src/resources/private-skills.js';

const source = (name = 'example', body = 'private body') =>
  `---\nname: ${name}\ndescription: "Private example"\nuser-invocable: true\n---\n${body}\n`;
test('native YAML block scalars and metadata are supported without tags, aliases or duplicate keys', () => {
  const text =
    '---\nname: example\ndescription: >-\n  Native multi-line\n  description\nwhenToUse: |\n  Use this fixture.\nmetadata:\n  category: docs\n  nested: [one, two]\nuser-invocable: false\n---\nBody';
  assert.equal(
    validatePrivateSkill(text).description,
    'Native multi-line description',
  );
  for (const input of [
    text.replace('category: docs', 'category: &a docs\n  copy: *a'),
    text.replace('category: docs', 'category: !!str docs'),
    text.replace('category: docs', 'category: .nan'),
    text.replace('user-invocable: false', 'user-invocable: "false"'),
  ])
    assert.throws(() => validatePrivateSkill(input), {
      code: 'common.validation_failed',
    });
});
async function fixture(t) {
  const home = await mkdtemp(join(tmpdir(), 'auriga-private-'));
  t.after(() => rm(home, { recursive: true, force: true }));
  return { home, store: createPrivateSkills({ home }) };
}
test('private lifecycle preserves attachments, stays out of shared roots, and freezes immutable bytes', async (t) => {
  const { home, store } = await fixture(t);
  await store.create(source());
  const asset = join(home, 'skills/example/references');
  await mkdir(asset);
  await writeFile(join(asset, 'note.md'), 'private attachment');
  let row = await store.read('example', true);
  const frozen = await store.change({ ...row, action: 'freeze' });
  const snapshotPath = join(
    home,
    'auriga/private-skills/frozen',
    frozen.snapshotId + '.json',
  );
  const original = await readFile(snapshotPath, 'utf8');
  row = await store.change({
    ...row,
    action: 'edit',
    source: source('example', 'changed'),
  });
  assert.equal(row.files['references/note.md'], 'private attachment');
  assert.equal(await readFile(snapshotPath, 'utf8'), original);
  await store.change({ ...row, action: 'disable' });
  assert.deepEqual(await readdir(join(home, 'skills')), []);
  row = await store.read('example', false);
  await store.change({ ...row, action: 'enable' });
  row = await store.read('example', true);
  await store.change({ ...row, action: 'remove' });
  assert.deepEqual(await store.list(), []);
  assert.equal(
    (await readdir(join(home, 'auriga/private-skills/removed'))).length,
    1,
  );
  await assert.rejects(
    readFile(join(home, 'auriga/skills-active/current/example/SKILL.md')),
    { code: 'ENOENT' },
  );
});
test('concurrent edits and manual changes cannot overwrite a stale revision', async (t) => {
  const { home, store } = await fixture(t);
  const row = await store.create(source());
  await writeFile(
    join(home, 'skills/example/SKILL.md'),
    source('example', 'manual edit'),
  );
  await assert.rejects(store.change({ ...row, action: 'remove' }), {
    code: 'common.conflict',
  });
  const current = await store.read('example', true);
  const results = await Promise.allSettled(
    [1, 2].map((n) =>
      store.change({
        ...current,
        action: 'edit',
        source: source('example', String(n)),
      }),
    ),
  );
  assert.equal(results.filter((r) => r.status === 'fulfilled').length, 1);
  assert.equal(results.filter((r) => r.status === 'rejected').length, 1);
});
test('invalid YAML, duplicate fields, invocation strings and unsafe names are refused', () => {
  for (const text of [
    source('../escape'),
    source('con'),
    source().replace('user-invocable: true', 'user-invocable: "false"'),
    source().replace('name: example', 'name: example\nname: duplicate'),
    source().replace(
      'description: "Private example"',
      'description: !tag text',
    ),
    source().replace(
      'description: "Private example"',
      'description: &anchor text',
    ),
  ]) {
    if (text.includes('name: con\n')) continue; // Windows path validation is in the store.
    assert.throws(() => validatePrivateSkill(text), {
      code: 'common.validation_failed',
    });
  }
});
test('no overwrite, name takeover, executable attachments, stale lock cleanup or path traversal', async (t) => {
  const { home, store } = await fixture(t);
  const row = await store.create(source());
  await assert.rejects(store.create(source()), { code: 'EEXIST' });
  await assert.rejects(store.create(source('con')), {
    code: 'common.validation_failed',
  });
  await assert.rejects(
    store.change({ ...row, action: 'edit', source: source('different') }),
    { code: 'common.validation_failed' },
  );
  await assert.rejects(store.read('../escape', true), {
    code: 'common.validation_failed',
  });
  await writeFile(join(home, 'skills/example/run.js'), 'process.exit()');
  await assert.rejects(store.read('example', true), {
    code: 'common.validation_failed',
  });
  await mkdir(join(home, 'auriga/private-skills/write.lock.d'));
  await assert.rejects(store.create(source('second')), {
    code: 'common.conflict',
  });
  assert.deepEqual(
    await readdir(join(home, 'auriga/private-skills/write.lock.d')),
    [],
  );
});
test('links in the native root and attachment trees fail closed', async (t) => {
  const { home, store } = await fixture(t);
  await store.create(source());
  const outside = join(home, 'outside');
  await mkdir(outside);
  await symlink(
    outside,
    join(home, 'skills/example/assets'),
    process.platform === 'win32' ? 'junction' : 'dir',
  );
  await assert.rejects(store.read('example', true), {
    code: 'common.validation_failed',
  });
  const other = join(home, 'other');
  await mkdir(other);
  await symlink(
    other,
    join(home, 'linked-home'),
    process.platform === 'win32' ? 'junction' : 'dir',
  );
  await assert.rejects(
    createPrivateSkills({ home: join(home, 'linked-home') }).list(),
    { code: 'resource.storage_unavailable' },
  );
});
