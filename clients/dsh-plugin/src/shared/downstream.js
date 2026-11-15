// @ts-nocheck
import { LlmAdapter } from '@deepseek-ai/dsh-llm';
import { fail } from './protocol.js';

/** P side of N's K capability ports. N owns metering, transport dedup and targets. */
export function createSharedDownstream({ runtime, ports }) {
  let closed = false;
  const active = new Set();
  async function authorize(sessionId, scope, signal) {
    if (closed || signal?.aborted) fail();
    const binding = await runtime.session(sessionId);
    const task = await ports.taskForSession(binding, signal);
    if (!task?.taskId || (scope === 'model.invoke' && !task.callId)) fail();
    const audience =
      scope === 'model.invoke'
        ? 'auriga.shared.model'
        : 'auriga.shared.message';
    const capability = await ports.issue(
      binding,
      task.taskId,
      audience,
      [scope],
      signal,
    );
    const check = async () => {
      if (closed || signal?.aborted) fail();
      await runtime.capability(
        capability,
        sessionId,
        task.taskId,
        scope,
        audience,
      );
    };
    await check();
    return { binding, task, capability, check };
  }
  class SharedModelAdapter extends LlmAdapter {
    async *stream(options) {
      const controller = new AbortController();
      const abort = () => controller.abort();
      options.signal?.addEventListener('abort', abort, { once: true });
      if (options.signal?.aborted) abort();
      active.add(controller);
      try {
        const context = await authorize(
          options.sessionId,
          'model.invoke',
          controller.signal,
        );
        const { binding, task, capability, check } = context;
        // Route identity is supplied separately and cannot be overridden by model input.
        const body = {
          model: options.model,
          messages: options.messages,
          tools: options.tools,
          temperature: options.temperature,
          maxTokens: options.maxTokens,
        };
        const request = {
          tenantId: binding.tenantId,
          sessionId: binding.sessionId,
          taskId: task.taskId,
          callId: task.callId,
          capability,
          body,
          signal: controller.signal,
        };
        for await (const chunk of ports.model(request)) {
          await check();
          yield chunk;
        }
        await check();
      } finally {
        controller.abort();
        active.delete(controller);
        options.signal?.removeEventListener('abort', abort);
      }
    }
  }
  async function message(scope, sessionId, input, signal) {
    const { binding, task, capability, check } = await authorize(
      sessionId,
      scope,
      signal,
    );
    const request = {
      tenantId: binding.tenantId,
      sessionId: binding.sessionId,
      taskId: task.taskId,
      capability,
      messageId: input.messageId,
      target: input.target,
      body: input.body,
      signal,
    };
    const result = await (scope === 'message.send'
      ? ports.send(request)
      : ports.receive(request));
    await check();
    return result;
  }
  return {
    adapter: new SharedModelAdapter(),
    send: (sessionId, input, signal) =>
      message('message.send', sessionId, input, signal),
    receive: (sessionId, input, signal) =>
      message('message.receive', sessionId, input, signal),
    close() {
      closed = true;
      for (const controller of active) controller.abort();
      active.clear();
    },
  };
}
