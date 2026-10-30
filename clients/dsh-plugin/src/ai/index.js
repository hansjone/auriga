import { AdapterError, check, integer, only, text } from './guard.js';
import { buildRequest } from './request.js';
import { responseState } from './response.js';
import { operation, readJson, readSse } from './transport.js';

/** Creates an isolated adapter using only the module loaded from the public
 * @deepseek-ai/dsh-llm root. No host mutation until registerAurigaAdapter.
 * @param {import('./types.js').AdapterOptions} options */
export function createAurigaAdapter(options) {
  const { hostApi, fetch: controlledFetch, getAccessToken } = options;
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
  check(
    typeof controlledFetch === 'function' &&
      typeof getAccessToken === 'function',
    'NO_ADAPTER',
  );
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
  const origin = base.origin;
  const limits = JSON.parse(JSON.stringify(options.modelLimits ?? {}));
  const mode = options.responseMode ?? 'sse';
  check(mode === 'sse' || mode === 'json', 'NO_ADAPTER');
  const countInputTokens = options.countInputTokens;
  const retryPolicy = hostApi.resolveRetryPolicy(
    { mode: 'normal', maxRetries: 0 },
    'auriga.retryPolicy',
  );
  const active = new Set();
  let disposed = false;
  /** @type {{ids: string[], expiresAt: number, signal: AbortSignal} | undefined} */
  let catalog;
  let catalogEpoch = 0;
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
    active.add(op);
    return op;
  };
  /** @param {ReturnType<typeof operation>} op @param {'models' | 'chat/completions'} path @param {string | undefined} body */
  const send = async (op, path, body) => {
    op.assert();
    const lease = await op.wait(
      Promise.resolve().then(() => getAccessToken(op.signal)),
    );
    check(lease, 'MISSING_CREDENTIAL');
    text(lease.token, 16384, 1);
    check(/^[A-Za-z0-9._~+/-]+=*$/.test(lease.token), 'INVALID_CREDENTIAL');
    integer(lease.expiresAt, 1);
    check(
      lease.signal && typeof lease.signal.addEventListener === 'function',
      'AUTH',
    );
    const token = lease.token,
      expiresAt = lease.expiresAt,
      revoked = lease.signal;
    op.watch(revoked, 'AUTH');
    op.expire(expiresAt);
    op.assert();
    const pending = controlledFetch(`${origin}/ai/v1/${path}`, {
      method: body === undefined ? 'GET' : 'POST',
      headers: {
        ...hostApi.attributionHeaders(),
        authorization: `Bearer ${token}`,
        accept:
          body === undefined || mode === 'json'
            ? 'application/json'
            : 'text/event-stream',
        ...(body === undefined ? {} : { 'content-type': 'application/json' }),
      },
      credentials: 'omit',
      redirect: 'error',
      cache: 'no-store',
      referrerPolicy: 'no-referrer',
      signal: op.signal,
      ...(body === undefined ? {} : { body }),
    }).then((response) => {
      if (op.signal.aborted) void response.body?.cancel().catch(() => {});
      return response;
    });
    const response = await op.wait(pending);
    op.adopt(response);
    if (
      response.redirected ||
      (response.url && response.url !== `${origin}/ai/v1/${path}`) ||
      !response.ok
    ) {
      if (response.status === 401 || response.status === 403) {
        catalogEpoch++;
        catalog = undefined;
      }
      void response.body?.cancel().catch(() => {});
      throw new AdapterError(
        response.status === 401 || response.status === 403
          ? 'AUTH'
          : response.status === 429
            ? 'RATE_LIMIT'
            : 'SERVER',
      );
    }
    return { response, lease: { expiresAt, signal: revoked } };
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
      return { id: 'auriga', name: 'Auriga' };
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
        const list = only(await readJson(response, op), ['object', 'data']);
        check(
          list.object === 'list' &&
            Array.isArray(list.data) &&
            list.data.length <= 1000,
          'SERVER',
        );
        const ids = new Set();
        const models = list.data.map((raw) => {
          const item = only(raw, ['id', 'object', 'created', 'owned_by']);
          text(item.id, 128, 1);
          integer(item.created);
          check(
            item.object === 'model' &&
              item.owned_by === 'auriga' &&
              !ids.has(item.id),
            'SERVER',
          );
          ids.add(item.id);
          return { provider: 'auriga', id: item.id, name: item.id };
        });
        op.assert();
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
      /** @type {ReturnType<typeof operation> | undefined} */
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
            op.assert();
            yield chunk;
          }
          for (const chunk of state.complete()) {
            op.assert();
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
                op.assert();
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
              op.assert();
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
            failure: { code, message: `Auriga AI: ${code}` },
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
      catalog = undefined;
      for (const op of active) op.close();
      active.clear();
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
