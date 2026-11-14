import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import { compileFromFile } from 'json-schema-to-typescript';
import { format, resolveConfig } from 'prettier';

test('resource signature types are generated from the authoritative schema', async () => {
  const source = fileURLToPath(
    new URL(
      '../../../../contracts/resource/signatures.schema.json',
      import.meta.url,
    ),
  );
  const target = fileURLToPath(
    new URL('../../src/generated/resource-signatures.d.ts', import.meta.url),
  );
  const generated = await compileFromFile(source, {
    unreachableDefinitions: true,
  });
  const expected = await format(generated, {
    ...(await resolveConfig(target)),
    parser: 'typescript',
  });
  assert.equal(
    (await readFile(target, 'utf8')).replace(/\r\n/g, '\n'),
    expected.replace(/\r\n/g, '\n'),
  );
});
