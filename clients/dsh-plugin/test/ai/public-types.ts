import * as hostApi from '@deepseek-ai/dsh-llm';
import type {
  GenerateOptions,
  StreamChunk,
  LlmAdapter,
} from '@deepseek-ai/dsh-llm';
import {
  createAurigaAdapter,
  registerAurigaAdapter,
} from '../../src/ai/index.js';

const options = {
  hostApi,
  apiBase: 'https://gateway.example.com',
  fetch,
  getAccessToken: async (_signal: AbortSignal) => null,
};
const adapter: LlmAdapter = createAurigaAdapter(options);
const input: GenerateOptions = {
  provider: 'auriga',
  model: 'team-model',
  maxTokens: 8,
  messages: [
    hostApi.createUserMessage({
      source: { kind: 'user' },
      content: [{ type: 'text', text: 'hello' }],
    }),
  ],
};
const stream: AsyncIterable<StreamChunk> = adapter.stream(input);
const register = (runtime: hostApi.LlmRuntime) =>
  registerAurigaAdapter(runtime, options);
void stream;
void register;
