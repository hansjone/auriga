import { randomUUID } from 'node:crypto';
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
  constructor(broker, streams, fetcher, host) {
    this.broker = broker;
    this.streams = streams;
    this.fetcher = fetcher;
    this.host = host;
  }
  resolve(owner, id, kind) {
    return this.host.resolve(owner, id, kind);
  }
  async execute(definition, input, context, owner, signal) {
    const needsContext = Boolean(input.executionContextId),
      execution = needsContext
        ? this.resolve(
            owner,
            input.executionContextId,
            definition.tool ? 'tool' : 'session',
          )
        : undefined;
    const session =
      execution?.kind === 'tool'
        ? this.resolve(owner, execution.parentExecutionContextId, 'session')
        : execution;
    if (execution) {
      for (const [key, expected] of Object.entries({
        endpointId: session.endpointId,
        bindingId: session.bindingId,
        hostSessionId: session.hostSessionId,
      }))
        if (
          input.arguments &&
          key in input.arguments &&
          input.arguments[key] !== expected
        )
          throw fail();
      if (definition.tool && (!session.endpointId || !session.bindingId))
        throw fail();
    }
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
              _meta: {
                'auriga/sessionId': execution.hostSessionId,
                'auriga/toolCallId': execution.toolCallId,
                'auriga/rootCallId': execution.rootCallId,
              },
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
    if (execution)
      this.resolve(
        owner,
        input.executionContextId,
        definition.tool ? 'tool' : 'session',
      );
    signal.throwIfAborted();
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
          ...(session?.endpointId
            ? { 'Auriga-Endpoint-Id': session.endpointId }
            : {}),
          ...(session?.bindingId
            ? { 'Auriga-Binding-Id': session.bindingId }
            : {}),
        },
        body: JSON.stringify(body),
      },
    );
    const raw = await boundedBody(response, 1048576);
    signal.throwIfAborted();
    this.broker.assert(context);
    if (execution)
      this.resolve(
        owner,
        input.executionContextId,
        definition.tool ? 'tool' : 'session',
      );
    if (!response.ok)
      throw Object.assign(new Error('remote Team unavailable'), {
        status: response.status,
      });
    if (definition.route.startsWith('/ai/team/node/')) {
      const result = JSON.parse(raw.toString('utf8'));
      if (definition.operation === 'team_node_register') {
        if (!uuid.test(result.endpoint?.id ?? '') || result.endpoint.revoked)
          throw fail();
        session.endpointId = result.endpoint.id;
      }
      if (definition.operation === 'team_node_bind') {
        const binding = result.binding;
        if (
          !uuid.test(binding?.id ?? '') ||
          binding.endpointId !== session.endpointId ||
          binding.hostSessionId !== session.hostSessionId
        )
          throw fail();
        session.bindingId = binding.id;
      }
      if (definition.operation === 'team_node_revoke') {
        session.endpointId = undefined;
        session.bindingId = undefined;
      }
    }
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
        this.host.checkPeer(this.host.peers.get(owner));
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
        signal.throwIfAborted();
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
    if (execution) stream.executionContextId = execution.id;
    this.streams.push(stream, raw);
    this.streams.end(stream, 'end');
    const output = {
      statusCode: response.status,
      body: { streamId: stream.id },
    };
    requireValid(definition.output, output);
    return output;
  }
  close() {}
}
