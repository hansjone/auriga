import test from 'node:test';
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { dirname, resolve } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import ts from 'typescript';
import { registerAurigaAdapter } from '../../src/ai/index.js';
import { responseState } from '../../src/ai/response.js';
import { readFileSync } from 'node:fs';
import Ajv from 'ajv';

// Optional installed-host probe: only root package exports, never src/lib paths.
// Explicit public package.json locator also supports a read-only baseline checkout.
const manifest = process.env.AURIGA_DSH_LLM_PACKAGE;
const require = createRequire(manifest ? resolve(manifest) : import.meta.url);
let root;
let peerRequire;
try {
  root = require.resolve('@deepseek-ai/dsh-llm');
  peerRequire = createRequire(root);
} catch (error) {
  if (!['MODULE_NOT_FOUND', 'ERR_MODULE_NOT_FOUND'].includes(error.code))
    throw error;
}

test(
  'public DSH API: actual LlmAdapter type compatibility',
  {
    skip: root
      ? false
      : 'No readable public host package; transport mocks do not prove compatibility',
  },
  () => {
    const fixture = fileURLToPath(
      new URL('./public-types.ts', import.meta.url),
    );
    const options = {
      strict: true,
      allowJs: true,
      checkJs: true,
      noEmit: true,
      skipLibCheck: true,
      target: ts.ScriptTarget.ES2022,
      module: ts.ModuleKind.ESNext,
      moduleResolution: ts.ModuleResolutionKind.Bundler,
      types: ['node'],
    };
    const host = ts.createCompilerHost(options);
    const publicContainingFile = manifest
      ? resolve(dirname(manifest), 'public-probe.ts')
      : fixture;
    host.resolveModuleNames = (names, containingFile) =>
      names.map(
        (name) =>
          ts.resolveModuleName(
            name,
            name === '@deepseek-ai/dsh-llm'
              ? publicContainingFile
              : containingFile,
            options,
            ts.sys,
          ).resolvedModule,
      );
    const program = ts.createProgram([fixture], options, host);
    const errors = ts
      .getPreEmitDiagnostics(program)
      .filter(
        (diagnostic) => diagnostic.category === ts.DiagnosticCategory.Error,
      );
    assert.equal(
      errors.length,
      0,
      ts.formatDiagnosticsWithColorAndContext(errors, {
        getCurrentDirectory: () => process.cwd(),
        getCanonicalFileName: (file) => file,
        getNewLine: () => '\n',
      }),
    );
  },
);

test(
  'public DSH API: actual BlockAssembler consumes tool name snapshots and argument increments',
  { skip: root ? false : 'No readable public host package' },
  async () => {
    const hostApi = await import(pathToFileURL(root));
    const state = responseState(hostApi, 'team-model');
    const assembler = new hostApi.BlockAssembler();
    for (const [name, args, id] of [
      ['look', '{"q":', 'call-1'],
      ['up', '1}', undefined],
    ]) {
      for (const chunk of state.accept({
        id: 'response',
        object: 'chat.completion.chunk',
        model: 'team-model',
        created: 1,
        choices: [
          {
            index: 0,
            delta: {
              tool_calls: [
                {
                  index: 0,
                  ...(id ? { id } : {}),
                  function: { name, arguments: args },
                },
              ],
            },
            finish_reason: null,
          },
        ],
      }))
        assembler.push(chunk);
    }
    assert.equal(assembler.blocks()[0].name, 'lookup');
    assert.equal(assembler.blocks()[0].arguments, '{"q":1}');
    for (const chunk of state.accept({
      id: 'response',
      object: 'chat.completion.chunk',
      model: 'team-model',
      created: 1,
      choices: [{ index: 0, delta: {}, finish_reason: 'tool_calls' }],
    }))
      assembler.push(chunk);
    for (const chunk of state.complete()) assembler.push(chunk);
    assert.equal(assembler.finish.kind, 'tool-calls');
    assert.deepEqual(assembler.blocks(), [
      {
        type: 'tool-call',
        id: hostApi.ToolCallId('call-1'),
        name: 'lookup',
        arguments: '{"q":1}',
      },
    ]);
  },
);

