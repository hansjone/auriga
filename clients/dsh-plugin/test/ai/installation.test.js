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

test('model lifecycle: catalog and generation admit only visible models with trusted capacity and tokenizer', async (t) => {
  const ctx = new Context();
  const fiber = await ctx.plugin(hostApi.LlmRuntime);
  const revoked = new AbortController();
  let visible = true,
    acquired = 0,
    posts = 0;
  t.mock.method(globalThis, 'fetch', async (url, init) => {
    assert.equal(init.credentials, 'omit');
    if (new URL(url).pathname.endsWith('/models'))
      return Response.json({
        object: 'list',
        data: visible
          ? [{ id: 'trusted', object: 'model', created: 1, owned_by: 'auriga' }]
          : [],
      });
    posts++;
    return new Response(
      `data: ${JSON.stringify({ id: 'response', object: 'chat.completion.chunk', created: 1, model: 'trusted', choices: [{ index: 0, delta: { role: 'assistant', content: 'ok' }, finish_reason: 'stop' }], usage: { prompt_tokens: 3, completion_tokens: 1, total_tokens: 4 } })}\n\ndata: [DONE]\n\n`,
      { headers: { 'content-type': 'text/event-stream' } },
    );
  });
  const models = createModelLifecycle({
    broker: {
      getAiAccess: async () => {
        acquired++;
        return {
          token: 'ai-token',
          expiresAt: Date.now() + 60000,
          signal: revoked.signal,
        };
      },
      status: {},
    },
    origin: 'https://portal.example.com',
    generation: {
      modelLimits: {
        trusted: {
          contextWindow: 128,
          maxOutputTokens: 32,
          defaultMaxTokens: 8,
        },
      },
      countInputTokens: () => 3,
    },
  });
  try {
    await models.attach(ctx.llm);
    models.login();
    assert.equal(models.status.available, false);
    assert.equal(models.status.directoryAvailable, false);
    assert.equal((await ctx.llm.listModels('auriga')).length, 1);
    assert.equal(models.status.directoryAvailable, true);
    assert.equal(models.status.authorized, true);
    assert.equal(models.status.available, true);
    const result = await Array.fromAsync(
      ctx.llm.stream({
        provider: 'auriga',
        model: 'trusted',
        messages: [
          hostApi.createUserMessage({
            source: { kind: 'user' },
            content: [{ type: 'text', text: 'test' }],
          }),
        ],
      }),
    );
    assert.equal(result.at(-1).reason.kind, 'stop');
    assert.equal(posts, 1);
    visible = false;
    assert.deepEqual(await ctx.llm.listModels('auriga'), []);
    assert.equal(models.status.available, false);
    assert.equal(models.status.authorized, false);
    assert.equal(acquired, 3);
    visible = true;
    await ctx.llm.listModels('auriga');
    revoked.abort();
    assert.equal(models.status.directoryAvailable, false);
    assert.equal(models.status.available, false);
    models.logout();
    assert.equal(models.status.available, false);
  } finally {
    models.close();
    await fiber.dispose();
  }
});

for (const generation of [
  undefined,
  { modelLimits: { trusted: { maxOutputTokens: 32 } } },
  { modelLimits: { trusted: { contextWindow: 128, maxOutputTokens: 32 } } },
  {
    modelLimits: { trusted: { contextWindow: 128, maxOutputTokens: 32 } },
    countInputTokens: 'not-a-port',
  },
])
  test('model lifecycle: incomplete generation ports keep directory usable and refuse generation before exchange', async (t) => {
    const ctx = new Context();
    const fiber = await ctx.plugin(hostApi.LlmRuntime);
    let acquired = 0;
    t.mock.method(globalThis, 'fetch', async (url) => {
      assert.equal(new URL(url).pathname, '/ai/v1/models');
      return Response.json({
        object: 'list',
        data: [
          { id: 'trusted', object: 'model', created: 1, owned_by: 'auriga' },
        ],
      });
    });
    const models = createModelLifecycle({
      broker: {
        getAiAccess: async () => {
          acquired++;
          return {
            token: 'ai-token',
            expiresAt: Date.now() + 60000,
            signal: new AbortController().signal,
          };
        },
        status: {},
      },
      origin: 'https://portal.example.com',
      generation,
    });
    try {
      await models.attach(ctx.llm);
      models.login();
      assert.equal((await ctx.llm.listModels('auriga')).length, 1);
      assert.equal(models.status.directoryAvailable, true);
      assert.equal(models.status.available, false);
      const result = await Array.fromAsync(
        ctx.llm.stream({
          provider: 'auriga',
          model: 'trusted',
          maxTokens: 8,
          messages: [
            hostApi.createUserMessage({
              source: { kind: 'user' },
              content: [{ type: 'text', text: 'test' }],
            }),
          ],
        }),
      );
      assert.equal(result.at(-1).reason.kind, 'error');
      assert.equal(acquired, 1);
    } finally {
      models.close();
      await fiber.dispose();
    }
  });
