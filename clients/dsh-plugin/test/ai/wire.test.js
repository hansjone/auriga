import test from 'node:test';
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { pathToFileURL } from 'node:url';
import * as hostApi from '@deepseek-ai/dsh-llm';
import { registerAurigaAdapter } from '../../src/ai/index.js';
import { responseBroker } from './response-broker.js';
import { ReadableStream } from 'node:stream/web';
import { setImmediate as nextTurn } from 'node:timers/promises';

const require = createRequire(
  createRequire(import.meta.url).resolve('@deepseek-ai/dsh-llm'),
);
const { Context } = await import(
  pathToFileURL(require.resolve('@deepseek-ai/cordis'))
);

test('wire: public LlmRuntime follows fixed broker streams across renewal and cancels both on logout', async (t) => {
  t.mock.timers.enable({ apis: ['Date'], now: Date.now() });
  let until = Date.now() + 29000;
  const sources = [],
    events = [[], []];
  const packet = (content) =>
    Buffer.from(
      'data: ' +
        JSON.stringify({
          id: 'synthetic',
          object: 'chat.completion.chunk',
          created: 1,
          model: 'synthetic',
          choices: [{ index: 0, delta: { content }, finish_reason: null }],
        }) +
        '\n\n',
    );
  const broker = responseBroker(
    async (operation) => {
      assert.equal(operation, 'createAiChatCompletionStream');
      return new Response(
        new ReadableStream({
          start(controller) {
            sources.push(controller);
            controller.enqueue(packet('initial'));
          },
        }),
        { headers: { 'content-type': 'text/event-stream' } },
      );
    },
    { expiresAt: () => until },
  );
  const ctx = new Context(),
    fiber = await ctx.plugin(hostApi.LlmRuntime);
  const registration = registerAurigaAdapter(ctx.llm, {
    hostApi,
    broker,
    apiBase: 'https://platform.example.com',
    modelLimits: { synthetic: { maxOutputTokens: 32, contextWindow: 100 } },
    countInputTokens: () => 1,
  });
  const input = {
    provider: 'auriga',
    model: 'synthetic',
    maxTokens: 8,
    messages: [
      hostApi.createUserMessage({
        source: { kind: 'user' },
        content: [{ type: 'text', text: 'synthetic-only' }],
      }),
    ],
  };
  const consume = async (index) => {
    for await (const chunk of ctx.llm.stream(input)) events[index].push(chunk);
  };
  const streams = [consume(0), consume(1)];
  const waitFor = async (predicate) => {
    for (let i = 0; i < 100 && !predicate(); i++) await nextTurn();
    assert.ok(predicate(), JSON.stringify(events));
  };
  try {
    await waitFor(() =>
      events.every((list) => JSON.stringify(list).includes('initial')),
    );
    t.mock.timers.tick(20000);
    until = Date.now() + 29000;
    t.mock.timers.tick(20000);
    for (const source of sources) source.enqueue(packet('afterRenewal'));
    await waitFor(() =>
      events.every((list) => JSON.stringify(list).includes('afterRenewal')),
    );
    assert.equal(
      events.some((list) => list.some((e) => e.type === 'finish')),
      false,
    );
    broker.invalidate();
    await Promise.all(streams);
    for (const list of events)
      assert.equal(list.at(-1).reason.failure.code, 'AUTH');
    assert.equal(broker.calls.length, 2);
    assert.equal(
      broker.calls.some((c) => c.input.headers || c.input.token || c.input.url),
      false,
    );
  } finally {
    broker.invalidate();
    registration.dispose();
    await fiber.dispose();
  }
});
