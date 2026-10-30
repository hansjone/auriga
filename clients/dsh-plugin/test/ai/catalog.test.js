import test from 'node:test';
import assert from 'node:assert/strict';
import * as hostApi from '@deepseek-ai/dsh-llm';
import { createAurigaAdapter } from '../../src/ai/index.js';

const page = (visible) =>
  Response.json({
    object: 'list',
    data: visible
      ? [{ id: 'trusted', object: 'model', created: 1, owned_by: 'auriga' }]
      : [],
  });

test('catalog: a late response cannot restore an older ACL result', async () => {
  const first = Promise.withResolvers(),
    second = Promise.withResolvers();
  const entered = [Promise.withResolvers(), Promise.withResolvers()];
  let requests = 0;
  const adapter = createAurigaAdapter({
    hostApi,
    apiBase: 'https://portal.example.com',
    fetch: async () => {
      const index = requests++;
      entered[index].resolve();
      return [first, second][index].promise;
    },
    getAccessToken: async () => ({
      token: 'ai-token',
      expiresAt: Date.now() + 60000,
      signal: new AbortController().signal,
    }),
    modelLimits: { trusted: { contextWindow: 128, maxOutputTokens: 32 } },
    countInputTokens: () => 3,
  });
  try {
    const old = adapter.listModels('auriga');
    const rejected = assert.rejects(old, /AUTH/);
    await entered[0].promise;
    const current = adapter.listModels('auriga');
    await entered[1].promise;
    second.resolve(page(false));
    assert.deepEqual(await current, []);
    first.resolve(page(true));
    await rejected;
    assert.deepEqual(adapter.availability, {
      directoryAvailable: true,
      authorized: false,
      available: false,
    });
  } finally {
    adapter.dispose();
  }
});

test('catalog: expiry and failed directory refresh invalidate availability', async (t) => {
  t.mock.timers.enable({ apis: ['Date'], now: 2000000000000 });
  let denied = false;
  const adapter = createAurigaAdapter({
    hostApi,
    apiBase: 'https://portal.example.com',
    fetch: async () =>
      denied ? new Response(null, { status: 403 }) : page(true),
    getAccessToken: async () => ({
      token: 'ai-token',
      expiresAt: Date.now() + 60000,
      signal: new AbortController().signal,
    }),
    modelLimits: { trusted: { contextWindow: 128, maxOutputTokens: 32 } },
    countInputTokens: () => 3,
  });
  try {
    await adapter.listModels('auriga');
    assert.equal(adapter.availability.available, true);
    t.mock.timers.tick(60001);
    assert.equal(adapter.availability.available, false);
    assert.equal(adapter.availability.directoryAvailable, false);
    await adapter.listModels('auriga');
    assert.equal(adapter.availability.available, true);
    denied = true;
    await assert.rejects(adapter.listModels('auriga'), /AUTH/);
    assert.deepEqual(adapter.availability, {
      directoryAvailable: false,
      authorized: false,
      available: false,
    });
  } finally {
    adapter.dispose();
  }
});
