import test from 'node:test';
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { dirname, resolve } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import ts from 'typescript';
import { registerAurigaAdapter } from '../../src/ai/index.js';
import { responseState } from '../../src/ai/response.js';

// Optional installed-host probe: only root package exports, never src/lib paths.
// Explicit public package.json locator also supports a read-only baseline checkout.
const manifest = process.env.AURIGA_DSH_LLM_PACKAGE;
const require = createRequire(manifest ? resolve(manifest) : import.meta.url);
let root;
try {
  root = require.resolve('@deepseek-ai/dsh-llm');
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
      pathToFileURL(require.resolve('@deepseek-ai/cordis'))
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
