import test from 'node:test';
import assert from 'node:assert/strict';
import { ReadableStream } from 'node:stream/web';
import { createAurigaAdapter } from '../../src/ai/index.js';
import { EVENT_LIMIT, TOTAL_LIMIT } from '../../src/ai/transport.js';
import { getEventListeners } from 'node:events';

// Transport tests only; real public API/registry/type checks live in public-api.test.js.
export const hostApi = {
  LlmAdapter: class {
    prepareCall() {}
    imageRequestPricing() {}
  },
  attributionHeaders: () => ({ 'user-agent': 'test-harness' }),
  resolveRetryPolicy: (policy) => policy,
  ToolCallId: (id) => id,
};
export const options = () => ({
  provider: 'auriga',
  model: 'team-model',
  maxTokens: 8,
  messages: [
    { role: 'user', content: [{ type: 'text', text: 'private prompt' }] },
  ],
});
export const packet = (delta = {}, finish_reason = null, usage) => ({
  id: 'test-response',
  object: 'chat.completion.chunk',
  model: 'team-model',
  created: 1,
  choices: [{ index: 0, delta, finish_reason }],
  ...(usage ? { usage } : {}),
});
export const sse = (events, size = 7) => {
  const bytes = Buffer.from(
    events
      .map(
        (value) =>
          `data: ${typeof value === 'string' ? value : JSON.stringify(value)}\r\n\r\n`,
      )
      .join(''),
  );
  return new Response(
    new ReadableStream({
      start(controller) {
        for (let i = 0; i < bytes.length; i += size)
          controller.enqueue(bytes.subarray(i, i + size));
        controller.close();
      },
    }),
    { headers: { 'content-type': 'text/event-stream' } },
  );
};
export const setup = (fetch, extra = {}) => {
  const revoked = new AbortController();
  return {
    revoked,
    adapter: createAurigaAdapter({
      hostApi,
      apiBase: 'https://gateway.example.com',
      fetch,
      getAccessToken: async () => ({
        token: 'test-only-token',
        expiresAt: Date.now() + 60000,
        signal: revoked.signal,
      }),
      modelLimits: { 'team-model': { maxOutputTokens: 32, tools: true } },
      ...extra,
    }),
  };
};
const collect = async (adapter, input = options()) =>
  Array.fromAsync(adapter.stream(input));

test('SSE: byte fragmentation UTF-8, usage, terminal ordering and transport whitelist', async () => {
  let posts = 0;
  const { adapter } = setup(async (url, init) => {
    posts++;
    assert.equal(url, 'https://gateway.example.com/ai/v1/chat/completions');
    assert.equal(init.credentials, 'omit');
    assert.equal(init.redirect, 'error');
    assert.equal(init.headers.authorization, 'Bearer test-only-token');
    assert.equal(JSON.parse(init.body).n, undefined);
    return sse(
      [
        packet({ content: '你好🌏' }),
        packet({}, 'stop', {
          prompt_tokens: 3,
          completion_tokens: 2,
          total_tokens: 5,
        }),
        '[DONE]',
      ],
      1,
    );
  });
  const chunks = await collect(adapter);
  assert.equal(chunks.find((c) => c.type === 'text-delta').text, '你好🌏');
  assert.equal(chunks.at(-2).type, 'usage');
  assert.deepEqual(chunks.at(-1), { type: 'finish', reason: { kind: 'stop' } });
  assert.equal(posts, 1);
});

test('SSE: missing DONE and in-stream AiError never report success', async () => {
  for (const events of [
    [packet({}, 'stop')],
    [
      packet({ content: 'partial' }),
      {
        error: {
          code: 'ai.stream_interrupted',
          message: 'private prompt test-only-token',
          type: 'gateway',
        },
        requestId: 'opaque',
      },
    ],
  ]) {
    const { adapter } = setup(async () => sse(events));
    const chunks = await collect(adapter);
    assert.equal(chunks.at(-1).reason.kind, 'error');
    assert.doesNotMatch(
      JSON.stringify(chunks.at(-1)),
      /private prompt|test-only-token/,
    );
  }
});

