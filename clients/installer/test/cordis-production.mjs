import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { mergeSkillPatch, yamlParser } from '../runtime/skill-config.mjs';
export function checkProductionCordis(yaml) {
  const base = new URL('../../../contracts/resource/', import.meta.url);
  const fixtures = JSON.parse(
    readFileSync(new URL('cordis-upgrade-fixture.json', base)),
  );
  const parse = yamlParser(yaml);
  for (const item of fixtures.cases) {
    const input = readFileSync(new URL(item.inputPath, base), 'utf8');
    const expected = readFileSync(new URL(item.expectedPath, base), 'utf8');
    if (item.expectShared) {
      assert.equal(
        mergeSkillPatch(input, parse, process.cwd()),
        expected,
        item.id,
      );
      assert.equal(
        mergeSkillPatch(expected, parse, process.cwd()),
        expected,
        item.id + ' idempotent',
      );
    } else
      assert.throws(
        () => mergeSkillPatch(input, parse, process.cwd()),
        item.id,
      );
  }
  return fixtures.cases.length;
}
