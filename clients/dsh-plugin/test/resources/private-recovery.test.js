import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import { syncBuiltinESMExports } from 'node:module';
import {
  mkdtempSync,
  mkdirSync,
  rmSync,
  realpathSync,
  writeFileSync,
  readdirSync,
  readFileSync,
} from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createPrivateSkills } from '../../src/resources/private-skills.js';
test('L3: a legacy reserved temporary is retained outside discovery after the writer lock is gone', async (t) => {
  const home = realpathSync.native(
    mkdtempSync(join(tmpdir(), 'auriga-private-legacy-')),
  );
  t.after(() => rmSync(home, { recursive: true, force: true }));
  const skills = createPrivateSkills({ home }),
    source = '---\nname: example\ndescription: "Private"\n---\nOld body';
  await skills.create(source);
  writeFileSync(
    join(home, 'skills/example/.auriga-019a0000-0000-7000-8000-000000000001'),
    'Uncommitted user edit',
  );
  assert.equal((await skills.read('example', true)).files['SKILL.md'], source);
  const temporary = join(home, 'auriga/private-skills/temporary');
  assert.ok(
    readdirSync(temporary).some(
      (name) =>
        readFileSync(join(temporary, name), 'utf8') === 'Uncommitted user edit',
    ),
  );
});
test('M4: a residual private write lock is observable for manual recovery and never hides readable skills', async (t) => {
  const home = realpathSync.native(
    mkdtempSync(join(tmpdir(), 'auriga-private-lock-')),
  );
  t.after(() => rmSync(home, { recursive: true, force: true }));
  const skills = createPrivateSkills({ home });
  await skills.create(
    '---\nname: example\ndescription: "Private"\n---\nOld body',
  );
  const lock = join(home, 'auriga/private-skills/write.lock.d');
  mkdirSync(lock);
  assert.equal((await skills.writeLock()).path, lock);
  assert.equal((await skills.list()).length, 1);
  await assert.rejects(
    skills.create('---\nname: other\ndescription: "Other"\n---\nOther body'),
    { code: 'common.conflict' },
  );
});
test('L3: private snapshots remain readable while an edit is staged and before its atomic replacement', async (t) => {
  const home = realpathSync.native(
    mkdtempSync(join(tmpdir(), 'auriga-private-edit-')),
  );
  t.after(() => rmSync(home, { recursive: true, force: true }));
  const skills = createPrivateSkills({ home }),
    source = '---\nname: example\ndescription: "Private"\n---\nOld body';
  const row = await skills.create(source);
  const original = fs.rename;
  let observed = false;
  const replacement = t.mock.method(fs, 'rename', async (from, to) => {
    observed = true;
    assert.equal(
      (await skills.read('example', true)).files['SKILL.md'],
      source,
    );
    return original(from, to);
  });
  syncBuiltinESMExports();
  try {
    const changed = await skills.change({
      ...row,
      action: 'edit',
      source: source.replace('Old body', 'New body'),
    });
    assert.match(changed.files['SKILL.md'], /New body/);
    assert.equal(observed, true);
  } finally {
    replacement.mock.restore();
    syncBuiltinESMExports();
  }
});