test('SSE: tools map name/arguments increments and close only complete calls', async () => {
  const { adapter } = setup(async () =>
    sse([
      packet({
        tool_calls: [
          {
            index: 0,
            id: 'call-1',
            type: 'function',
            function: { name: 'look', arguments: '{"q":' },
          },
        ],
      }),
      packet({
        tool_calls: [{ index: 0, function: { name: 'up', arguments: '1}' } }],
      }),
      packet({}, 'tool_calls'),
      '[DONE]',
    ]),
  );
  const chunks = await collect(adapter);
  assert.deepEqual(chunks.find((c) => c.type === 'block-end').block, {
    type: 'tool-call',
    id: 'call-1',
    name: 'lookup',
    arguments: '{"q":1}',
  });
  assert.deepEqual(
    chunks.filter((c) => c.type === 'tool-call-delta').map((c) => c.name),
    ['look', 'lookup'],
  );
  assert.equal(chunks.at(-1).reason.kind, 'tool-calls');
});

test('HTTP: 401, exceptions and transport failures are sanitized and never retried', async () => {
  for (const status of [401, 429, 503]) {
    let posts = 0;
    const { adapter } = setup(async () => {
      posts++;
      return new Response('private prompt test-only-token', { status });
    });
    const chunks = await collect(adapter);
    assert.equal(posts, 1);
    assert.equal(chunks.at(-1).reason.kind, 'error');
    assert.doesNotMatch(
      JSON.stringify(chunks),
      /private prompt|test-only-token/,
    );
    assert.equal(adapter.providerRetryPolicy('auriga').maxRetries, 0);
  }
});

test('JSON: visible text, tool calls, authoritative usage and length finish', async () => {
  const { adapter } = setup(
    async () =>
      new Response(
        JSON.stringify({
          id: 'response',
          object: 'chat.completion',
          model: 'team-model',
          created: 1,
          choices: [
            {
              index: 0,
              message: { role: 'assistant', content: 'result' },
              finish_reason: 'length',
            },
          ],
          usage: { prompt_tokens: 0, completion_tokens: 4, total_tokens: 4 },
        }),
        { headers: { 'content-type': 'application/json' } },
      ),
    { responseMode: 'json' },
  );
  const chunks = await collect(adapter);
  assert.deepEqual(chunks.at(-2), {
    type: 'usage',
    usage: { inputTokens: 0, outputTokens: 4, totalTokens: 4 },
  });
  assert.equal(chunks.at(-1).reason.kind, 'max-tokens');
});

test('catalog: only authorized ids, no invented capacities, costs or quotas', async () => {
  let gets = 0;
  const { adapter } = setup(async (url, init) => {
    gets++;
    assert.equal(url, 'https://gateway.example.com/ai/v1/models');
    assert.equal(init.method, 'GET');
    assert.equal(init.body, undefined);
    return new Response(
      JSON.stringify({
        object: 'list',
        data: [
          { id: 'team-model', object: 'model', created: 1, owned_by: 'auriga' },
        ],
      }),
      { headers: { 'content-type': 'application/json' } },
    );
  });
  assert.deepEqual(await adapter.listModels('auriga'), [
    { provider: 'auriga', id: 'team-model', name: 'team-model' },
  ]);
  assert.deepEqual(await adapter.resolveModel('auriga', 'unknown'), {
    provider: 'auriga',
    id: 'unknown',
    name: 'unknown',
  });
  assert.equal(gets, 1);
  assert.throws(() => adapter.providerInfo('other'), { code: 'NO_ADAPTER' });
  const aborted = new AbortController();
  aborted.abort();
  await assert.rejects(
    adapter.resolveModel('auriga', 'team-model', aborted.signal),
    { code: 'ABORTED' },
  );
});

