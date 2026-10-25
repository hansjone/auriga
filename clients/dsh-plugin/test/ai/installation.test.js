import test from 'node:test';
import assert from 'node:assert/strict';
import * as hostApi from '@deepseek-ai/dsh-llm';
import { createRequire } from 'node:module';
import { pathToFileURL } from 'node:url';
import { createModelLifecycle } from '../../src/ai/installation.js';
const require = createRequire(
  createRequire(import.meta.url).resolve('@deepseek-ai/dsh-llm'),
);
const { Context } = await import(
  pathToFileURL(require.resolve('@deepseek-ai/cordis'))
);

test('production model lifecycle: public module resolution, registration, capability refusal, logout and provider removal', async () => {
  const ctx = new Context();
  const fiber = await ctx.plugin(hostApi.LlmRuntime);
  let acquired = 0;
  const models = createModelLifecycle({
    broker: {
      getAiAccess: async () => {
        acquired++;
        throw Error('must not exchange');
      },
      invalidate() {},
      status: { state: 'sign_in_required' },
    },
    origin: 'https://portal.example.com',
  });
  try {
    const detach = await models.attach(ctx.llm);
    assert.deepEqual(ctx.llm.listProviders(), []);
    models.login();
    assert.deepEqual(ctx.llm.listProviders(), [
      { id: 'auriga', name: 'Auriga' },
    ]);
    assert.equal(models.status.registered, true);
    assert.equal(models.status.state, 'model_metadata_unavailable');
    assert.equal(models.status.authorized, false);
    const result = [];
    for await (const chunk of ctx.llm.stream({
      provider: 'auriga',
      model: 'unknown',
      maxTokens: 8,
      messages: [
        hostApi.createUserMessage({
          source: { kind: 'user' },
          content: [{ type: 'text', text: 'test' }],
        }),
      ],
    }))
      result.push(chunk);
    assert.equal(result.at(-1).reason.kind, 'error');
    assert.equal(acquired, 0);
    models.logout();
    assert.deepEqual(ctx.llm.listProviders(), []);
    models.login();
    assert.equal(ctx.llm.listProviders().length, 1);
    detach();
    assert.deepEqual(ctx.llm.listProviders(), []);
    assert.equal(models.status.state, 'llm_unavailable');
    models.close();
    models.close();
  } finally {
    models.close();
    await fiber.dispose();
  }
});

test('model lifecycle: missing public registration capability never disrupts Team/login', async () => {
  const models = createModelLifecycle({
    broker: { status: {} },
    origin: 'https://portal.example.com',
  });
  await models.attach({});
  models.login();
  assert.equal(models.status.registered, false);
  assert.equal(models.status.state, 'llm_unavailable');
  models.close();
});
