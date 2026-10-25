import { check, integer, object, only, text, toolName } from './guard.js';

/** Strict public DSH -> D2 conversion. No request-controlled transport options.
 * @param {import('./types.js').GenerateOptions} input
 * @param {import('./types.js').Limits} limits
 * @param {boolean} stream
 * @param {((body: object) => number) | undefined} [countInputTokens]
 * @returns {import('./contract.js').components['schemas']['ChatCompletionRequest']} */
export function buildRequest(input, limits, stream, countInputTokens) {
  const options = only(input, [
    'provider',
    'model',
    'messages',
    'system',
    'tools',
    'temperature',
    'maxTokens',
    'stop',
    'signal',
    'sessionId',
    'purpose',
    'toolHistory',
  ]);
  check(options.provider === 'auriga');
  text(options.model, 128, 1);
  // History declarations are metadata only; dynamic declaration blocks are rejected below.
  if (options.toolHistory !== undefined) {
    only(options.toolHistory, ['tools', 'updates']);
    check(
      Array.isArray(options.toolHistory.tools) &&
        Array.isArray(options.toolHistory.updates),
    );
    check(options.toolHistory.updates.length === 0);
  }
  integer(limits.maxOutputTokens, 1);
  const max = options.maxTokens ?? limits.defaultMaxTokens;
  integer(max, 1);
  check(max <= limits.maxOutputTokens);
  check(
    Array.isArray(options.messages) &&
      options.messages.length > 0 &&
      options.messages.length <= 256,
  );
  /** @type {import('./contract.js').components['schemas']['ChatMessage'][]} */
  const messages = [];
  if (options.system !== undefined) {
    text(options.system, 1048576);
    messages.push({ role: 'system', content: options.system });
  }
  const calls = new Map();
  const pending = new Set();
  let contentBytes = 0;
  for (const raw of options.messages) {
    const msg = only(raw, [
      'id',
      'role',
      'source',
      'content',
      'toolCallId',
      'isError',
    ]);
    check(['system', 'user', 'assistant', 'tool'].includes(msg.role));
    check(pending.size === 0 || msg.role === 'tool');
    check(Array.isArray(msg.content));
    let content = '';
    /** @type {import('./contract.js').components['schemas']['ToolCall'][]} */
    const toolCalls = [];
    for (const rawBlock of msg.content) {
      const block = object(rawBlock);
      if (block.type === 'text') {
        only(block, ['type', 'text']);
        text(block.text, 1048576);
        contentBytes += Buffer.byteLength(block.text);
        check(contentBytes <= 1048576);
        content += block.text;
      } else {
        only(block, ['type', 'id', 'name', 'arguments']);
        check(
          block.type === 'tool-call' &&
            msg.role === 'assistant' &&
            limits.tools === true,
        );
        text(block.id, 128, 1);
        toolName(block.name);
        text(block.arguments, 262144);
        contentBytes += Buffer.byteLength(block.arguments);
        check(contentBytes <= 1048576);
        check(!calls.has(block.id));
        calls.set(block.id, block.name);
        pending.add(block.id);
        toolCalls.push({
          id: block.id,
          type: 'function',
          function: { name: block.name, arguments: block.arguments },
        });
      }
    }
    text(content, 1048576);
    check(toolCalls.length <= 128);
    if (msg.role === 'tool') {
      text(msg.toolCallId, 128, 1);
      check(pending.delete(msg.toolCallId));
      check(
        msg.source?.kind === 'tool' && msg.source.callId === msg.toolCallId,
      );
      check(msg.isError === undefined || typeof msg.isError === 'boolean');
      messages.push({ role: 'tool', content, tool_call_id: msg.toolCallId });
    } else {
      check(msg.toolCallId === undefined && msg.isError === undefined);
      messages.push({
        role: msg.role,
        content: !content && toolCalls.length ? null : content,
        ...(toolCalls.length ? { tool_calls: toolCalls } : {}),
      });
    }
  }
  check(messages.length <= 256 && pending.size === 0);
  /** @type {import('./contract.js').components['schemas']['ChatCompletionRequest']} */
  const body = { model: options.model, messages, stream, max_tokens: max };
  if (options.tools !== undefined) {
    check(
      limits.tools === true &&
        Array.isArray(options.tools) &&
        options.tools.length <= 128,
    );
    const names = new Set();
    body.tools = options.tools.map((rawTool) => {
      const tool = only(rawTool, ['name', 'description', 'parameters']);
      toolName(tool.name);
      text(tool.description, 8192);
      object(tool.parameters);
      check(!names.has(tool.name));
      names.add(tool.name);
      return {
        type: 'function',
        function: {
          name: tool.name,
          description: tool.description,
          parameters: tool.parameters,
        },
      };
    });
  }
  if (options.temperature !== undefined) {
    check(
      limits.temperature === true &&
        Number.isFinite(options.temperature) &&
        options.temperature >= 0 &&
        options.temperature <= 2,
    );
    body.temperature = options.temperature;
  }
  if (options.stop !== undefined) {
    check(
      limits.stop === true &&
        Array.isArray(options.stop) &&
        options.stop.length >= 1 &&
        options.stop.length <= 4,
    );
    for (const stop of options.stop) text(stop, 128);
    body.stop = options.stop;
  }
  check(Buffer.byteLength(JSON.stringify(body)) <= 1048576);
  if (limits.contextWindow !== undefined) {
    integer(limits.contextWindow, 1);
    check(typeof countInputTokens === 'function');
    const tokens = countInputTokens(JSON.parse(JSON.stringify(body)));
    integer(tokens);
    check(tokens <= limits.contextWindow - max, 'CONTEXT_WINDOW_EXCEEDED');
  }
  return body;
}