test('no login, expired/revoked/malformed token and invalid input fail before POST', async () => {
  let posts = 0;
  const revoked = new AbortController();
  revoked.abort();
  for (const getAccessToken of [
    async () => null,
    async () => ({
      token: 'test',
      expiresAt: Date.now() - 1,
      signal: new AbortController().signal,
    }),
    async () => ({
      token: 'test',
      expiresAt: Date.now() + 10000,
      signal: revoked.signal,
    }),
    async () => ({
      token: 'test\nprivate',
      expiresAt: Date.now() + 10000,
      signal: revoked.signal,
    }),
  ]) {
    const { adapter } = setup(
      async () => {
        posts++;
        throw new Error('must not post');
      },
      { getAccessToken },
    );
    assert.equal((await collect(adapter)).at(-1).reason.kind, 'error');
    await assert.rejects(adapter.listModels('auriga'));
  }
  const { adapter } = setup(async () => {
    posts++;
    throw new Error('must not post');
  });
  assert.equal(
    (await collect(adapter, { ...options(), url: 'https://example.com' })).at(
      -1,
    ).reason.kind,
    'error',
  );
  assert.equal(posts, 0);
  assert.throws(() => createAurigaAdapter({}), { code: 'NO_ADAPTER' });
  for (const apiBase of [
    'https://example.com/ai/v1',
    'https://user:pass@example.com',
    'https://example.com/?x=1',
    'file:///etc/passwd',
  ])
    assert.throws(() => setup(async () => {}, { apiBase }), {
      code: 'NO_ADAPTER',
    });
});

test('cancel/revoke/dispose: stalled read is interrupted and body cancelled once', async () => {
  for (const kind of ['caller', 'revocation', 'dispose', 'consumer']) {
    let cancelled = 0,
      posts = 0,
      signal;
    const caller = new AbortController();
    const { adapter, revoked } = setup(async (_url, init) => {
      posts++;
      signal = init.signal;
      return new Response(
        new ReadableStream({
          start(controller) {
            controller.enqueue(
              Buffer.from(
                `data: ${JSON.stringify(packet({ content: 'partial' }))}\n\n`,
              ),
            );
          },
          cancel() {
            cancelled++;
          },
        }),
        { headers: { 'content-type': 'text/event-stream' } },
      );
    });
    const stream = adapter.stream({ ...options(), signal: caller.signal });
    assert.equal((await stream.next()).value.type, 'block-start');
    assert.equal((await stream.next()).value.type, 'text-delta');
    if (kind === 'consumer') {
      await stream.return();
    } else {
      const pending = stream.next();
      if (kind === 'caller') caller.abort();
      else if (kind === 'revocation') revoked.abort();
      else adapter.dispose();
      const terminal = (await pending).value;
      assert.equal(
        terminal.reason.kind,
        kind === 'revocation' ? 'error' : 'aborted',
      );
      assert.equal((await stream.next()).done, true);
    }
    assert.equal(signal.aborted, true);
    assert.equal(cancelled, 1);
    assert.equal(posts, 1);
  }
});

test('token expiry: cancels a paused stream using absolute expiry, never finishes successfully', async (t) => {
  t.mock.timers.enable({ apis: ['Date', 'setTimeout'], now: 100000 });
  let cancelled = 0;
  const { adapter } = setup(
    async () =>
      new Response(
        new ReadableStream({
          start(controller) {
            controller.enqueue(
              Buffer.from(
                `data: ${JSON.stringify(packet({ content: 'partial' }))}\n\n`,
              ),
            );
          },
          cancel() {
            cancelled++;
          },
        }),
        { headers: { 'content-type': 'text/event-stream' } },
      ),
    {
      getAccessToken: async () => ({
        token: 'test',
        expiresAt: Date.now() + 100,
        signal: new AbortController().signal,
      }),
    },
  );
  const stream = adapter.stream(options());
  await stream.next();
  t.mock.timers.tick(101);
  const terminal = (await stream.next()).value;
  assert.equal(terminal.reason.failure.code, 'AUTH');
  assert.equal(cancelled, 1);
  await stream.return();
});

