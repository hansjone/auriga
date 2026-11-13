import { checkLease } from '../runtime/guardian.mjs';
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
    if (vector.operation === 'clock') {
      const v = vector.input;
      const result = checkLease(
        v.clock,
        { expiresAt: v.leaseExpiresAt },
        {
          bootId: v.bootId,
          monotonicMillis: v.monotonicNowMillis,
          wallMillis: Date.parse(v.wallNow),
        },
      );
      assert.deepEqual(
        result.valid
          ? { decision: 'accept', reason: 'ok' }
          : { decision: 'reject', reason: result.reason },
        vector.expect,
      );
    }
  });
