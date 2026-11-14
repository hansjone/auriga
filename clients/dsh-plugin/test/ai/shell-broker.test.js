import test from 'node:test';
import assert from 'node:assert/strict';
import { createAurigaAdapter } from '../../src/ai/index.js';

const hostApi = {
  LlmAdapter: class {
    prepareCall() {}
    imageRequestPricing() {}
  },
  attributionHeaders: () => ({}),
  resolveRetryPolicy: (v) => v,
  ToolCallId: (v) => v,
};
function fixture() {
  const calls = [];
  let active = true;
  const broker = {
    state: {
      current: () => ({
        state: 'authenticated',
        expiresAt: new Date(Date.now() + 29000).toISOString(),
      }),
      fence: () => () => {
        if (!active) throw new Error('revoked');
      },
    },
    async call(operation, input) {
      calls.push({ operation, input });
      return {
        statusCode: 200,
        headers: { 'content-type': 'application/json' },
        body: {
          object: 'list',
          data: [
            {
              id: 'team-model',
              object: 'model',
              created: 1,
              owned_by: 'auriga',
            },
          ],
        },
      };
    },
  };
  const adapter = createAurigaAdapter({
    hostApi,
    apiBase: 'https://gateway.example.com',
    broker,
    modelLimits: { 'team-model': { contextWindow: 1000, maxOutputTokens: 32 } },
    countInputTokens: () => 1,
  });
  return {
    adapter,
    calls,
    revoke() {
      active = false;
    },
  };
}
test('platform model discovery uses the fixed broker operation without a token or fetch', async () => {
  const f = fixture();
  try {
    const models = await f.adapter.listModels('auriga');
    assert.equal(models[0].id, 'team-model');
    assert.deepEqual(f.calls, [{ operation: 'listAiModels', input: {} }]);
    assert.equal(f.adapter.availability.directoryAvailable, true);
    assert.equal(f.adapter.availability.available, true);
  } finally {
    f.adapter.dispose();
  }
});
test('revoked model discovery cannot publish a late directory', async () => {
  const f = fixture();
  f.revoke();
  await assert.rejects(f.adapter.listModels('auriga'));
  assert.equal(f.adapter.availability.directoryAvailable, false);
  f.adapter.dispose();
});