test('cancel: broker/fetch that ignore signal settle promptly; late body is closed', async () => {
  for (const stage of ['broker', 'fetch']) {
    const caller = new AbortController();
    let entered, release;
    const started = new Promise((resolve) => {
      entered = resolve;
    });
    const stalled = new Promise((resolve) => {
      release = resolve;
    });
    let cancelled = 0;
    const { adapter } = setup(
      async () => {
        entered();
        return stalled;
      },
      stage === 'broker'
        ? {
            getAccessToken: async () => {
              entered();
              return stalled;
            },
          }
        : {},
    );
    const stream = adapter.stream({ ...options(), signal: caller.signal });
    const pending = stream.next();
    await started;
    caller.abort();
    assert.equal((await pending).value.reason.kind, 'aborted');
    await stream.next();
    if (stage === 'fetch') {
      release(
        new Response(
          new ReadableStream({
            cancel() {
              cancelled++;
            },
          }),
        ),
      );
      await Promise.resolve();
      await Promise.resolve();
      assert.equal(cancelled, 1);
    } else release(null);
  }
});

test('timeouts: first byte read closes without POST replay', async (t) => {
  t.mock.timers.enable({ apis: ['Date', 'setTimeout'], now: 100000 });
  let entered;
  const started = new Promise((resolve) => {
    entered = resolve;
  });
  const { adapter } = setup(async () => {
    entered();
    return new Response(new ReadableStream({}), {
      headers: { 'content-type': 'text/event-stream' },
    });
  });
  const stream = adapter.stream(options());
  const pending = stream.next();
  await started;
  // Advance microtasks until the read begins, without a wall-clock wait.
  for (let i = 0; i < 8; i++) await Promise.resolve();
  t.mock.timers.tick(30001);
  assert.equal((await pending).value.reason.failure.code, 'TIMEOUT');
  await stream.next();
});

test('SSE: malformed, extra choices, incomplete tools, inconsistent usage and provider exceptions fail closed', async () => {
  const invalid = [
    ['{bad json', '[DONE]'],
    [packet({ content: 'x' }), '[DONE]'],
    [
      {
        ...packet({}, 'stop'),
        choices: [packet().choices[0], packet().choices[0]],
      },
      '[DONE]',
    ],
    [
      packet(
        {
          tool_calls: [{ index: 0, function: { name: 'x', arguments: '{}' } }],
        },
        'tool_calls',
      ),
      '[DONE]',
    ],
    [
      packet(
        {
          tool_calls: [
            { index: 0, id: 'call', function: { name: 'x', arguments: '{' } },
          ],
        },
        'tool_calls',
      ),
      '[DONE]',
    ],
    [
      packet({ content: 'x' }, 'stop', {
        prompt_tokens: 1,
        completion_tokens: 1,
        total_tokens: 3,
      }),
      '[DONE]',
    ],
    [packet({ content: 'x', reasoning_content: 'hidden' }, 'stop'), '[DONE]'],
    [packet({}, 'content_filter'), '[DONE]'],
    [{ ...packet({}, 'stop'), model: 'wrong' }, '[DONE]'],
  ];
  for (const events of invalid) {
    const { adapter } = setup(async () => sse(events, 13));
    const chunks = await collect(adapter);
    assert.equal(chunks.at(-1).reason.kind, 'error');
    assert.equal(chunks.filter((chunk) => chunk.type === 'finish').length, 1);
  }
  const { adapter } = setup(async () => {
    throw new Error('test-only-token private prompt');
  });
  assert.doesNotMatch(
    JSON.stringify(await collect(adapter)),
    /test-only-token|private prompt/,
  );
});