test(
  'public DSH API: real runtime register, stream, duplicate refusal, unregister',
  { skip: root ? false : 'No readable public host package' },
  async () => {
    const hostApi = await import(pathToFileURL(root));
    const { Context } = await import(
      pathToFileURL(peerRequire.resolve('@deepseek-ai/cordis'))
    );
    const ctx = new Context();
    const fiber = await ctx.plugin(hostApi.LlmRuntime);
    const options = {
      hostApi,
      apiBase: 'https://gateway.example.com',
      responseMode: 'json',
      modelLimits: { 'team-model': { maxOutputTokens: 32 } },
      getAccessToken: async () => ({
        token: 'test-token',
        expiresAt: Date.now() + 60000,
        signal: new AbortController().signal,
      }),
      fetch: async () =>
        new Response(
          JSON.stringify({
            id: 'response',
            object: 'chat.completion',
            model: 'team-model',
            created: 1,
            choices: [
              {
                index: 0,
                message: { role: 'assistant', content: '你好' },
                finish_reason: 'stop',
              },
            ],
            usage: { prompt_tokens: 1, completion_tokens: 2, total_tokens: 3 },
          }),
          { headers: { 'content-type': 'application/json' } },
        ),
    };
    let registration;
    try {
      registration = registerAurigaAdapter(ctx.llm, options);
      assert.ok(registration.adapter instanceof hostApi.LlmAdapter);
      assert.deepEqual(ctx.llm.listProviders(), [
        { id: 'auriga', name: 'Auriga' },
      ]);
      assert.throws(() => registerAurigaAdapter(ctx.llm, options), {
        code: 'NO_ADAPTER',
      });
      const chunks = [];
      for await (const chunk of ctx.llm.stream({
        provider: 'auriga',
        model: 'team-model',
        maxTokens: 8,
        messages: [
          hostApi.createUserMessage({
            source: { kind: 'user' },
            content: [{ type: 'text', text: 'hello' }],
          }),
        ],
      }))
        chunks.push(chunk);
      assert.equal(chunks.at(-1).reason.kind, 'stop');
      assert.deepEqual(
        await registration.adapter.resolveModel('auriga', 'unknown'),
        { provider: 'auriga', id: 'unknown', name: 'unknown' },
      );
      registration.dispose();
      registration.dispose();
      assert.deepEqual(ctx.llm.listProviders(), []);
    } finally {
      registration?.dispose();
      await fiber.dispose();
    }
  },
);

