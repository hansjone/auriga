/** Optional host peer: resolved against its actual public root by public-api.test.js.
 * No bundled implementation or private upstream import is used at runtime. */
import type * as PublicLlm from '@deepseek-ai/dsh-llm';
export type HostApi = Pick<
  typeof PublicLlm,
  'LlmAdapter' | 'attributionHeaders' | 'resolveRetryPolicy' | 'ToolCallId'
>;
export type HostService = Pick<PublicLlm.LlmRuntime, 'registerAdapter'>;
export type GenerateOptions = PublicLlm.GenerateOptions;
export type StreamChunk = PublicLlm.StreamChunk;
export type ModelInfo = PublicLlm.LlmResolvedModelInfo;
export type Limits = {
  maxOutputTokens?: number;
  defaultMaxTokens?: number;
  contextWindow?: number;
  tools?: boolean;
  temperature?: boolean;
  stop?: boolean;
};
export interface AccessToken {
  token: string;
  /** Absolute Unix milliseconds, never a duration. */
  expiresAt: number;
  /** Aborted by logout, device/member revocation or broker invalidation. */
  signal: AbortSignal;
}
export interface AdapterOptions {
  hostApi: HostApi;
  /** Trusted deployment origin, with no path/query/userinfo/fragment. */
  apiBase: string;
  fetch: typeof fetch;
  /** In-memory broker; never receives model input and must not persist tokens. */
  getAccessToken(signal: AbortSignal): Promise<AccessToken | null>;
  /** Trusted exact-model facts; absence never creates capacity/capabilities. */
  modelLimits?: Record<string, Limits>;
  /** Exact token count from a trusted tokenizer, required for known contextWindow. */
  countInputTokens?(body: object): number;
  responseMode?: 'sse' | 'json';
}
