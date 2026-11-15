import { randomUUID } from 'node:crypto';
import { createTeam as createBrokerTeam } from '../src/local/team.js';
import { validate } from './broker-fixture.js';

/** Preserve the existing strict HTTP/core and durable-host peers behind C's
 * fixed-operation boundary. These are protocol doubles, not OS peer evidence. */
export function createTeam(options) {
  const { platform, getAccess, ctx } = options;
  const contexts = new Map(),
    sessions = new Map(),
    streams = new Map(),
    bindings = new Map();
  const shellEpoch = randomUUID();
  let endpointId;
  const raw = (value) => {
    const streamId = randomUUID();
    streams.set(streamId, Buffer.from(JSON.stringify(value)));
    return { statusCode: 200, body: { streamId } };
  };
  const registerSession = async (id) => {
    if (!ctx.sessions?.get(id))
      throw new Error('trusted native session required');
    let context = sessions.get(id);
    if (!context) {
      context = {
        executionContextId: randomUUID(),
        hostSessionId: id,
        kind: 'session',
      };
      contexts.set(context.executionContextId, context);
      sessions.set(id, context);
    }
    return context;
  };
  const rpc = async (method, params, context) => {
    const binding = context && bindings.get(context.hostSessionId);
    return platform.request('/ai/mcp', {
      method: 'POST',
      token: await getAccess(),
      headers: {
        accept: 'application/json, text/event-stream',
        'MCP-Protocol-Version': '2025-06-18',
        ...(binding
          ? {
              'Auriga-Endpoint-Id': endpointId,
              'Auriga-Binding-Id': binding.id,
            }
          : {}),
      },
      body: {
        jsonrpc: '2.0',
        ...(method === 'notifications/initialized' ? {} : { id: randomUUID() }),
        method,
        ...(params ? { params } : {}),
      },
    });
  };
  const broker = {
    async revoke(id) {
      contexts.delete(id);
    },
    async call(operation, input) {
      validate('ShellAuthBusinessCall', {
        protocolVersion: 1,
        requestId: randomUUID(),
        shellEpoch,
        expectedRevision: 1,
        expiresAt: new Date(Date.now() + 10000).toISOString(),
        call: { operation, input },
      });
      if (operation === 'team_initialize') {
        const result = await rpc('initialize', {
          protocolVersion: input.protocolVersion,
          capabilities: {},
          clientInfo: { name: 'contract-peer', version: '1' },
        });
        await rpc('notifications/initialized');
        return raw(result);
      }
      if (operation === 'team_list_tools')
        return { statusCode: 200, body: (await rpc('tools/list')).result };
      if (operation === 'team_ping')
        return { statusCode: 200, body: (await rpc('ping')).result };
      const context = contexts.get(input.executionContextId);
      if (!context) throw new Error('invalid source context');
      if (operation.startsWith('team_node_')) {
        const action = operation.slice('team_node_'.length);
        const result = await platform.request('/ai/team/node/' + action, {
          method: 'POST',
          token: await getAccess(),
          body: input.arguments,
        });
        if (action === 'register') endpointId = result.endpoint.id;
        if (action === 'bind')
          bindings.set(context.hostSessionId, result.binding);
        return raw(result);
      }
      const name = {
        team_list_endpoints: 'auriga_list_endpoints',
        team_open_thread: 'auriga_open_thread',
        team_reply_thread: 'auriga_reply_thread',
        team_read_thread: 'auriga_read_thread',
      }[operation];
      if (!name || context.kind !== 'tool')
        throw new Error('invalid fixed tool');
      return raw(
        await rpc(
          'tools/call',
          {
            name,
            arguments: input.arguments,
            _meta: {
              'auriga/sessionId': context.hostSessionId,
              'auriga/toolCallId': context.toolCallId,
              'auriga/rootCallId': context.rootCallId,
            },
          },
          context,
        ),
      );
    },
  };
  const team = createBrokerTeam({
    ...options,
    broker,
    getSessionContext: registerSession,
    async getToolContext(execution) {
      const session = execution?.agent?.session;
      if (
        !session ||
        ctx.sessions?.get(session.id) !== session ||
        !execution.callId ||
        !execution.rootCallId ||
        execution.signal?.aborted
      )
        throw new Error('trusted native execution required');
      await registerSession(session.id);
      const context = {
        executionContextId: randomUUID(),
        hostSessionId: session.id,
        kind: 'tool',
        toolCallId: execution.callId,
        rootCallId: execution.rootCallId,
      };
      contexts.set(context.executionContextId, context);
      return context;
    },
    async readStream(id) {
      const bytes = streams.get(id);
      streams.delete(id);
      return bytes;
    },
  });
  const start = team.start.bind(team);
  team.start = async () => {
    await start();
    if (options.sourceSessionId)
      await team.activateSession(options.sourceSessionId);
    return team;
  };
  return team;
}
