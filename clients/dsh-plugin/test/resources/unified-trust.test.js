import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import schemas from '../../src/resources/schema-data.js';
import { evaluateVector } from '../../src/resources/unified-trust.js';
import { schemaValid } from '../../src/resources/schema.js';
const root = new URL('../../../../contracts/resource/', import.meta.url);
test('packaged runtime schemas are exact frozen contract content', async () => {
  for (const name of ['signatures', 'package', 'unified'])
    assert.deepEqual(
      schemas[name],
      JSON.parse(await readFile(new URL(name + '.schema.json', root), 'utf8')),
    );
});
const vectors = JSON.parse(
  await readFile(new URL('unified-vectors.json', root), 'utf8'),
);
for (const vector of vectors.cases)
  test('C unified contract vector: ' + vector.id, () => {
    assert.deepEqual(evaluateVector(vector, vectors.trust), vector.expect);
  });
const localFiles = JSON.parse(
  await readFile(new URL('local-file-fixtures.json', root), 'utf8'),
);
for (const fixture of localFiles.cases)
  test('C frozen local file: ' + fixture.id, () => {
    assert.equal(
      schemaValid('unified', fixture.schema, fixture.input),
      fixture.expect,
    );
  });
test('lock owner accepts ISO bootId while the separate frozen guardian clock retains UUID representation', () => {
  const state = globalThis.structuredClone(
    localFiles.cases.find((f) => f.id === 'UnifiedGuardianStateFile-valid')
      .input,
  );
  const owner = globalThis.structuredClone(
    localFiles.cases.find((f) => f.id === 'host-lock-owner-valid').input,
  );
  const bootId = '2026-11-12T08:30:00.000Z';
  state.clock.bootId = bootId;
  owner.bootId = bootId;
  assert.equal(schemaValid('unified', 'UnifiedHostLockOwner', owner), true);
  assert.equal(
    schemaValid('unified', 'UnifiedGuardianStateFile', state),
    false,
  );
  assert.equal(
    schemas.unified.$defs.UnifiedTrustedClock.properties.bootId.format,
    'uuid',
  );
});
