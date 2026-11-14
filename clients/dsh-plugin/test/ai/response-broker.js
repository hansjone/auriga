import { randomUUID } from 'node:crypto';

/** Parser regression peer behind C's fixed-operation boundary. Responses and
 * stalled/cancelled bodies are supplied by tests; no platform HTTP is performed.
 * The production broker envelope/schema tests live in broker-port.test.js.
 */
export function responseBroker(respond, { signal, expiresAt, before } = {}) {
  const streams = new Map(),
    listeners = new Set(),
    calls = [];
  let invalid = false;
  const expiry = () =>
    typeof expiresAt === 'function'
      ? expiresAt()
      : (expiresAt ?? Date.now() + 60000);
  const fail = () => {
    throw Object.assign(new Error('authority unavailable'), {
      code: 'common.unauthenticated',
    });
  };
  const invalidate = () => {
    invalid = true;
    for (const callback of listeners) callback();
  };
  const current = () => {
    if (invalid || signal?.aborted || expiry() <= Date.now()) return undefined;
    return {
      state: 'authenticated',
      expiresAt: new Date(expiry()).toISOString(),
    };
  };
  return {
    calls,
    state: {
      current,
      fence() {
        if (!current()) fail();
        return () => {
          if (!current()) fail();
        };
      },
      subscribeInvalidation(callback) {
        listeners.add(callback);
        if (listeners.size === 1)
          signal?.addEventListener('abort', invalidate, { once: true });
        const timer =
          expiresAt === undefined
            ? undefined
            : setTimeout(invalidate, expiry() - Date.now());
        timer?.unref();
        return () => {
          listeners.delete(callback);
          clearTimeout(timer);
          if (!listeners.size) signal?.removeEventListener('abort', invalidate);
        };
      },
    },
    async call(operation, input) {
      if (!current()) fail();
      await before?.();
      if (!current()) fail();
      if (
        ![
          'listAiModels',
          'createAiChatCompletion',
          'createAiChatCompletionStream',
        ].includes(operation)
      )
        throw Error('unexpected operation');
      calls.push({ operation, input });
      const response = await respond(operation, input);
      if (
        operation === 'createAiChatCompletionStream' ||
        !response.ok ||
        response.headers.get('content-type')?.split(';')[0] !==
          'application/json'
      ) {
        const streamId = randomUUID();
        streams.set(streamId, response.body);
        return {
          statusCode: response.status,
          headers: Object.fromEntries(response.headers),
          body: { streamId },
        };
      }
      return {
        statusCode: response.status,
        headers: Object.fromEntries(response.headers),
        body: await response.json(),
      };
    },
    openStream(id, active) {
      const stream = streams.get(id);
      streams.delete(id);
      // Models readJson/readSse own body cancellation. Mirror the transport's
      // signal for test assertions without adding a second reader.
      void active;
      return stream;
    },
    async streamCommand({ streamId, action }) {
      if (action === 'cancel') {
        await streams.get(streamId)?.cancel();
        streams.delete(streamId);
      }
    },
    invalidate,
  };
}
