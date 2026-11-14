import { createHash } from 'node:crypto';
import { performance } from 'node:perf_hooks';
import { requireValid, valid, contractFile } from './schema.mjs';
import { boundedBody } from './platform.mjs';
import { Streams } from './streams.mjs';
import { TeamProxy } from './team.mjs';
import { HostChannel } from './host.mjs';
import { NetworkProxy } from './network.mjs';
const operations = new Map(
  JSON.parse(
    contractFile('plugin/shell-business-operations.json'),
  ).operations.map((o) => [o.operation, o]),
);
const error = (code, retryable = false) => ({ code, retryable });
export function mapBusinessError(status, code, retryAfter) {
  const mapped = {
    400: ['common.validation_failed', false],
    401: ['common.unauthenticated', false],
    403: ['common.forbidden', false],
    404: ['common.not_found', false],
    409: ['common.conflict', false],
    412: ['common.precondition_failed', false],
    422: ['common.validation_failed', false],
    429: ['common.rate_limited', true],
    500: ['common.internal', true],
    502: ['common.unavailable', true],
    503: ['common.unavailable', true],
    504: ['common.unavailable', true],
  }[status] ?? [
    status >= 500 ? 'common.unavailable' : 'common.internal',
    status >= 500,
  ];
  const known = [
    'resource.digest_mismatch',
    'resource.signature_invalid',
    'resource.storage_unavailable',
  ].includes(code);
  return {
    ...error(
      known ? code : mapped[0],
      known ? code === 'resource.storage_unavailable' : mapped[1],
    ),
    upstreamStatus: status,
    ...(/^\d{1,3}$/.test(retryAfter ?? '') && Number(retryAfter) <= 300
      ? { retryAfterSeconds: Number(retryAfter) }
      : {}),
  };
}
/** Fixed-schema JSON operations. No arbitrary target, headers, or bearer input. */
export class BusinessProxy {
  constructor(
    broker,
    fetcher = globalThis.fetch,
    uploadDirectory,
    hostOptions = {},
  ) {
    this.streams = new Streams(broker, uploadDirectory);
    this.host = new HostChannel(broker, this.streams, hostOptions);
    this.network = new NetworkProxy(broker, this.host, this.streams, {
      directory: hostOptions.networkDirectory,
      config: broker.platform.config.network,
      core: hostOptions.networkCore,
      fetcher,
    });
    this.team = new TeamProxy(broker, this.streams, fetcher, this.host);
    this.broker = broker;
    this.fetcher = fetcher;
    this.requests = new Map();
    this.controllers = new Set();
    this.changed = () => {
      for (const c of this.controllers) c.abort();
    };
    broker.on('changed', this.changed);
  }
  async call(message, owner) {
    requireValid('ShellAuthBusinessCall', message);
    const { operation, input } = message.call,
      definition = operations.get(operation);
    const response = (value) =>
      requireValid('ShellAuthBusinessResult', {
        requestId: message.requestId,
        operation,
        ...value,
      });
    for (const [k, v] of this.requests)
      if (v.until <= performance.now()) this.requests.delete(k);
    const hash = createHash('sha256')
        .update(JSON.stringify(message))
        .digest('hex'),
      previous = this.requests.get(message.requestId);
    if (previous) {
      if (previous.owner !== owner || previous.hash !== hash)
        return response({ error: error('common.conflict') });
      // Cached results are still identity-bound, including tickets and reads.
      try {
        this.broker.assert(previous.context);
      } catch {
        return response({ error: error('common.unauthenticated') });
      }
      return previous.result;
    }
    const deadline = Date.parse(message.expiresAt),
      remaining = deadline - Date.now();
    if (remaining <= 0 || remaining > 30000 || this.requests.size >= 512)
      return response({ error: error('common.validation_failed') });
    let context;
    try {
      context = this.broker.guard(message.shellEpoch, message.expectedRevision);
    } catch (e) {
      return response({ error: error(e.code ?? 'common.unauthenticated') });
    }
    const result = this.execute(definition, input, context, remaining, owner)
      .then((output) => response({ output }))
      .catch((e) =>
        response({
          error:
            e.businessError ??
            error(
              [
                'common.conflict',
                'common.forbidden',
                'common.validation_failed',
                'common.unauthenticated',
              ].includes(e.code)
                ? e.code
                : 'common.unavailable',
              !e.code,
            ),
        }),
      );
    this.requests.set(message.requestId, {
      hash,
      owner,
      context,
      until: performance.now() + remaining,
      result,
    });
    return result;
  }
  async execute(definition, input, context, remaining, owner) {
    if (
      [
        'network_open',
        'network_send',
        'network_submit',
        'getNetworkKeys',
        'getNetworkKeyAnnouncement',
        'getPublicNetworkControl',
        'confirmNetworkControl',
      ].includes(definition.operation)
    )
      return this.network.execute(
        definition,
        input,
        context,
        owner,
        AbortSignal.timeout(remaining),
      );
    if (definition.operation === 'upload_open')
      return this.streams.openUpload(input, context, owner);
    if (definition.operation === 'upload_finish')
      return this.streams.finishUpload(input.uploadId, context, owner);
    // These transports need their own signed-wire/stream machinery. Never treat
    // a broker:nats or upload reference as a general HTTP address.
    if (!definition || definition.route.startsWith('broker:'))
      throw new Error('transport unavailable');
    let route = definition.route;
    const query = new URLSearchParams(),
      headers = {};
    for (const [key, value] of Object.entries(input)) {
      if (key === 'body' || key === 'headers') continue;
      if (route.includes(`{${key}}`))
        route = route.replaceAll(`{${key}}`, encodeURIComponent(String(value)));
      else if (value !== undefined) query.set(key, String(value));
    }
    if (route.includes('{') || route.includes('\\'))
      throw Object.assign(new Error('invalid route'), {
        code: 'common.validation_failed',
      });
    for (const key of ['tenantId', 'ownerId', 'deviceId', 'clientId']) {
      const expected = {
        tenantId: context.identity.tenantId,
        ownerId: context.identity.memberId,
        deviceId: context.identity.deviceId,
        clientId: context.identity.deviceId,
      }[key];
      if (
        input.body &&
        Object.hasOwn(input.body, key) &&
        input.body[key] !== expected
      )
        throw Object.assign(new Error('identity mismatch'), {
          code: 'common.forbidden',
        });
    }
    for (const [key, value] of Object.entries(input.headers ?? {})) {
      if (!definition.requestHeaders.includes(key))
        throw Object.assign(new Error('invalid header'), {
          code: 'common.validation_failed',
        });
      headers[key] = value;
    }
    const isTeam =
      definition.route === '/ai/mcp' ||
      definition.route.startsWith('/ai/team/node/');
    const token = isTeam
      ? undefined
      : await this.broker.tokenFor(definition.audience, context);
    this.broker.assert(context);
    const controller = new AbortController();
    this.controllers.add(controller);
    const timeout = setTimeout(() => controller.abort(), remaining);
    timeout.unref();
    try {
      if (
        definition.route === '/ai/mcp' ||
        definition.route.startsWith('/ai/team/node/')
      )
        return await this.team.execute(
          definition,
          input,
          context,
          owner,
          controller.signal,
        );
      const method =
        definition.method?.toUpperCase() ??
        ([
          'profile_read',
          'provider_diagnostic',
          'getNetworkKeys',
          'getNetworkKeyAnnouncement',
          'getPublicNetworkControl',
          'confirmNetworkControl',
        ].includes(definition.operation)
          ? 'GET'
          : 'POST');
      const url = new URL(route, this.broker.platform.config.platformOrigin);
      url.search = query.toString();
      /** @type {string|FormData|undefined} */
      let requestBody =
        input.body !== undefined ? JSON.stringify(input.body) : undefined;
      let contentType =
        input.body !== undefined ? 'application/json' : undefined;
      if (
        ['UnifiedUploadResourceVersion', 'uploadResourceVersion'].includes(
          definition.operation,
        )
      ) {
        const form = new FormData();
        for (const [key, value] of Object.entries(input.body)) {
          if (key === 'metadata') {
            form.append(
              key,
              new Blob([JSON.stringify(value)], { type: 'application/json' }),
            );
            continue;
          }
          for (const reference of Array.isArray(value) ? value : [value]) {
            const upload = this.streams.consume(reference, context, owner);
            form.append(
              key,
              new Blob([upload.bytes], { type: upload.mediaType }),
              reference.uploadId,
            );
          }
        }
        requestBody = form;
        contentType = undefined;
      }
      const response = await this.fetcher(url.href, {
        method,
        redirect: 'error',
        credentials: 'omit',
        signal: controller.signal,
        headers: {
          ...headers,
          Authorization: `Bearer ${token}`,
          ...(contentType ? { 'Content-Type': contentType } : {}),
        },
        ...(requestBody !== undefined ? { body: requestBody } : {}),
      });
      if (
        definition.operation === 'createAiChatCompletionStream' &&
        response.ok
      ) {
        if (
          !response.headers
            .get('content-type')
            ?.startsWith('text/event-stream') ||
          !response.body
        )
          throw new Error('invalid SSE response');
        clearTimeout(timeout);
        const reader = response.body.getReader();
        const stream = this.streams.create(context, owner, () => {
          controller.abort();
          reader.cancel().catch(() => {});
        });
        this.pumpSse(stream, reader)
          .finally(() => {
            this.controllers.delete(controller);
          })
          .catch(() => {});
        return {
          statusCode: response.status,
          body: { streamId: stream.id, encoding: 'sse_utf8' },
        };
      }
      if (definition.stream && response.ok) {
        if (!response.body) throw new Error('empty streaming response');
        clearTimeout(timeout);
        const reader = response.body.getReader();
        const stream = this.streams.create(context, owner, () => {
          controller.abort();
          reader.cancel().catch(() => {});
        });
        this.pumpBinary(stream, reader).catch(() => {});
        return { statusCode: response.status, body: { streamId: stream.id } };
      }
      const raw = await boundedBody(response);
      this.broker.assert(context);
      let body;
      try {
        body = raw.length ? JSON.parse(raw.toString('utf8')) : {};
      } catch {
        throw new Error('invalid business response');
      }
      if (!response.ok)
        throw Object.assign(new Error('business failure'), {
          businessError: mapBusinessError(
            response.status,
            body.code,
            response.headers.get('retry-after'),
          ),
        });
      const safeHeaders = {};
      for (const key of definition.responseHeaders) {
        const value = response.headers.get(key);
        if (value !== null) safeHeaders[key] = value;
      }
      const output = {
        statusCode: response.status,
        headers: safeHeaders,
        body,
      };
      if (!valid(definition.output, output))
        throw new Error('invalid business response');
      return output;
    } finally {
      clearTimeout(timeout);
      this.controllers.delete(controller);
    }
  }
  close() {
    this.network.close();
    this.host.close();
    this.team.close();
    this.streams.close();
    this.changed();
    this.broker.off('changed', this.changed);
    this.requests.clear();
  }
  async pumpSse(stream, reader) {
    let pending = Buffer.alloc(0),
      done = false;
    try {
      for (;;) {
        const chunk = await reader.read();
        if (chunk.done) break;
        pending = Buffer.concat([pending, Buffer.from(chunk.value)]);
        for (;;) {
          const text = pending.toString('utf8'),
            match = /\r?\n\r?\n/.exec(text);
          if (!match) break;
          const byteEnd = Buffer.byteLength(
            text.slice(0, match.index + match[0].length),
          );
          const frame = pending.subarray(0, byteEnd);
          pending = pending.subarray(byteEnd);
          const data = text
            .slice(0, match.index)
            .split(/\r?\n/)
            .filter((line) => line.startsWith('data:'))
            .map((line) => line.slice(5).replace(/^ /, ''))
            .join('\n');
          this.streams.push(stream, frame);
          await this.streams.waitDrain(stream);
          if (data === '[DONE]') {
            done = true;
            break;
          }
        }
        if (done) break;
        if (pending.length > 1048576) throw new Error('SSE event limit');
        if (!this.streams.streams.has(stream.id) || stream.terminal) return;
      }
      if (!done) throw new Error('SSE missing done');
      this.streams.end(stream, 'end');
    } catch {
      this.streams.end(stream, 'error', 'common.unavailable');
    } finally {
      await reader.cancel().catch(() => {});
      reader.releaseLock();
    }
  }
  async pumpBinary(stream, reader) {
    let size = 0;
    try {
      for (;;) {
        const chunk = await reader.read();
        if (chunk.done) break;
        size += chunk.value.length;
        if (size > 67108864) throw new Error('download limit');
        for (let offset = 0; offset < chunk.value.length; offset += 65536) {
          this.streams.push(
            stream,
            Buffer.from(chunk.value.subarray(offset, offset + 65536)),
          );
          await this.streams.waitDrain(stream);
        }
      }
      this.streams.end(stream, 'end');
    } catch {
      this.streams.end(stream, 'error', 'common.unavailable');
    } finally {
      await reader.cancel().catch(() => {});
      reader.releaseLock();
    }
  }
}