test('SSE: invalid UTF-8, unterminated DONE, disconnect and response content-type cancel body', async () => {
  for (const body of [
    Buffer.from([100, 97, 116, 97, 58, 32, 0xff, 10, 10]),
    Buffer.from(`data: ${JSON.stringify(packet({}, 'stop'))}\n\ndata: [DONE]`),
    new ReadableStream({
      start(controller) {
        controller.error(new Error('private upstream'));
      },
    }),
  ]) {
    const { adapter } = setup(
      async () =>
        new Response(body, {
          headers: { 'content-type': 'text/event-stream' },
        }),
    );
    assert.equal((await collect(adapter)).at(-1).reason.kind, 'error');
  }
  let cancelled = 0;
  const { adapter } = setup(
    async () =>
      new Response(
        new ReadableStream({
          cancel() {
            cancelled++;
          },
        }),
        { headers: { 'content-type': 'text/html' } },
      ),
  );
  assert.equal((await collect(adapter)).at(-1).reason.kind, 'error');
  assert.equal(cancelled, 1);
});

test('SSE: event and total byte budgets are bounded; unknown usage is omitted', async () => {
  for (const raw of [
    `:${'x'.repeat(EVENT_LIMIT)}\n\n`,
    `:${'x'.repeat(EVENT_LIMIT - 4)}\r\n\r\n`,
    Array.from(
      { length: Math.ceil(TOTAL_LIMIT / 131072) + 1 },
      () => `:${'x'.repeat(131068)}\n\n`,
    ).join(''),
  ]) {
    const { adapter } = setup(
      async () =>
        new Response(
          Buffer.from(
            `${raw}data: ${JSON.stringify(packet({}, 'stop'))}\n\ndata: [DONE]\n\n`,
          ),
          {
            headers: { 'content-type': 'text/event-stream' },
          },
        ),
    );
    assert.equal((await collect(adapter)).at(-1).reason.kind, 'error');
  }
  const raw = `:${'x'.repeat(EVENT_LIMIT - 5)}\r\n\r\ndata: ${JSON.stringify(packet({ content: 'ok' }, 'stop'))}\r\n\r\ndata: [DONE]\r\n\r\n`;
  const { adapter } = setup(
    async () =>
      new Response(raw, { headers: { 'content-type': 'text/event-stream' } }),
  );
  const chunks = await collect(adapter);
  assert.equal(chunks.at(-1).reason.kind, 'stop');
  assert.equal(
    chunks.some((c) => c.type === 'usage'),
    false,
  );
});

test('SSE: CR-only, multiline data and LF boundary fragmentation', async () => {
  const raw = `data: ${JSON.stringify(packet({ content: '你好' }, 'stop')).replace(',"object"', ',\rdata: "object"')}\r\rdata: [DONE]\r\r`;
  const { adapter } = setup(
    async () =>
      new Response(raw, { headers: { 'content-type': 'text/event-stream' } }),
  );
  assert.equal((await collect(adapter)).at(-1).reason.kind, 'stop');
});

test('JSON: function tool result, malformed response and missing usage', async () => {
  const result = {
    id: 'response',
    object: 'chat.completion',
    model: 'team-model',
    created: 1,
    choices: [
      {
        index: 0,
        message: {
          role: 'assistant',
          content: null,
          tool_calls: [
            {
              id: 'call-1',
              type: 'function',
              function: { name: 'lookup', arguments: '{}' },
            },
          ],
        },
        finish_reason: 'tool_calls',
      },
    ],
    usage: { prompt_tokens: 3, completion_tokens: 1, total_tokens: 4 },
  };
  const { adapter } = setup(
    async () =>
      new Response(JSON.stringify(result), {
        headers: { 'content-type': 'application/json' },
      }),
    { responseMode: 'json' },
  );
  const chunks = await collect(adapter);
  assert.equal(chunks.at(-1).reason.kind, 'tool-calls');
  assert.equal(
    chunks.find((chunk) => chunk.type === 'block-end').block.name,
    'lookup',
  );
  for (const body of [
    { ...result, usage: undefined },
    { ...result, choices: [] },
    { ...result, unknown: true },
    {
      ...result,
      choices: [
        {
          ...result.choices[0],
          message: {
            ...result.choices[0].message,
            tool_calls: [
              {
                id: 'call-1',
                type: 'function',
                function: { name: 'lookup', arguments: '{' },
              },
            ],
          },
        },
      ],
    },
  ]) {
    const { adapter: invalid } = setup(
      async () =>
        new Response(JSON.stringify(body), {
          headers: { 'content-type': 'application/json' },
        }),
      { responseMode: 'json' },
    );
    assert.equal((await collect(invalid)).at(-1).reason.kind, 'error');
  }
});

