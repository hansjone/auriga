// @ts-nocheck
import { fail } from './protocol.js';

/** P side of N's K capability ports. N owns metering, transport dedup and targets. */
export function createSharedDownstream({ runtime, ports, sdk }) {
  if (typeof sdk?.LlmAdapter !== 'function') fail('common.unavailable');
  let closed = false;
  const active = new Set();
  async function authorize(sessionId, scope, signal, nativeSignal, execution) {
    if (closed || signal?.aborted) fail();
    const binding = await runtime.session(sessionId);
    const audience =
      scope === 'model.invoke'
        ? 'auriga.shared.model'
        : 'auriga.shared.message';
    const task = await ports.taskForSession(binding, signal, {
      audience,
      scope,
      nativeSignal,
      execution,
    });
    try {
      if (!task?.taskId || (scope === 'model.invoke' && !task.callId)) fail();
      const capability = await ports.issue(
        binding,
        task.taskId,
        audience,
        [scope],
        signal,
      );
      const check = async () => {
        if (closed || signal?.aborted || task.signal?.aborted) fail();
        task.check?.();
        await runtime.capability(
          capability,
          sessionId,
          task.taskId,
          scope,
          audience,
        );
        if (closed || signal?.aborted || task.signal?.aborted) fail();
        task.check?.();
      };
      await check();
      return { binding, task, capability, check };
    } catch (error) {
      await task?.close?.();
      throw error;
    }
  }
  class SharedModelAdapter extends sdk.LlmAdapter {
    async *stream(options) {
      const controller = new AbortController();
      const abort = () => controller.abort();
      options.signal?.addEventListener('abort', abort, { once: true });
      if (options.signal?.aborted) abort();
      active.add(controller);
      let context;
      try {
        context = await authorize(
          options.sessionId,
          'model.invoke',
          controller.signal,
          options.signal,
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
          signal: globalThis.AbortSignal.any(
            [controller.signal, task.signal].filter(Boolean),
          ),
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
        await context?.task.close?.();
      }
    }
  }
  async function message(scope, sessionId, input, signal, execution) {
    const { binding, task, capability, check } = await authorize(
      sessionId,
      scope,
      signal,
      undefined,
      execution,
    );
    try {
      const request = {
        tenantId: binding.tenantId,
        sessionId: binding.sessionId,
        taskId: task.taskId,
        capability,
        messageId: input.messageId,
        target: input.target,
        body: input.body,
        operation: input.operation,
        frame: input.frame,
        signal: globalThis.AbortSignal.any(
          [signal, task.signal].filter(Boolean),
        ),
      };
      const result = await (scope === 'message.send'
        ? ports.send(request)
        : ports.receive(request));
      await check();
      return result;
    } finally {
      await task.close?.();
    }
  }
  return {
    adapter: new SharedModelAdapter(),
    send: (sessionId, input, signal, execution) =>
      message('message.send', sessionId, input, signal, execution),
    receive: (sessionId, input, signal, execution) =>
      message('message.receive', sessionId, input, signal, execution),
    close() {
      closed = true;
      for (const controller of active) controller.abort();
      active.clear();
    },
  };
}
