/** Generated from contracts/openapi/ai.yaml by test/ai/generate-contract.js. */
export type paths = Record<string, never>;
export type webhooks = Record<string, never>;
export interface components {
  schemas: {
    ChatCompletionRequest: {
      model: string;
      messages: components['schemas']['ChatMessage'][];
      /** @default false */
      stream: boolean;
      /** Format: int64 */
      max_tokens: number;
      temperature?: number;
      top_p?: number;
      /** @description 首批仅接受最多4个字符串的数组，不接受单字符串stop。 */
      stop?: string[];
      tools?: components['schemas']['Tool'][];
      /** @enum {string} */
      tool_choice?: 'none' | 'auto' | 'required';
    };
    ChatMessage: {
      /** @enum {string} */
      role: 'system' | 'user' | 'assistant' | 'tool';
      content: string | null;
      name?: string;
      tool_call_id?: string;
      tool_calls?: components['schemas']['ToolCall'][];
    };
    ToolCall: {
      id: string;
      /** @constant */
      type: 'function';
      function: {
        name: string;
        arguments: string;
      };
    };
    Tool: {
      /** @constant */
      type: 'function';
      function: {
        name: string;
        description?: string;
        parameters: {
          [key: string]: unknown;
        };
      };
    };
    ChatCompletion: {
      id: string;
      /** @constant */
      object: 'chat.completion';
      /** Format: int64 */
      created: number;
      model: string;
      choices: {
        /** Format: int64 */
        index: number;
        message: components['schemas']['AssistantMessage'];
        /** @enum {string} */
        finish_reason: 'stop' | 'length' | 'tool_calls' | 'content_filter';
      }[];
      usage: components['schemas']['TokenUsage'];
    };
    AssistantMessage: {
      /** @constant */
      role: 'assistant';
      content: string | null;
      tool_calls?: components['schemas']['ToolCall'][];
    };
    TokenUsage: {
      /** Format: int64 */
      prompt_tokens: number;
      /** Format: int64 */
      completion_tokens: number;
      /** Format: int64 */
      total_tokens: number;
    };
    ChatCompletionChunk: {
      id: string;
      /** @constant */
      object: 'chat.completion.chunk';
      /** Format: int64 */
      created: number;
      model: string;
      choices: {
        /** Format: int64 */
        index: number;
        delta: {
          /** @enum {string} */
          role?: 'assistant';
          content?: string | null;
          tool_calls?: components['schemas']['ToolCallDelta'][];
        };
        /** @enum {string|null} */
        finish_reason:
          'stop' | 'length' | 'tool_calls' | 'content_filter' | null;
      }[];
      usage?: components['schemas']['TokenUsage'];
    };
    ToolCallDelta: {
      /** Format: int64 */
      index: number;
      id?: string;
      /** @constant */
      type?: 'function';
      function?: {
        name?: string;
        arguments?: string;
      };
    };
    ModelList: {
      /** @constant */
      object: 'list';
      data: {
        id: string;
        /** @constant */
        object: 'model';
        /** Format: int64 */
        created: number;
        /** @constant */
        owned_by: 'auriga';
      }[];
    };
    AiError: {
      error: {
        code: string;
        message: string;
        /** @enum {string} */
        type:
          | 'authentication_error'
          | 'permission_error'
          | 'invalid_request_error'
          | 'rate_limit_error'
          | 'upstream_error'
          | 'conflict_error'
          | 'internal_error';
      };
      requestId: string;
      originalRequestId?: string;
      /** @enum {string} */
      outcome?: 'completed' | 'failed' | 'cancelled' | 'unknown';
    };
  };
  responses: never;
  parameters: never;
  requestBodies: never;
  headers: never;
  pathItems: never;
}
export type $defs = Record<string, never>;
export type operations = Record<string, never>;
