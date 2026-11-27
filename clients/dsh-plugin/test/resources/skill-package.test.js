import { test } from 'node:test';
import assert from 'node:assert/strict';
import { gzipSync, gunzipSync } from 'node:zlib';
import { packSkill, unpackSkill } from '../../src/resources/skill-package.js';
const meta = { name: 'example', description: 'Public fixture' };
const files = {
  'SKILL.md':
    '---\nname: example\ndescription: "Public fixture"\n---\nFixture body.',
  'references/note.md': 'Public attachment.',
};
test('text-only bundle with scripts and root dependency manifests round trips exact bytes', () => {
  const withScripts = {
    ...files,
    'requirements.txt': 'requests==2.32.3\n',
    'scripts/run.py': 'print("ok")\n',
    'scripts/helper.sh': 'echo ok\n',
  };
  const archive = packSkill(withScripts, meta),
    restored = unpackSkill(archive, meta);
  for (const [key, value] of Object.entries(withScripts))
    assert.equal(restored[key].toString('utf8'), value);
  assert.deepEqual(archive, packSkill(withScripts, meta));
});
test('disallowed paths, script outside scripts/, escape paths, aliases, metadata mismatch and executable archive members are refused', () => {
  for (const path of [
    'assets/run.js',
    'references/run.sh',
    'scripts/binary.exe',
    'other/file.txt',
    'references/../escape.md',
    'assets/CON.txt',
    'assets/a//b.txt',
  ])
    assert.throws(() => packSkill({ ...files, [path]: 'evil' }, meta), {
      code: 'common.validation_failed',
    });
  assert.throws(
    () => packSkill({ ...files, 'references/NOTE.md': 'alias' }, meta),
    { code: 'common.validation_failed' },
  );
  assert.throws(() => packSkill(files, { ...meta, name: 'different' }), {
    code: 'common.validation_failed',
  });
  const tar = gunzipSync(packSkill(files, meta));
  tar.write('0000700\0', 100);
  tar.fill(32, 148, 156);
  const checksum = tar.subarray(0, 512).reduce((n, b) => n + b, 0);
  tar.write(checksum.toString(8).padStart(6, '0') + '\0 ', 148);
  assert.throws(() => unpackSkill(gzipSync(tar), meta), {
    code: 'common.validation_failed',
  });
});
