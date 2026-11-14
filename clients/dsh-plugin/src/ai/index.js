import { BRAND_NAME } from '../brand.js';
import { modelDirectory } from './catalog.js';

import { AdapterError, check, integer, text } from './guard.js';
import { buildRequest } from './request.js';
import { responseState } from './response.js';
import { operation, readJson, readSse } from './transport.js';

/** Creates an isolated adapter using only the module loaded from the public
 * @deepseek-ai/dsh-llm root. No host mutation until registerAurigaAdapter.
 * @param {import('./types.js').AdapterOptions} options */
export function createAurigaAdapter(options) {
  const { hostApi, broker } = options;
  check(
    hostApi &&
      typeof hostApi.LlmAdapter === 'function' &&
      typeof hostApi.LlmAdapter.prototype?.prepareCall === 'function' &&
      typeof hostApi.LlmAdapter.prototype?.imageRequestPricing === 'function' &&
      typeof hostApi.attributionHeaders === 'function' &&
      typeof hostApi.resolveRetryPolicy === 'function' &&
      typeof hostApi.ToolCallId === 'function',
    'NO_ADAPTER',
  );
  check(broker && typeof broker.call === 'function', 'NO_ADAPTER');
  let base;
  try {
    base = new URL(options.apiBase);
  } catch {
    throw new AdapterError('NO_ADAPTER');
  }
  check(
    ['http:', 'https:'].includes(base.protocol) &&
      base.pathname === '/' &&
      !base.search &&
      !base.hash &&
      !base.username &&
      !base.password,
    'NO_ADAPTER',
  );
  // Snapshot trusted options; later caller mutations cannot retarget a request.

  const limits = JSON.parse(JSON.stringify(options.modelLimits ?? {}));
  const mode = options.responseMode ?? 'sse';
  check(mode === 'sse' || mode === 'json', 'NO_ADAPTER');
  const countInputTokens = options.countInputTokens;
  const retryPolicy = hostApi.resolveRetryPolicy(
    { mode: 'normal', maxRetries: 0 },
    'auriga.retryPolicy',
  );
  const active = new Set();
  const lifetime = new AbortController();
  let disposed = false;
  /** @type {{ids: string[], expiresAt: number, signal: AbortSignal} | undefined} */
  let catalog;
  let catalogEpoch = 0;
  const unsubscribe = broker.state.subscribeInvalidation?.(() => {
    catalog = undefined;
    lifetime.abort();
  });
  /** Capacity and tokenizer are installation-owned ports, never directory facts.
   * @param {string} model */
  const generationReady = (model) => {
    const facts = limits[model];
    return (
      typeof countInputTokens === 'function' &&
      Number.isSafeInteger(facts?.contextWindow) &&
      Number.isSafeInteger(facts?.maxOutputTokens) &&
      facts.maxOutputTokens > 0 &&
      facts.contextWindow > facts.maxOutputTokens
    );
  };
  /** @param {string} provider */
  const route = (provider) => {
    check(provider === 'auriga' && !disposed, 'NO_ADAPTER');
  };

  /** @param {AbortSignal | undefined} caller */
  const begin = (caller) => {
    check(!disposed, 'NO_ADAPTER');
    const op = operation(caller);
    let fence;
    try {
      fence = broker.state.fence();
    } catch {
      op.close();
      throw new AdapterError('AUTH');
    }
    const verify = () => {
      op.assert();
      try {
        fence();
      } catch {
        op.abort('AUTH');
        op.assert();
      }
    };
    op.watch(lifetime.signal, 'AUTH');
    active.add(op);
    return Object.assign(op, { verify });
  };
  /** @param {ReturnType<typeof operation>} op @param {'models' | 'chat/completions'} path @param {string | undefined} body */
  const send = async (op, path, body) => {
    op.assert();
    const fence = broker.state.fence();
    const snapshot = broker.state.current();
    const expiresAt = Date.parse(snapshot?.expiresAt);
    check(
      snapshot?.state === 'authenticated' && expiresAt > Date.now(),
      'AUTH',
    );
    // Snapshot expiry is a renewable freshness lease, not a fixed request TTL.
    // Its invalidation subscription interrupts active calls/streams; an ordinary
    // same-generation refresh must not truncate a longer generation at 30s.
    const receive = async (/** @type {Promise<any>} */ pending) => {
      const value = await pending;
      if (op.signal.aborted && value.body?.streamId)
        void broker
          .streamCommand({ streamId: value.body.streamId, action: 'cancel' })
          .catch(() => {});
      return value;
    };
    let output;
    try {
      if (path === 'models')
        output = await op.wait(receive(broker.call('listAiModels', {})));
      else if (mode === 'json')
        output = await op.wait(
          receive(
            broker.call('createAiChatCompletion', {
              body: JSON.parse(body ?? '{}'),
            }),
          ),
        );
      else
        output = await op.wait(
          receive(
            broker.call('createAiChatCompletionStream', {
              body: JSON.parse(body ?? '{}'),
            }),
          ),
        );
      fence();
      op.assert();
    } catch (error) {
      if (output?.body?.streamId)
        void broker
          .streamCommand({ streamId: output.body.streamId, action: 'cancel' })
          .catch(() => {});
      if (error instanceof AdapterError) throw error;
      const value = /** @type {{code?:string,status?:number}} */ (error);
      if (
        ['common.unauthenticated', 'common.forbidden'].includes(
          value.code ?? '',
        ) ||
        [401, 403].includes(value.status ?? 0)
      )
        throw new AdapterError('AUTH');
      throw error;
    }
    const response = new Response(
      output.body?.streamId
        ? broker.openStream(output.body.streamId, op.signal)
        : JSON.stringify(output.body),
      {
        status: output.statusCode,
        headers: output.headers ?? {
          'content-type':
            path === 'models' || mode === 'json'
              ? 'application/json'
              : 'text/event-stream',
        },
      },
    );
    op.adopt(response);
    check(
      response.ok,
      [401, 403].includes(response.status)
        ? 'AUTH'
        : response.status === 429
          ? 'RATE_LIMIT'
          : 'SERVER',
    );
    return { response, lease: { expiresAt, signal: lifetime.signal } };
  };

  class AurigaAdapter extends hostApi.LlmAdapter {
    get availability() {
      const current =
        !disposed &&
        catalog &&
        !catalog.signal.aborted &&
        catalog.expiresAt > Date.now()
          ? catalog
          : undefined;
      return {
        directoryAvailable: !!current,
        authorized: !!current?.ids.length,
        available: current?.ids.some(generationReady) === true,
      };
    }
    /** @param {string} provider */
    providerInfo(provider) {
      route(provider);
      return { id: 'auriga', name: BRAND_NAME };
    }
    /** @param {string} provider */
    providerRetryPolicy(provider) {
      route(provider);
      return retryPolicy;
    }
    /** @param {string} provider @returns {Promise<import('./types.js').ModelInfo[]>} */
    async listModels(provider) {
      route(provider);
      const epoch = ++catalogEpoch;
      catalog = undefined;
      const op = begin(undefined);
      try {
        const { response, lease } = await send(op, 'models', undefined);
        check(
          response.headers.get('content-type')?.split(';')[0].trim() ===
            'application/json',
          'SERVER',
        );
        const models = modelDirectory(await readJson(response, op)).map(
          (item) => ({
            provider: 'auriga',
            id: item.id,
            name: item.id,
            ...(item.context_window === undefined
              ? {}
              : { context: { contextWindow: item.context_window } }),
          }),
        );
        op.verify();
        check(epoch === catalogEpoch, 'AUTH');
        catalog = { ids: models.map((item) => item.id), ...lease };
        return models;
      } catch (error) {
        throw error instanceof AdapterError
          ? error
          : new AdapterError('TRANSPORT');
      } finally {
        op.close();
        active.delete(op);
      }
    }
    /** @param {string} provider @param {string} model @param {AbortSignal} [signal]
     * @returns {Promise<import('./types.js').ModelInfo>} */
    async resolveModel(provider, model, signal) {
      route(provider);
      text(model, 128, 1);
      if (signal?.aborted) throw new AdapterError('ABORTED');
      const facts = limits[model];
      /** @type {import('./types.js').ModelInfo} */
      const result = { provider: 'auriga', id: model, name: model };
      if (facts?.contextWindow !== undefined) {
        integer(facts.contextWindow, 1);
        result.context = { contextWindow: facts.contextWindow };
      }
      if (facts?.defaultMaxTokens !== undefined) {
        integer(facts.defaultMaxTokens, 1);
        integer(facts.maxOutputTokens, 1);
        check(facts.defaultMaxTokens <= facts.maxOutputTokens);
        result.defaultMaxTokens = facts.defaultMaxTokens;
      }
      return result;
    }
    /** @param {import('./types.js').GenerateOptions} input
     * @returns {AsyncGenerator<import('./types.js').StreamChunk>} */
    async *stream(input) {
      /** @type {ReturnType<typeof begin> | undefined} */
      let op;
      try {
        route(input.provider);
        const body = buildRequest(
          input,
          limits[input.model] ?? {},
          mode === 'sse',
          countInputTokens,
        );
        op = begin(input.signal);
        const { response } = await send(
          op,
          'chat/completions',
          JSON.stringify(body),
        );
        const contentType = response.headers
          .get('content-type')
          ?.split(';')[0]
          .trim();
        const state = responseState(hostApi, body.model);
        if (mode === 'json') {
          check(contentType === 'application/json', 'SERVER');
          for (const chunk of state.json(await readJson(response, op))) {
            op.verify();
            yield chunk;
          }
          for (const chunk of state.complete()) {
            op.verify();
            if (chunk.type === 'finish') {
              op.close();
              active.delete(op);
            }
            yield chunk;
          }
        } else {
          check(contentType === 'text/event-stream', 'SERVER');
          for await (const event of readSse(response, op)) {
            if (event === '[DONE]') {
              for (const chunk of state.complete()) {
                op.verify();
                if (chunk.type === 'finish') {
                  op.close();
                  active.delete(op);
                }
                yield chunk;
              }
              return;
            }
            const packet = JSON.parse(event);
            if (packet?.error) throw new AdapterError('SERVER');
            for (const chunk of state.accept(packet)) {
              op.verify();
              yield chunk;
            }
          }
        }
      } catch (error) {
        const code = error instanceof AdapterError ? error.code : 'TRANSPORT';
        if (op) {
          op.close();
          active.delete(op);
        }
        yield {
          type: 'finish',
          reason: {
            kind: code === 'ABORTED' ? 'aborted' : 'error',
            failure: { code, message: `Auriga DSH AI: ${code}` },
          },
        };
      } finally {
        if (op) {
          op.close();
          active.delete(op);
        }
      }
    }
    dispose() {
      disposed = true;
      unsubscribe?.();
      catalog = undefined;
      for (const op of active) op.close();
      active.clear();
      lifetime.abort();
    }
  }
  return new AurigaAdapter();
}

/** Register with an explicit idempotent teardown. Caller owns the host fiber.
 * @param {import('./types.js').HostService} llm
 * @param {import('./types.js').AdapterOptions} options */
export function registerAurigaAdapter(llm, options) {
  check(llm && typeof llm.registerAdapter === 'function', 'NO_ADAPTER');
  const adapter = createAurigaAdapter(options);
  let unregister;
  try {
    unregister = llm.registerAdapter(['auriga'], adapter);
    check(typeof unregister === 'function', 'NO_ADAPTER');
  } catch {
    adapter.dispose();
    throw new AdapterError('NO_ADAPTER');
  }
  let disposed = false;
  return {
    adapter,
    dispose() {
      if (disposed) return;
      disposed = true;
      adapter.dispose();
      unregister();
    },
  };
}
