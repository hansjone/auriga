import test from 'node:test';
import assert from 'node:assert/strict';
import { buildRequest } from '../../src/ai/request.js';
import { readFileSync } from 'node:fs';
import Ajv from 'ajv';
import { generateContract } from './generate-contract.js';

const limits = {
  maxOutputTokens: 128,
  tools: true,
  temperature: true,
  stop: true,
};
const input = () => ({
  provider: 'auriga',
  model: 'team-model',
  maxTokens: 32,
  messages: [{ role: 'user', content: [{ type: 'text', text: '你好' }] }],
});

test('request: generated data-plane declarations match the authoritative contract', async () => {
  assert.equal(
    readFileSync(
      new URL('../../src/ai/contract.d.ts', import.meta.url),
      'utf8',
    ),
    await generateContract(),
  );
});

test('request: D2 whitelist and fixed single choice without forbidden n', () => {
  const body = buildRequest(input(), limits, true);
  assert.deepEqual(body, {
    model: 'team-model',
    messages: [{ role: 'user', content: '你好' }],
    max_tokens: 32,
    stream: true,
  });
});

test('request: rejects unknown options, unsupported content and unsafe limits', () => {
  for (const extra of [
    { n: 1 },
    { baseURL: 'https://example.com' },
    { reasoningEffort: 'high' },
    { maxTokens: 0 },
    { maxTokens: 129 },
    { top_p: 1 },
  ]) {
    assert.throws(() => buildRequest({ ...input(), ...extra }, limits, true), {
      code: 'UNSUPPORTED_OPTION',
    });
  }
  for (const type of ['image', 'audio', 'reasoning', 'file', 'tool-addition']) {
    const options = input();
    options.messages[0].content = [{ type, text: 'secret' }];
    assert.throws(() => buildRequest(options, limits, true), {
      code: 'UNSUPPORTED_OPTION',
    });
  }
  assert.throws(() => buildRequest(input(), {}, true), {
    code: 'UNSUPPORTED_OPTION',
  });
});

test('request: tool history correlation and known capabilities', () => {
  const options = input();
  options.messages = [
    {
      role: 'assistant',
      content: [
        {
          type: 'tool-call',
          id: 'call-1',
          name: 'lookup',
          arguments: '{"q":1}',
        },
      ],
    },
    {
      role: 'tool',
      toolCallId: 'call-1',
      source: { kind: 'tool', callId: 'call-1' },
      content: [{ type: 'text', text: 'found' }],
    },
    ...options.messages,
  ];
  options.tools = [
    { name: 'lookup', description: '查询', parameters: { type: 'object' } },
  ];
  assert.equal(
    buildRequest(options, limits, false).messages[1].tool_call_id,
    'call-1',
  );
  options.messages[1].toolCallId = 'missing';
  assert.throws(() => buildRequest(options, limits, true), {
    code: 'UNSUPPORTED_OPTION',
  });
});

test('request: non-wire tool history does not reject projected text or tool continuations', () => {
  const history = {
    tools: [],
    updates: [
      {
        messageId: 'tool-update',
        additions: [
          {
            name: 'lookup',
            description: 'Find',
            parameters: { type: 'object' },
            deferLoading: true,
          },
        ],
      },
    ],
  };
  const options = {
    ...input(),
    tools: [
      { name: 'lookup', description: 'Find', parameters: { type: 'object' } },
    ],
    toolHistory: history,
  };
  let measured;
  const projected = buildRequest(
    options,
    { ...limits, contextWindow: 100 },
    true,
    (body) => {
      measured = body;
      return 68;
    },
  );
  assert.deepEqual(
    projected,
    buildRequest({ ...options, toolHistory: undefined }, limits, true),
  );
  assert.deepEqual(measured, projected);
  assert.equal('toolHistory' in projected, false);
  const continuation = {
    ...options,
    messages: [
      ...options.messages,
      {
        role: 'assistant',
        content: [
          { type: 'tool-call', id: 'call-1', name: 'lookup', arguments: '{}' },
        ],
      },
      {
        role: 'tool',
        toolCallId: 'call-1',
        source: { kind: 'tool', callId: 'call-1' },
        content: [{ type: 'text', text: 'found' }],
      },
    ],
  };
  assert.equal(
    buildRequest(continuation, limits, true).messages.at(-1).tool_call_id,
    'call-1',
  );
  for (const messages of [
    [
      {
        role: 'developer',
        content: [{ type: 'tool-addition', toolName: 'lookup' }],
      },
    ],
    [{ role: 'user', content: [{ type: 'image', attachment: {} }] }],
    [{ role: 'user', content: [{ type: 'tool-removal', toolName: 'lookup' }] }],
    [...continuation.messages, continuation.messages.at(-1)],
  ])
    assert.throws(() => buildRequest({ ...options, messages }, limits, true), {
      code: 'UNSUPPORTED_OPTION',
    });
  assert.throws(
    () =>
      buildRequest(options, { ...limits, contextWindow: 100 }, true, () => 69),
    { code: 'CONTEXT_WINDOW_EXCEEDED' },
  );
});

