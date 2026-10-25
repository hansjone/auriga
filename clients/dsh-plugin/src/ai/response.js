import { AdapterError, check, integer, only, text, toolName } from './guard.js';

/** @param {import('./types.js').HostApi} hostApi @param {string} model */
export function responseState(hostApi, model) {
  let nextIndex = 0;
  let textIndex = -1;
  let content = '';
  let finish = '';
  let identity = '';
  /** @type {{inputTokens: number, outputTokens: number, totalTokens: number} | undefined} */
  let usage;
  /** @type {Map<number, {index: number, id: string, name: string, arguments: string}>} */
  const tools = new Map();
  /** @param {unknown} raw @returns {import('./types.js').StreamChunk[]} */
  const accept = (raw) => {
    const event = only(raw, [
      'id',
      'object',
      'created',
      'model',
      'choices',
      'usage',
    ]);
    check(
      event.object === 'chat.completion.chunk' && event.model === model,
      'SERVER',
    );
    text(event.id, 1024, 1);
    integer(event.created);
    check(!identity || identity === event.id, 'SERVER');
    identity = event.id;
    check(Array.isArray(event.choices) && event.choices.length <= 1, 'SERVER');
    /** @type {import('./types.js').StreamChunk[]} */
    const chunks = [];
    if (event.choices.length) {
      const choice = only(event.choices[0], [
        'index',
        'delta',
        'finish_reason',
      ]);
      check(choice.index === 0 && !finish, 'SERVER');
      const delta = only(choice.delta, ['role', 'content', 'tool_calls']);
      check(delta.role === undefined || delta.role === 'assistant', 'SERVER');
      if (delta.content !== undefined && delta.content !== null) {
        text(delta.content, 1048576);
        if (delta.content) {
          if (textIndex < 0) {
            textIndex = nextIndex++;
            chunks.push({
              type: 'block-start',
              index: textIndex,
              blockType: 'text',
            });
          }
          content += delta.content;
          check(Buffer.byteLength(content) <= 4 * 1024 * 1024, 'SERVER');
          chunks.push({
            type: 'text-delta',
            index: textIndex,
            text: delta.content,
          });
        }
      }
      if (delta.tool_calls !== undefined) {
        check(
          Array.isArray(delta.tool_calls) && delta.tool_calls.length <= 128,
          'SERVER',
        );
        for (const rawCall of delta.tool_calls) {
          const call = only(rawCall, ['index', 'id', 'type', 'function']);
          integer(call.index);
          check(call.index < 128, 'SERVER');
          check(call.type === undefined || call.type === 'function', 'SERVER');
          let state = tools.get(call.index);
          if (!state) {
            text(call.id, 128, 1);
            check(![...tools.values()].some((t) => t.id === call.id), 'SERVER');
            state = {
              index: nextIndex++,
              id: call.id,
              name: '',
              arguments: '',
            };
            tools.set(call.index, state);
            chunks.push({
              type: 'block-start',
              index: state.index,
              blockType: 'tool-call',
            });
          } else check(call.id === undefined || call.id === state.id, 'SERVER');
          const fn =
            call.function === undefined
              ? {}
              : only(call.function, ['name', 'arguments']);
          if (fn.name !== undefined) {
            text(fn.name, 64);
            state.name += fn.name;
            check(state.name.length <= 64, 'SERVER');
          }
          if (fn.arguments !== undefined) {
            text(fn.arguments, 262144);
            state.arguments += fn.arguments;
            check(state.arguments.length <= 262144, 'SERVER');
          }
          chunks.push({
            type: 'tool-call-delta',
            index: state.index,
            id: hostApi.ToolCallId(state.id),
            // Public DSH name is a snapshot; only argumentsDelta is append-only.
            ...(fn.name === undefined ? {} : { name: state.name }),
            argumentsDelta: fn.arguments ?? '',
          });
        }
      }
      if (choice.finish_reason !== null) {
        check(
          ['stop', 'length', 'tool_calls', 'content_filter'].includes(
            choice.finish_reason,
          ),
          'SERVER',
        );
        finish = choice.finish_reason;
      }
    }
    if (event.usage !== undefined) {
      check(finish && !usage, 'SERVER');
      const value = only(event.usage, [
        'prompt_tokens',
        'completion_tokens',
        'total_tokens',
      ]);
      integer(value.prompt_tokens);
      integer(value.completion_tokens);
      integer(value.total_tokens);
      check(
        Number.isSafeInteger(value.prompt_tokens + value.completion_tokens) &&
          value.prompt_tokens + value.completion_tokens === value.total_tokens,
        'SERVER',
      );
      usage = {
        inputTokens: value.prompt_tokens,
        outputTokens: value.completion_tokens,
        totalTokens: value.total_tokens,
      };
    }
    return chunks;
  };
  return {
    accept,
    /** @param {unknown} raw */
    json(raw) {
      const result = only(raw, [
        'id',
        'object',
        'created',
        'model',
        'choices',
        'usage',
      ]);
      check(
        result.object === 'chat.completion' &&
          Array.isArray(result.choices) &&
          result.choices.length === 1 &&
          result.usage !== undefined,
        'SERVER',
      );
      const choice = only(result.choices[0], [
        'index',
        'message',
        'finish_reason',
      ]);
      const message = only(choice.message, ['role', 'content', 'tool_calls']);
      check(
        message.role === 'assistant' &&
          (message.content === null || typeof message.content === 'string'),
        'SERVER',
      );
      const delta = { ...message };
      if (message.tool_calls !== undefined) {
        check(
          Array.isArray(message.tool_calls) && message.tool_calls.length <= 128,
          'SERVER',
        );
        delta.tool_calls = message.tool_calls.map((rawCall, index) => {
          const call = only(rawCall, ['id', 'type', 'function']);
          check(call.type === 'function', 'SERVER');
          const fn = only(call.function, ['name', 'arguments']);
          toolName(fn.name);
          text(fn.arguments, 262144);
          return { index, ...call };
        });
      }
      return accept({
        ...result,
        object: 'chat.completion.chunk',
        choices: [
          { index: choice.index, delta, finish_reason: choice.finish_reason },
        ],
      });
    },
    /** Called only after a complete JSON or a terminated SSE [DONE] event.
     * @returns {import('./types.js').StreamChunk[]} */
    complete() {
      check(finish && finish !== 'content_filter', 'SERVER');
      check((finish === 'tool_calls') === tools.size > 0, 'SERVER');
      for (const tool of tools.values()) {
        toolName(tool.name);
        text(tool.arguments, 262144, 1);
        let args;
        try {
          args = JSON.parse(tool.arguments);
        } catch {
          throw new AdapterError('SERVER');
        }
        check(
          args !== null && typeof args === 'object' && !Array.isArray(args),
          'SERVER',
        );
      }
      /** @type {import('./types.js').StreamChunk[]} */
      const chunks = [];
      if (textIndex >= 0)
        chunks.push({
          type: 'block-end',
          index: textIndex,
          block: { type: 'text', text: content },
        });
      for (const tool of tools.values())
        chunks.push({
          type: 'block-end',
          index: tool.index,
          block: {
            type: 'tool-call',
            id: hostApi.ToolCallId(tool.id),
            name: tool.name,
            arguments: tool.arguments,
          },
        });
      if (usage) chunks.push({ type: 'usage', usage });
      chunks.push({
        type: 'finish',
        reason: {
          kind:
            finish === 'tool_calls'
              ? 'tool-calls'
              : finish === 'length'
                ? 'max-tokens'
                : 'stop',
        },
      });
      return chunks;
    },
  };
}
