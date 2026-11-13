import { randomUUID } from 'node:crypto';
import { performance } from 'node:perf_hooks';
import { requireValid } from './schema.mjs';
import { boundedBody } from './platform.mjs';
const fail = () =>
  Object.assign(new Error('trusted execution context unavailable'), {
    code: 'common.forbidden',
  });
const uuid = /^[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}$/i;
/** Context registration is an OS-authenticated host lifecycle operation, never
 * a model tool or page route. D supplies only values taken from real host ctx. */
export class TeamProxy {
  constructor(broker, streams, fetcher) {
    this.broker = broker;
    this.streams = streams;
    this.fetcher = fetcher;
    this.contexts = new Map();
    this.changed = () => this.contexts.clear();
    broker.on('changed', this.changed);
  }
  register(owner, record) {
    const context = this.broker.guard();
    if (
      !record ||
      Object.keys(record).some(
        (k) =>
          !['executionContextId', 'meta', 'endpointId', 'bindingId'].includes(
            k,
          ),
      ) ||
      ![record.executionContextId, record.endpointId, record.bindingId].every(
        (v) => typeof v === 'string' && uuid.test(v),
      )
    )
      throw fail();
    const meta = record.meta;
    if (
      !meta ||
      Object.keys(meta).sort().join(',') !==
        'auriga/rootCallId,auriga/sessionId,auriga/toolCallId' ||
      Object.values(meta).some(
        (v) => typeof v !== 'string' || !v || v.length > 512,
      )
    )
      throw fail();
    for (const [id, value] of this.contexts)
      if (value.until <= performance.now()) this.contexts.delete(id);
    if (
      this.contexts.size >= 1024 ||
      this.contexts.has(record.executionContextId)
    )
      throw fail();
    this.contexts.set(record.executionContextId, {
      ...structuredClone(record),
      owner,
      context,
      until: performance.now() + 600000,
    });
  }
  release(owner, id) {
    const record = this.contexts.get(id);
    if (record?.owner !== owner) throw fail();
    this.contexts.delete(id);
  }
  resolve(owner, id) {
    const value = this.contexts.get(id);
    if (!value || value.owner !== owner || value.until <= performance.now())
      throw fail();
    this.broker.assert(value.context);
    return value;
  }
  async execute(definition, input, context, owner, signal) {
    const needsContext = Boolean(input.executionContextId),
      execution = needsContext
        ? this.resolve(owner, input.executionContextId)
        : undefined;
    let body,
      method = 'POST';
    if (definition.route === '/ai/mcp') {
      const rpcMethod = definition.tool
        ? 'tools/call'
        : {
            team_initialize: 'initialize',
            team_list_tools: 'tools/list',
            team_ping: 'ping',
          }[definition.operation];
      if (!rpcMethod) throw fail();
      body = {
        jsonrpc: '2.0',
        id: randomUUID(),
        method: rpcMethod,
        params: definition.tool
          ? {
              name: definition.tool,
              arguments: input.arguments,
              _meta: execution.meta,
            }
          : rpcMethod === 'initialize'
            ? {
                protocolVersion: '2025-06-18',
                capabilities: {},
                clientInfo: { name: 'auriga-desktop', version: '1' },
              }
            : {},
      };
    } else {
      if (!execution || !definition.route.startsWith('/ai/team/node/'))
        throw fail();
      body = input.arguments;
    }
    const token = await this.broker.tokenFor(definition.audience, context);
    this.broker.assert(context);
    const response = await this.fetcher(
      new URL(definition.route, this.broker.platform.config.platformOrigin)
        .href,
      {
        method,
        redirect: 'error',
        credentials: 'omit',
        signal,
        headers: {
          Authorization: `Bearer ${token}`,
          'Content-Type': 'application/json',
          'MCP-Protocol-Version': '2025-06-18',
          accept: 'application/json, text/event-stream',
          ...(execution
            ? {
                'Auriga-Endpoint-Id': execution.endpointId,
                'Auriga-Binding-Id': execution.bindingId,
              }
            : {}),
        },
        body: JSON.stringify(body),
      },
    );
    const raw = await boundedBody(response, 1048576);
    this.broker.assert(context);
    if (execution) this.resolve(owner, input.executionContextId);
    if (!response.ok)
      throw Object.assign(new Error('remote Team unavailable'), {
        status: response.status,
      });
    // Preserve JSON-RPC and existing tool/transport response bytes; the receiving
    // host adapter parses them only after reconstructing the ordered byte stream.
    if (definition.route === '/ai/mcp') {
      const rpc = JSON.parse(raw.toString('utf8'));
      if (
        rpc.jsonrpc !== '2.0' ||
        rpc.id !== body.id ||
        rpc.error ||
        !rpc.result
      )
        throw new Error('invalid remote MCP result');
      if (definition.operation === 'team_initialize') {
        if (rpc.result.protocolVersion !== '2025-06-18')
          throw new Error('unsupported MCP version');
        this.broker.assert(context);
        const initialized = await this.fetcher(
          new URL('/ai/mcp', this.broker.platform.config.platformOrigin).href,
          {
            method: 'POST',
            redirect: 'error',
            credentials: 'omit',
            signal,
            headers: {
              Authorization: `Bearer ${token}`,
              'Content-Type': 'application/json',
              'MCP-Protocol-Version': '2025-06-18',
            },
            body: JSON.stringify({
              jsonrpc: '2.0',
              method: 'notifications/initialized',
            }),
          },
        );
        await boundedBody(initialized);
        this.broker.assert(context);
        if (!initialized.ok) throw new Error('MCP initialize failed');
      }
      if (!definition.stream) {
        const output = {
          statusCode: response.status,
          body:
            definition.operation === 'team_ping'
              ? { available: true }
              : rpc.result,
        };
        requireValid(definition.output, output);
        return output;
      }
    }
    const stream = this.streams.create(context, owner);
    this.streams.push(stream, raw);
    this.streams.end(stream, 'end');
    const output = {
      statusCode: response.status,
      body: { streamId: stream.id },
    };
    requireValid(definition.output, output);
    return output;
  }
  close() {
    this.contexts.clear();
    this.broker.off('changed', this.changed);
  }
}