test('request: actual OpenAPI schema accepts projection and rejects forbidden fields', () => {
  const spec = JSON.parse(
    readFileSync(
      new URL('../../../../contracts/openapi/ai.yaml', import.meta.url),
      'utf8',
    ),
  );
  const ajv = new Ajv({ strict: false, validateFormats: false });
  ajv.addSchema(spec, 'ai');
  const validate = ajv.compile({
    $ref: 'ai#/components/schemas/ChatCompletionRequest',
  });
  const body = buildRequest(
    {
      ...input(),
      temperature: 0.5,
      stop: ['end'],
      tools: [
        { name: 'lookup', description: 'Find', parameters: { type: 'object' } },
      ],
    },
    limits,
    true,
  );
  assert.ok(validate(body), JSON.stringify(validate.errors));
  for (const extra of [
    { n: 1 },
    { stream_options: {} },
    { max_completion_tokens: 8 },
    { reasoning_effort: 'high' },
    { store: true },
  ])
    assert.equal(validate({ ...body, ...extra }), false);
});

test('request: sampling, message/tool shape, missing limits, body size and exact context counts', () => {
  const bad = [
    { temperature: NaN },
    { temperature: 3 },
    { stop: [] },
    { stop: ['x'.repeat(129)] },
    { tools: [{ name: 'invalid.name', description: '', parameters: {} }] },
    {
      tools: [
        { name: 'lookup', description: '', parameters: {}, strict: true },
      ],
    },
    { messages: [{ role: 'user', content: null }] },
    { messages: [{ role: 'developer', content: [] }] },
    { messages: [{ role: 'tool', toolCallId: 'unknown', content: [] }] },
    {
      messages: [
        {
          role: 'user',
          content: [{ type: 'text', text: 'x' }],
          baseURL: 'https://example.com',
        },
      ],
    },
    {
      messages: Array.from({ length: 257 }, () => ({
        role: 'user',
        content: [],
      })),
    },
    {
      messages: [
        {
          role: 'user',
          content: [{ type: 'text', text: '你'.repeat(400000) }],
        },
      ],
    },
  ];
  for (const extra of bad)
    assert.throws(() => buildRequest({ ...input(), ...extra }, limits, false), {
      code: 'UNSUPPORTED_OPTION',
    });
  for (const key of ['tools', 'temperature', 'stop']) {
    const extras = { tools: [], temperature: 1, stop: ['end'] };
    assert.throws(
      () =>
        buildRequest(
          { ...input(), [key]: extras[key] },
          { maxOutputTokens: 128 },
          true,
        ),
      { code: 'UNSUPPORTED_OPTION' },
    );
  }
  assert.throws(
    () => buildRequest(input(), { ...limits, contextWindow: 100 }, false),
    { code: 'UNSUPPORTED_OPTION' },
  );
  assert.throws(
    () =>
      buildRequest(input(), { ...limits, contextWindow: 100 }, false, () => 69),
    { code: 'CONTEXT_WINDOW_EXCEEDED' },
  );
  assert.equal(
    buildRequest(input(), { ...limits, contextWindow: 100 }, false, () => 68)
      .max_tokens,
    32,
  );
  assert.throws(
    () => buildRequest({ ...input(), maxTokens: undefined }, limits, true),
    { code: 'UNSUPPORTED_OPTION' },
  );
  assert.equal(
    buildRequest(
      { ...input(), maxTokens: undefined },
      { ...limits, defaultMaxTokens: 16 },
      true,
    ).max_tokens,
    16,
  );
});
