import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import Ajv from 'ajv/dist/2020.js';

const schema = JSON.parse(
  await readFile(
    new URL(
      '../../../contracts/plugin/managed-model.schema.json',
      import.meta.url,
    ),
    'utf8',
  ),
);
const validate = new Ajv({ strict: false, validateFormats: false }).compile(
  schema,
);
const material = (models) => ({
  tenantId: '00000000-0000-4000-8000-000000000001',
  ownerId: '00000000-0000-4000-8000-000000000002',
  instanceId: '00000000-0000-4000-8000-000000000003',
  credentialVersion: 1,
  credential: `amc.00000000-0000-4000-8000-000000000001.00000000-0000-4000-8000-000000000002.00000000-0000-4000-8000-000000000003.00000000-0000-4000-8000-000000000004.${'a'.repeat(43)}`,
  baseUrl: 'https://portal.example.com/ai/v1',
  models,
});
test('managed material schema preserves missing and large safe capability metadata', () => {
  assert.equal(
    validate(
      material([
        { id: 'unknown' },
        { id: 'context', contextWindow: 2000000 },
        { id: 'output', maxOutputTokens: 131072 },
        { id: 'large', contextWindow: 2000000, maxOutputTokens: 131072 },
      ]),
    ),
    true,
    JSON.stringify(validate.errors),
  );
});
for (const [field, values] of [
  [
    'contextWindow',
    [0, 1, -1, 1.5, null, '2000000', Number.MAX_SAFE_INTEGER + 1],
  ],
  [
    'maxOutputTokens',
    [0, -1, 1.5, null, '131072', Number.MAX_SAFE_INTEGER + 1],
  ],
])
  for (const value of values)
    test(
      'managed schema rejects invalid provided capacity: ' +
        field +
        '=' +
        value,
      () => {
        assert.equal(
          validate(material([{ id: 'invalid', [field]: value }])),
          false,
        );
      },
    );