test('failure/finish: close body and remove abort listeners before terminal is observed', async () => {
  for (const bad of [false, true]) {
    let cancelled = 0;
    const caller = new AbortController();
    const { adapter, revoked } = setup(
      async () =>
        new Response(
          new ReadableStream({
            start(controller) {
              controller.enqueue(
                Buffer.from(
                  bad
                    ? 'data: {broken}\n\n'
                    : `data: ${JSON.stringify(packet({}, 'stop'))}\n\ndata: [DONE]\n\n`,
                ),
              );
            },
            cancel() {
              cancelled++;
            },
          }),
          { headers: { 'content-type': 'text/event-stream' } },
        ),
    );
    const stream = adapter.stream({ ...options(), signal: caller.signal });
    const terminal = (await stream.next()).value;
    assert.equal(terminal.type, 'finish');
    assert.equal(terminal.reason.kind, bad ? 'error' : 'stop');
    assert.equal(cancelled, 1);
    assert.equal(getEventListeners(caller.signal, 'abort').length, 0);
    assert.equal(getEventListeners(revoked.signal, 'abort').length, 0);
    await stream.return();
  }
});

test('expiry: wall clock deadline is enforced when timer callback has not run', async (t) => {
  t.mock.timers.enable({ apis: ['Date', 'setTimeout'], now: 100000 });
  const { adapter } = setup(
    async () => sse([packet({ content: 'hello' }, 'stop'), '[DONE]']),
    {
      getAccessToken: async () => ({
        token: 'test',
        expiresAt: 100100,
        signal: new AbortController().signal,
      }),
    },
  );
  const stream = adapter.stream(options());
  await stream.next();
  t.mock.timers.setTime(100101);
  assert.equal((await stream.next()).value.reason.failure.code, 'AUTH');
  await stream.return();
});

test('byte budgets: fragmented oversized event and cumulative bounded events fail and cancel', async () => {
  for (const type of ['event', 'total']) {
    let cancelled = 0,
      generated = 0;
    const block =
      type === 'event'
        ? `:${'x'.repeat(131070)}`
        : `:${'x'.repeat(131068)}\n\n`;
    const { adapter } = setup(
      async () =>
        new Response(
          new ReadableStream({
            pull(controller) {
              generated++;
              controller.enqueue(Buffer.from(block));
            },
            cancel() {
              cancelled++;
            },
          }),
          { headers: { 'content-type': 'text/event-stream' } },
        ),
    );
    assert.equal((await collect(adapter)).at(-1).reason.kind, 'error');
    assert.equal(cancelled, 1);
    assert.ok(generated <= (type === 'event' ? 4 : 67));
  }
});

test('redirects and response URL changes fail closed with one fetch', async () => {
  for (const type of ['redirect', 'foreign']) {
    let calls = 0,
      cancelled = 0;
    const { adapter } = setup(async () => {
      calls++;
      const response = new Response(
        new ReadableStream({
          cancel() {
            cancelled++;
          },
        }),
        type === 'redirect'
          ? { status: 302, headers: { location: 'https://example.com/' } }
          : {},
      );
      if (type === 'foreign')
        Object.defineProperty(response, 'url', {
          value: 'https://example.com/ai/v1/chat/completions',
        });
      return response;
    });
    assert.equal((await collect(adapter)).at(-1).reason.kind, 'error');
    assert.equal(calls, 1);
    assert.equal(cancelled, 1);
  }
});

test('token rotation: each new request reads once; in-flight headers stay fixed', async () => {
  const headers = [];
  let reads = 0;
  const { adapter } = setup(
    async (_url, init) => {
      headers.push(init.headers.authorization);
      return sse([packet({}, 'stop'), '[DONE]']);
    },
    {
      getAccessToken: async () => ({
        token: `test-token-${++reads}`,
        expiresAt: Date.now() + 10000,
        signal: new AbortController().signal,
      }),
    },
  );
  for (let i = 0; i < 2; i++)
    assert.equal((await collect(adapter)).at(-1).reason.kind, 'stop');
  assert.deepEqual(headers, ['Bearer test-token-1', 'Bearer test-token-2']);
  assert.equal(reads, 2);
});