test(
  'public DSH API: real Runtime projects tool additions/removals and preserves usable continuations',
  { skip: root ? false : 'No readable public host package' },
  async (t) => {
    const hostApi = await import(pathToFileURL(root));
    if (typeof hostApi.createDeveloperMessage !== 'function') {
      t.skip(
        'Selected public build lacks createDeveloperMessage; run with the installed host package, not the older source-tree lib',
      );
      return;
    }
    const { Context } = await import(
      pathToFileURL(peerRequire.resolve('@deepseek-ai/cordis'))
    );
    const ctx = new Context();
    const fiber = await ctx.plugin(hostApi.LlmRuntime);
    const search = {
      name: 'search',
      description: 'Search',
      parameters: { type: 'object' },
    };
    const lookup = {
      name: 'lookup',
      description: 'Lookup',
      parameters: { type: 'object' },
    };
    const user = hostApi.createUserMessage({
      source: { kind: 'user' },
      content: [{ type: 'text', text: 'hello' }],
    });
    const added = hostApi.createDeveloperMessage({
      source: { kind: 'user' },
      content: [{ type: 'tool-addition', toolName: 'lookup' }],
    });
    const removed = hostApi.createDeveloperMessage({
      source: { kind: 'user' },
      content: [{ type: 'tool-removal', toolName: 'search' }],
    });
    const initialHistory = { tools: [search], updates: [] };
    const addedHistory = {
      tools: [search],
      updates: [{ messageId: added.id, additions: [lookup] }],
    };
    const fullHistory = {
      tools: [search],
      updates: [
        ...addedHistory.updates,
        { messageId: removed.id, additions: [] },
      ],
    };
    const ajv = new Ajv({ strict: false, validateFormats: false });
    const spec = JSON.parse(
      readFileSync(
        new URL('../../../../contracts/openapi/ai.yaml', import.meta.url),
        'utf8',
      ),
    );
    ajv.addSchema(spec, 'ai');
    const valid = ajv.compile({
      $ref: 'ai#/components/schemas/ChatCompletionRequest',
    });
    const bodies = [],
      measured = [],
      observed = [];
    let inputTokens = 3;
    let registration;
    const consume = async (stream) => {
      const chunks = [];
      for await (const chunk of stream) chunks.push(chunk);
      return chunks;
    };
    try {
      registration = registerAurigaAdapter(ctx.llm, {
        hostApi,
        apiBase: 'https://gateway.example.com',
        responseMode: 'json',
        modelLimits: {
          'team-model': {
            maxOutputTokens: 32,
            contextWindow: 100,
            tools: true,
          },
        },
        countInputTokens(body) {
          measured.push(JSON.parse(JSON.stringify(body)));
          return inputTokens;
        },
        getAccessToken: async () => ({
          token: 'test-only-token',
          expiresAt: Date.now() + 60000,
          signal: new AbortController().signal,
        }),
        fetch: async (_url, init) => {
          const body = JSON.parse(init.body);
          bodies.push(body);
          assert.ok(valid(body), JSON.stringify(valid.errors));
          assert.equal('toolHistory' in body, false);
          assert.ok(
            body.messages.every((message) => message.role !== 'developer'),
          );
          assert.ok(
            body.tools.every((tool) => !('deferLoading' in tool.function)),
          );
          return new Response(
            JSON.stringify({
              id: 'response',
              object: 'chat.completion',
              model: 'team-model',
              created: 1,
              choices: [
                {
                  index: 0,
                  message: { role: 'assistant', content: 'ok' },
                  finish_reason: 'stop',
                },
              ],
              usage: {
                prompt_tokens: 3,
                completion_tokens: 2,
                total_tokens: 5,
              },
            }),
            { headers: { 'content-type': 'application/json' } },
          );
        },
      });
      const adapterStream = registration.adapter.stream.bind(
        registration.adapter,
      );
      // Observe our own public adapter boundary, without altering host projection.
      registration.adapter.stream = (options) => {
        observed.push({
          roles: options.messages.map((message) => message.role),
          tools: options.tools,
          updates: options.toolHistory?.updates.length,
        });
        return adapterStream(options);
      };
      const base = { provider: 'auriga', model: 'team-model', maxTokens: 8 };
      const continuing = hostApi.createUserMessage({
        source: { kind: 'user' },
        content: [{ type: 'text', text: 'continue' }],
      });
      const callId = hostApi.ToolCallId('call-1');
      const assistant = hostApi.createAssistantMessage({
        source: { provider: 'auriga', model: 'team-model' },
        content: [
          { type: 'tool-call', id: callId, name: 'lookup', arguments: '{}' },
        ],
      });
      const result = hostApi.createToolResultMessage({
        callId,
        content: [{ type: 'text', text: 'found' }],
        isError: false,
      });
      const cases = [
        { messages: [user], tools: [search], toolHistory: initialHistory },
        {
          messages: [user, added],
          tools: [search, { ...lookup, deferLoading: true }],
          toolHistory: addedHistory,
        },
        {
          messages: [user, added, removed],
          tools: [lookup],
          toolHistory: fullHistory,
        },
        {
          messages: [user, added, removed, continuing],
          tools: [lookup],
          toolHistory: fullHistory,
        },
        {
          messages: [user, added, removed, assistant, result],
          tools: [lookup],
          toolHistory: fullHistory,
        },
        { messages: [continuing], tools: [lookup], toolHistory: fullHistory },
      ];
      for (const [index, options] of cases.entries()) {
        const chunks = await consume(ctx.llm.stream({ ...base, ...options }));
        assert.equal(chunks.at(-1).reason.kind, 'stop');
        assert.deepEqual(chunks.find((chunk) => chunk.type === 'usage').usage, {
          inputTokens: 3,
          outputTokens: 2,
          totalTokens: 5,
        });
        assert.equal(
          bodies.length,
          index + 1,
          'each successful turn submits one POST',
        );
        assert.deepEqual(measured[index], bodies[index]);
        assert.deepEqual(
          bodies[index].tools.map((tool) => tool.function.name),
          index === 0
            ? ['search']
            : index === 1
              ? ['search', 'lookup']
              : ['lookup'],
        );
        assert.equal(bodies[index].max_tokens, 8);
      }
      assert.deepEqual(
        observed.map((options) => options.updates),
        [0, 1, 2, 2, 2, 2],
      );
      assert.ok(
        observed.every((options) => !options.roles.includes('developer')),
      );
      assert.ok(
        observed.every((options) =>
          options.tools.every((tool) => !('deferLoading' in tool)),
        ),
      );
      assert.equal(bodies[4].messages.at(-1).tool_call_id, 'call-1');
      const count = bodies.length;
      for (const input of [
        {
          ...base,
          messages: [user, added],
          tools: [lookup],
          toolHistory: addedHistory,
        },
        {
          ...base,
          messages: [{ ...user, content: [{ type: 'image', attachment: {} }] }],
          tools: [lookup],
          toolHistory: fullHistory,
        },
        {
          ...base,
          messages: [
            {
              ...user,
              content: [{ type: 'tool-addition', toolName: 'lookup' }],
            },
          ],
          tools: [lookup],
          toolHistory: fullHistory,
        },
        {
          ...base,
          messages: [{ ...result, toolCallId: hostApi.ToolCallId('missing') }],
          tools: [lookup],
          toolHistory: fullHistory,
        },
        { ...base, ...cases[3], stream_options: {} },
      ]) {
        const chunks = await consume(registration.adapter.stream(input));
        assert.equal(chunks.at(-1).reason.failure.code, 'UNSUPPORTED_OPTION');
        assert.equal(bodies.length, count);
      }
      inputTokens = 93;
      const rejected = await consume(ctx.llm.stream({ ...base, ...cases[3] }));
      assert.equal(
        rejected.at(-1).reason.failure.code,
        'CONTEXT_WINDOW_EXCEEDED',
      );
      assert.equal(bodies.length, count);
    } finally {
      registration?.dispose();
      await fiber.dispose();
    }
  },
);
