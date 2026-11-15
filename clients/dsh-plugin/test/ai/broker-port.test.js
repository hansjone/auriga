import test from 'node:test';
import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { ReadableStream } from 'node:stream/web';
import { setImmediate as nextTurn } from 'node:timers/promises';
import * as hostApi from '@deepseek-ai/dsh-llm';
import { createAurigaAdapter } from '../../src/ai/index.js';
import { createBroker } from '../../src/local/broker.js';
import { brokerFixture } from '../broker-fixture.js';

const input = () => ({
  provider: 'auriga',
  model: 'test-model',
  maxTokens: 8,
  messages: [
    { role: 'user', content: [{ type: 'text', text: 'private prompt' }] },
  ],
});
async function fixture(t, respond, responseMode = 'json') {
  const connection = brokerFixture(),
    original = connection.ipc.call.bind(connection.ipc),
    calls = [],
    cancels = [];
  connection.ipc.call = async (message) => {
    if (!message.call) return original(message);
    connection.validate('ShellAuthBusinessCall', message);
    calls.push(message.call);
    const output = await respond(message.call);
    connection.validate('ShellAuthOutput_' + message.call.operation, output);
    return {
      requestId: message.requestId,
      operation: message.call.operation,
      output,
    };
  };
  connection.ipc.streamCommand = async (command) => {
    cancels.push(command);
  };
  const broker = await createBroker(connection);
  const adapter = createAurigaAdapter({
    hostApi,
    broker,
    apiBase: 'https://platform.example.com',
    responseMode,
    modelLimits: { 'test-model': { contextWindow: 128, maxOutputTokens: 16 } },
    countInputTokens: () => 1,
  });
  t.after(() => {
    adapter.dispose();
    broker.close();
  });
  return { connection, broker, adapter, calls, cancels };
}
test('AI directory and generation use fixed broker operations without platform fetch or credentials', async (t) => {
  t.mock.method(globalThis, 'fetch', () =>
    assert.fail('plugin must not fetch platform'),
  );
  const f = await fixture(t, async ({ operation }) => ({
    statusCode: 200,
    body:
      operation === 'listAiModels'
        ? {
            object: 'list',
            data: [
              {
                id: 'test-model',
                object: 'model',
                created: 1,
                owned_by: 'auriga',
              },
            ],
          }
        : {
            id: 'response',
            object: 'chat.completion',
            usage: { prompt_tokens: 1, completion_tokens: 1, total_tokens: 2 },
            created: 1,
            model: 'test-model',
            choices: [
              {
                index: 0,
                message: { role: 'assistant', content: 'ok' },
                finish_reason: 'stop',
              },
            ],
          },
  }));
  assert.equal((await f.adapter.listModels('auriga'))[0].id, 'test-model');
  const chunks = await Array.fromAsync(f.adapter.stream(input()));
  assert.equal(chunks.at(-1).reason.kind, 'stop', JSON.stringify(chunks));
  assert.deepEqual(
    f.calls.map((call) => call.operation),
    ['listAiModels', 'createAiChatCompletion'],
  );
  assert.equal(JSON.stringify(f.calls).includes('authorization'), false);
});
test('same-generation snapshot renewals do not impose a 30-second model response deadline', async (t) => {
  t.mock.timers.enable({ apis: ['Date', 'setTimeout'], now: 2000000000000 });
  const streamId = randomUUID();
  let source;
  const stream = new ReadableStream({
    start(controller) {
      source = controller;
    },
  });
  const f = await fixture(
    t,
    async () => ({ statusCode: 200, body: { streamId, encoding: 'sse_utf8' } }),
    'sse',
  );
  f.broker.openStream = () => stream;
  const pending = Array.fromAsync(f.adapter.stream(input()));
  const send = (content, finish_reason = null) =>
    source.enqueue(
      Buffer.from(
        'data: ' +
          JSON.stringify({
            id: 'response',
            object: 'chat.completion.chunk',
            created: 1,
            model: 'test-model',
            choices: [{ index: 0, delta: { content }, finish_reason }],
          }) +
          '\n\n',
      ),
    );
  send('a');
  await nextTurn();
  t.mock.timers.tick(20000);
  f.connection.refresh();
  send('b');
  await nextTurn();
  t.mock.timers.tick(20000);
  f.connection.refresh();
  send('', 'stop');
  source.enqueue(Buffer.from('data: [DONE]\n\n'));
  source.close();
  const chunks = await pending;
  assert.equal(chunks.at(-1).reason.kind, 'stop', JSON.stringify(chunks));
});
test('logout interrupts a pending broker call and cancels its late-created stream', async (t) => {
  const entered = Promise.withResolvers(),
    response = Promise.withResolvers(),
    streamId = randomUUID();
  const f = await fixture(
    t,
    async () => {
      entered.resolve();
      return response.promise;
    },
    'sse',
  );
  const pending = Array.fromAsync(f.adapter.stream(input()));
  await entered.promise;
  f.connection.logout();
  const chunks = await pending;
  assert.equal(chunks.at(-1).reason.failure.code, 'AUTH');
  response.resolve({
    statusCode: 200,
    body: { streamId, encoding: 'sse_utf8' },
  });
  await nextTurn();
  assert.equal(
    f.cancels.filter((c) => c.streamId === streamId && c.action === 'cancel')
      .length,
    0,
    'revoked generations cannot send stream commands; C closes their streams',
  );
});
test('broker authentication errors remain sanitized AUTH failures', async (t) => {
  const f = await fixture(t, async () => {
    throw Object.assign(new Error('private upstream secret'), {
      code: 'common.unauthenticated',
    });
  });
  const chunks = await Array.fromAsync(f.adapter.stream(input()));
  assert.equal(chunks.at(-1).reason.failure.code, 'AUTH');
  assert.doesNotMatch(JSON.stringify(chunks), /private upstream secret/);
});