test('tool history: projected requests submit once while raw developer/content/call id violations submit zero', async () => {
  const bodies = [];
  const { adapter } = setup(async (_url, init) => {
    bodies.push(JSON.parse(init.body));
    return sse([packet({}, 'stop'), '[DONE]']);
  });
  const input = {
    ...options(),
    tools: [
      { name: 'lookup', description: 'Find', parameters: { type: 'object' } },
    ],
    toolHistory: {
      tools: [],
      updates: [{ messageId: 'added', additions: [] }],
    },
  };
  assert.equal((await collect(adapter, input)).at(-1).reason.kind, 'stop');
  assert.equal(bodies.length, 1);
  assert.equal('toolHistory' in bodies[0], false);
  for (const extra of [
    {
      messages: [
        {
          role: 'developer',
          content: [{ type: 'tool-addition', toolName: 'lookup' }],
        },
      ],
    },
    {
      messages: [
        { role: 'user', content: [{ type: 'image', attachment: {} }] },
      ],
    },
    {
      messages: [
        {
          role: 'user',
          content: [{ type: 'tool-removal', toolName: 'lookup' }],
        },
      ],
    },
    {
      messages: [
        {
          role: 'tool',
          toolCallId: 'missing',
          source: { kind: 'tool', callId: 'missing' },
          content: [{ type: 'text', text: 'found' }],
        },
      ],
    },
    { stream_options: {} },
    { tools: [{ ...input.tools[0], deferLoading: true }] },
  ]) {
    const terminal = (await collect(adapter, { ...input, ...extra })).at(-1);
    assert.equal(terminal.reason.failure.code, 'UNSUPPORTED_OPTION');
    assert.equal(
      bodies.length,
      1,
      'rejected direct inputs must not submit a POST',
    );
  }
});

test('timeouts: broker, fetch, idle and total deadlines interrupt without replay', async (t) => {
  t.mock.timers.enable({ apis: ['Date', 'setTimeout'], now: 100000 });
  for (const stage of ['broker', 'fetch', 'idle', 'total']) {
    let entered,
      posts = 0,
      cancelled = 0;
    const started = new Promise((resolve) => {
      entered = resolve;
    });
    const never = new Promise(() => {});
    const { adapter } = setup(
      async () => {
        posts++;
        entered();
        if (stage === 'fetch') return never;
        return new Response(
          new ReadableStream({
            start(controller) {
              controller.enqueue(
                Buffer.from(
                  `data: ${JSON.stringify(packet({ content: 'partial' }))}\n\n`,
                ),
              );
            },
            cancel() {
              cancelled++;
            },
          }),
          { headers: { 'content-type': 'text/event-stream' } },
        );
      },
      {
        getAccessToken: async () => {
          if (stage === 'broker') {
            entered();
            return never;
          }
          return {
            token: 'test-token',
            expiresAt: Date.now() + 600000,
            signal: new AbortController().signal,
          };
        },
      },
    );
    const stream = adapter.stream(options());
    let pending;
    if (stage === 'idle' || stage === 'total') {
      await stream.next();
      await stream.next();
      pending = stream.next();
    } else pending = stream.next();
    await started;
    for (let i = 0; i < 8; i++) await Promise.resolve();
    if (stage === 'total') t.mock.timers.setTime(Date.now() + 300001);
    t.mock.timers.tick(stage === 'total' ? 300001 : 30001);
    assert.equal((await pending).value.reason.failure.code, 'TIMEOUT');
    assert.equal(posts, stage === 'broker' ? 0 : 1);
    if (stage === 'idle' || stage === 'total') assert.equal(cancelled, 1);
    await stream.return();
  }
});
