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
const catalogArchiveRules = new Set([
  'bundled_dependency_required',
  'lifecycle_scripts_disallowed',
  'bundle_patch_missing',
  'native_build_disallowed',
]);
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
    this.host = hostOptions.hostFactory
      ? hostOptions.hostFactory(broker, this.streams)
      : new HostChannel(broker, this.streams, hostOptions);
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
    this.invalidationEvent =
      typeof broker.canSelfHeal === 'function'
        ? 'identity_invalidated'
        : 'changed';
    broker.on(this.invalidationEvent, this.changed);
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
        if (previous.context) {
          this.broker.assert(
            previous.context,
            typeof this.broker.canSelfHeal === 'function'
              ? { allowRevisionAdvance: true }
              : undefined,
          );
        }
        if (operation === 'network_open') {
          this.host.resolve(owner, input.executionContextId, 'session');
          const cached = await previous.result;
          if (cached.output) {
            this.streams.get(cached.output.body.streamId, owner);
            return response({
              output: {
                ...cached.output,
                body: {
                  ...cached.output.body,
                  registration: {
                    ...cached.output.body.registration,
                    created: false,
                  },
                },
              },
            });
          }
        }
      } catch {
        return response({ error: error('common.unauthenticated') });
      }
      return previous.result;
    }
    const deadline = Date.parse(message.expiresAt),
      remaining = deadline - Date.now();
    if (remaining <= 0 || remaining > 30000 || this.requests.size >= 512)
      return response({ error: error('common.validation_failed') });
    const entry = {
      hash,
      owner,
      context: undefined,
      until: performance.now() + remaining,
      result: undefined,
    };
    const run = async () => {
      let context;
      try {
        context = this.broker.guard(
          message.shellEpoch,
          message.expectedRevision,
        );
      } catch (e) {
        if (
          e.code === 'common.unauthenticated' &&
          message.shellEpoch === this.broker.epoch &&
          this.broker.canSelfHeal?.()
        ) {
          try {
            context = await this.broker.ensureAuthenticated({
              timeoutMs: Math.min(remaining, 10000),
              expectedRevision: message.expectedRevision,
            });
          } catch (healError) {
            return response({
              error: error(
                healError.code ?? 'common.unauthenticated',
                healError.code === 'common.unavailable',
              ),
            });
          }
        } else {
          return response({ error: error(e.code ?? 'common.unauthenticated') });
        }
      }
      const remainingAfterHeal = deadline - Date.now();
      if (remainingAfterHeal <= 0) {
        return response({ error: error('common.unavailable', true) });
      }
      entry.context = context;
      try {
        const output = await this.execute(
          definition,
          input,
          context,
          remainingAfterHeal,
          owner,
        );
        return response({ output });
      } catch (e) {
        return response({
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
              !e.code || e.code === 'common.unavailable',
            ),
        });
      }
    };
    entry.result = run();
    this.requests.set(message.requestId, entry);
    return entry.result;
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
    for (const param of definition.boundParams ?? []) {
      const value = context.identity?.[param.identity];
      if (value === undefined && !param.required) continue;
      if (value === undefined || !valid(param.schema, value))
        throw Object.assign(new Error('missing bound identity'), {
          code: 'common.unauthenticated',
        });
      if (param.in === 'path')
        route = route.replaceAll(`{${param.name}}`, encodeURIComponent(value));
      else query.set(param.name, value);
    }
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
    for (const key of [
      'tenantId',
      'ownerId',
      'memberId',
      'deviceId',
      'clientId',
    ]) {
      const expected = {
        tenantId: context.identity.tenantId,
        ownerId: context.identity.memberId,
        memberId: context.identity.memberId,
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
    let activeContext = context;
    const isTeam =
      definition.route === '/ai/mcp' ||
      definition.route.startsWith('/ai/team/node/');
    let token = isTeam
      ? undefined
      : await this.broker.tokenFor(
          definition.audience,
          activeContext,
          definition.operation,
        );
    this.broker.assert(activeContext, { allowRevisionAdvance: true });
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
          activeContext,
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
      const isMultipartUpload = [
        'UnifiedUploadResourceVersion',
        'uploadResourceVersion',
      ].includes(definition.operation);
      if (isMultipartUpload) {
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
            const upload = this.streams.consume(reference, activeContext, owner);
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
      const sendFetch = (bearer) =>
        this.fetcher(url.href, {
          method,
          redirect: 'error',
          credentials: 'omit',
          signal: controller.signal,
          headers: {
            ...headers,
            Authorization: `Bearer ${bearer}`,
            ...(contentType ? { 'Content-Type': contentType } : {}),
          },
          ...(requestBody !== undefined ? { body: requestBody } : {}),
        });
      let response = await sendFetch(token);
      const canReplayOn401 =
        !isMultipartUpload &&
        (method === 'GET' ||
          method === 'HEAD' ||
          Boolean(headers['idempotency-key'] || headers['Idempotency-Key']));
      if (
        response.status === 401 &&
        canReplayOn401 &&
        this.broker.canSelfHeal?.()
      ) {
        await response.body?.cancel?.().catch(() => {});
        const expectedIdentityKey = this.broker.identityKey?.({
          identity: context.identity,
        });
        try {
          activeContext = await this.broker.ensureAuthenticated({
            forceRefresh: true,
            timeoutMs: Math.min(remaining, 8000),
            expectedGeneration: context.generation,
            expectedIdentityKey,
          });
          this.broker.assert(activeContext, { allowRevisionAdvance: true });
          token = await this.broker.tokenFor(
            definition.audience,
            activeContext,
            definition.operation,
          );
          this.broker.assert(activeContext, { allowRevisionAdvance: true });
        } catch (healError) {
          if (healError?.code === 'common.unavailable')
            throw Object.assign(new Error('common.unavailable'), {
              code: 'common.unavailable',
            });
          throw Object.assign(new Error('common.unauthenticated'), {
            code: 'common.unauthenticated',
          });
        }
        response = await sendFetch(token);
      }
      const resolveStreamContext = () => {
        if (typeof this.broker.canSelfHeal !== 'function') {
          this.broker.assert(activeContext);
          return activeContext;
        }
        this.broker.assert(activeContext, { allowRevisionAdvance: true });
        if (activeContext.revision === this.broker.revision) {
          return activeContext;
        }
        const refreshed = this.broker.guard(
          activeContext.epoch,
          this.broker.revision,
        );
        if (
          refreshed.generation !== activeContext.generation ||
          this.broker.identityKey({ identity: refreshed.identity }) !==
            this.broker.identityKey({ identity: activeContext.identity })
        ) {
          throw Object.assign(new Error('common.unauthenticated'), {
            code: 'common.unauthenticated',
          });
        }
        return refreshed;
      };
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
        const streamContext = resolveStreamContext();
        clearTimeout(timeout);
        const reader = response.body.getReader();
        const stream = this.streams.create(streamContext, owner, () => {
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
        const streamContext = resolveStreamContext();
        clearTimeout(timeout);
        const reader = response.body.getReader();
        const stream = this.streams.create(streamContext, owner, () => {
          controller.abort();
          reader.cancel().catch(() => {});
        });
        this.pumpBinary(stream, reader).catch(() => {});
        return { statusCode: response.status, body: { streamId: stream.id } };
      }
      const ticketWire = [
        'UnifiedCreateDownloadTicket',
        'UnifiedRenewInstallation',
        'createMemberResourceDownloadTicket',
      ].includes(definition.operation);
      const raw = await boundedBody(response, ticketWire ? 65536 : undefined);
      this.broker.assert(activeContext, { allowRevisionAdvance: true });
      let body;
      try {
        body =
          ticketWire && response.ok
            ? { wire: raw.toString('base64') }
            : raw.length
              ? JSON.parse(raw.toString('utf8'))
              : {};
      } catch {
        throw new Error('invalid business response');
      }
      if (!response.ok) {
        const businessError = mapBusinessError(
          response.status,
          body?.code,
          response.headers.get('retry-after'),
        );
        const rule = body?.params?.rule;
        if (
          definition.operation === 'UnifiedUploadResourceVersion' &&
          businessError.code === 'common.validation_failed' &&
          catalogArchiveRules.has(rule)
        )
          Object.assign(businessError, { rule });
        throw Object.assign(new Error('business failure'), { businessError });
      }
      if (definition.operation === 'profile_read') {
        if (
          !body?.account ||
          body.account.id !== context.claims.sub ||
          body.account.tenantId !== context.identity.tenantId
        )
          throw new Error('invalid profile identity');
        let avatarUrl;
        const candidate = body.account.avatarUrl;
        if (
          typeof candidate === 'string' &&
          candidate.length <= 2048 &&
          /^https?:\/\//.test(candidate) &&
          !/[\s"'<>\\]/.test(candidate) &&
          !Array.from(candidate).some(
            (char) => char.charCodeAt(0) < 32 || char.charCodeAt(0) === 127,
          )
        ) {
          try {
            const parsed = new URL(candidate);
            if (
              parsed.hostname &&
              !parsed.username &&
              !parsed.password &&
              !parsed.hash &&
              parsed.port !== '0'
            )
              avatarUrl = candidate;
          } catch {
            /* Invalid presentation data does not change identity. */
          }
        }
        body = {
          memberId: context.identity.memberId,
          displayName: body.account.displayName,
          ...(avatarUrl ? { avatarUrl } : {}),
        };
      }
      if (definition.operation === 'provider_diagnostic') {
        if (
          !body ||
          !Array.isArray(body.items) ||
          body.items.some(
            (item) =>
              !item ||
              typeof item !== 'object' ||
              Array.isArray(item) ||
              typeof item.id !== 'string' ||
              !/^[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}$/i.test(item.id) ||
              typeof item.connectorType !== 'string' ||
              !item.displayName ||
              typeof item.displayName !== 'object' ||
              Array.isArray(item.displayName) ||
              Object.values(item.displayName).some(
                (text) => typeof text !== 'string',
              ) ||
              !Number.isInteger(item.order) ||
              !['redirect', 'qr', 'form', 'webauthn', 'device'].includes(
                item.primaryStep,
              ),
          )
        )
          throw new Error('invalid providers response');
        body = {
          available: body.items.some(
            (item) => item.id === context.identity.providerId,
          ),
        };
      }
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
    this.broker.off(this.invalidationEvent, this.changed);
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
