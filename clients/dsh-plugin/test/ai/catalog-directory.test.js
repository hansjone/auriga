import test from 'node:test';
import assert from 'node:assert/strict';
import { modelDirectory } from '../../src/ai/catalog.js';

const row = {
  id: 'auto',
  object: 'model',
  created: 0,
  owned_by: 'auriga',
  context_window: 64000,
  max_output_tokens: 8192,
};
test('the standard directory accepts disclosed pool limits and unknown creation sentinel', () => {
  assert.deepEqual(modelDirectory({ object: 'list', data: [row] }), [row]);
  assert.deepEqual(
    modelDirectory({
      object: 'list',
      data: [{ id: 'legacy', object: 'model', created: 5, owned_by: 'auriga' }],
    })[0].id,
    'legacy',
  );
});
for (const patch of [
  { created: undefined },
  { created: -1 },
  { context_window: 0 },
  { max_output_tokens: 64000 },
  { max_output_tokens: 1.5 },
  { endpoint: 'https://other.example' },
  { owned_by: 'other' },
  { id: '../other' },
]) {
  test(
    'strict directory rejects malformed metadata: ' + JSON.stringify(patch),
    () => {
      assert.throws(() =>
        modelDirectory({ object: 'list', data: [{ ...row, ...patch }] }),
      );
    },
  );
}
test('duplicate identities and unbounded directories reject atomically', () => {
  assert.throws(() => modelDirectory({ object: 'list', data: [row, row] }));
  assert.throws(() =>
    modelDirectory({ object: 'list', data: Array(1001).fill(row) }),
  );
});
