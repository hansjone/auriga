import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { evaluateVector } from '../runtime/resource-trust.mjs';
const vectors = JSON.parse(
  readFileSync(
    new URL(
      '../../../contracts/resource/unified-vectors.json',
      import.meta.url,
    ),
  ),
);
for (const vector of vectors.cases)
  test(`Desktop frozen vector: ${vector.id}`, () => {
    assert.deepEqual(evaluateVector(vector, vectors.trust), vector.expect);
  });
