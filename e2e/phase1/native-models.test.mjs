import test from 'node:test';
import assert from 'node:assert/strict';
import { configureRelayProvider, waitAssistant } from './native-models.mjs';

test('native relay settings use a unique credential and CAS provider without overwriting settings', async () => {
  const calls = [],
    cleanup = [];
  const namespace = {
    ns: 'llm-pi-ai',
    revision: 7,
    value: { providers: { existing: {} } },
  };
  const client = {
    rpc: async (method, args) => {
      calls.push({ method, args });
      if (method === 'settings/describe')
        return { writable: true, namespaces: [namespace] };
    },
  };
  const provider = 'auriga-relay-123456abcdef';
  await configureRelayProvider(
    client,
    {
      baseUrl: 'https://portal.example.com/ai/v1',
      contextWindow: 32768,
      maxTokens: 256,
    },
    provider,
    'sk-local-test',
    'pool',
    cleanup,
  );
  const set = calls.find((x) => x.method === 'settings/mutate').args;
  assert.equal(set.expectedRevision, 7);
  assert.deepEqual(set.ops[0].path, ['providers', provider]);
  assert.equal(set.ops[0].value.api, 'openai-completions');
  assert.deepEqual(set.ops[0].value.compat, { supportsStore: false });
  assert.equal(JSON.stringify(set).includes('sk-local-test'), false);
  assert.equal(set.ops[0].value.models[0].id, 'pool');
  for (const action of cleanup.reverse()) await action();
  assert.equal(calls.at(-1).method, 'credentials/unset');
  assert.deepEqual(calls.at(-2).args.ops, [
    { op: 'unset', path: ['providers', provider] },
  ]);
  namespace.value.providers[provider] = {};
  await assert.rejects(
    configureRelayProvider(
      client,
      {
        baseUrl: 'https://portal.example.com/ai/v1',
        contextWindow: 32768,
        maxTokens: 256,
      },
      provider,
      'sk-local-test',
      'pool',
      [],
    ),
  );
});

test('native conversation proof requires an assistant event with the fresh marker and nonzero tokens', async () => {
  let complete = false;
  const client = {
    rpc: async (method) =>
      method === 'session/projections'
        ? { asOfSeq: 10 }
        : {
            records: [
              {
                event: {
                  type: 'user/message',
                  data: {
                    message: { content: [{ type: 'text', text: 'marker' }] },
                  },
                },
              },
              ...(complete
                ? [
                    {
                      event: {
                        type: 'assistant/message',
                        data: {
                          message: {
                            content: [{ type: 'text', text: 'marker' }],
                          },
                          usage: { inputTokens: 1, outputTokens: 2 },
                        },
                      },
                    },
                  ]
                : []),
            ],
          },
  };
  await assert.rejects(waitAssistant(client, 'session', 'marker', 1));
  complete = true;
  const result = await waitAssistant(client, 'session', 'marker', 1000);
  assert.equal(result.markerObserved, true);
});
