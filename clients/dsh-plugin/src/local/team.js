/* global AbortController, AbortSignal */
import { randomUUID } from 'node:crypto';
import { Buffer } from 'node:buffer';
import { setInterval, clearInterval } from 'node:timers';

const TOOL_NAMES = new Set([
  'auriga_list_endpoints',
  'auriga_open_thread',
  'auriga_reply_thread',
  'auriga_read_thread',
]);
const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
const opaque = (/** @type {unknown} */ value) =>
  typeof value === 'string' && value.length > 0 && value.length <= 512;

/** Native rc.3 output.render receives (arguments, value), independently of MCP content.
 * @param {any} registry @param {any[]} tools @param {(name:string,args:any,exec:any)=>Promise<any>} call */
export function registerTeamTools(registry, tools, call) {
  /** @type {(()=>void)[]} */
  const disposers = [];
  try {
    for (const tool of tools.filter((t) => TOOL_NAMES.has(t.name)))
      disposers.push(
        registry.register({
          name: tool.name,
          description: tool.description,
          parameters: tool.inputSchema,
          output: {
            schema: { type: 'object', additionalProperties: true },
            render: (/** @type {any} */ _args, /** @type {any} */ value) =>
              value.content ?? [
                {
                  type: 'text',
                  text: JSON.stringify(value.structuredContent ?? {}),
                },
              ],
          },
          execute: (/** @type {any} */ args, /** @type {any} */ exec) =>
            call(tool.name, args, exec),
        }),
      );
  } catch (error) {
    for (const dispose of disposers.splice(0)) dispose();
    throw error;
  }
  return () => {
    for (const dispose of disposers.splice(0)) dispose();
  };
}

/** Remote HTTP MCP only; actor/session never comes from model arguments.
 * @param {{platform:ReturnType<typeof import('./platform.js').createPlatform>,vault:Awaited<ReturnType<typeof import('./vault.js').createVault>>,getAccess:()=>Promise<string>,assertActive:()=>void,ctx:any,label:string,onState?:(state:{connected:boolean,unauthorized?:boolean})=>void}} options */
export function createTeam({
  platform,
  vault,
  getAccess,
  assertActive,
  ctx,
  label,
  onState = () => {},
}) {
  let nextId = 0,
    closed = false,
    polling = false,
    protocol = '2025-06-18';
  /** @type {ReturnType<typeof setInterval>|undefined} */
  let timer;
  /** @type {Promise<string>|undefined} */
  let endpointPending;
  /** @type {(()=>void)[]} */
  const disposers = [],
    controller = new AbortController();
  const transport = async (
    /** @type {string} */ path,
    /** @type {any} */ options,
  ) => {
    try {
      const result = await platform.request(path, {
        ...options,
        signal: options.signal
          ? AbortSignal.any([options.signal, controller.signal])
          : controller.signal,
      });
      active();
      onState({ connected: true });
      return result;
    } catch (error) {
      onState({
        connected: false,
        unauthorized:
          /** @type {any} */ (error).status === 401 ||
          /** @type {any} */ (error).status === 403,
      });
      throw error;
    }
  };
  const active = () => {
    if (closed) throw new Error('Team connection closed');
    assertActive();
  };
  /** @param {string} method @param {any} body @param {AbortSignal} [signal] */
  async function privateCall(method, body, signal) {
    active();
    const token = await getAccess();
    active();
    const result = await transport('/ai/team/node/' + method, {
      method: 'POST',
      body,
      token,
      signal,
    });
    active();
    return result;
  }
  /** @param {string} method @param {any} params @param {AbortSignal} [signal] @param {Record<string,string>} [references] */
  async function rpc(method, params, signal, references = {}) {
    active();
    const token = await getAccess();
    active();
    const id = ++nextId;
    const result = await transport('/ai/mcp', {
      method: 'POST',
      token,
      signal,
      headers: {
        'MCP-Protocol-Version': protocol,
        accept: 'application/json, text/event-stream',
        ...references,
      },
      body: { jsonrpc: '2.0', id, method, params },
    });
    active();
    if (
      result.jsonrpc !== '2.0' ||
      result.id !== id ||
      result.error ||
      !result.result
    )
      throw new Error('remote MCP operation failed');
    return result.result;
  }
  async function endpoint() {
    const current = vault.snapshot().endpoint;
    if (current) return current;
    if (endpointPending) return endpointPending;
    endpointPending = (async () => {
      const result = await privateCall('register', {
        label,
        receiveEnabled: true,
      });
      if (!UUID.test(result.endpoint?.id) || result.endpoint.revoked)
        throw new Error('invalid Team endpoint');
      await vault.update((state) => {
        state.endpoint = result.endpoint.id;
      });
      active();
      return result.endpoint.id;
    })();
    try {
      return await endpointPending;
    } finally {
      endpointPending = undefined;
    }
  }
  /** @param {string} sessionId @param {string} [threadId] @param {AbortSignal} [signal] */
  async function binding(sessionId, threadId, signal) {
    if (!opaque(sessionId)) throw new Error('real DSH session required');
    const endpointId = await endpoint();
    const result = await privateCall(
      'bind',
      {
        endpointId,
        hostSessionId: sessionId,
        ...(threadId ? { threadId } : {}),
      },
      signal,
    );
    const row = result.binding;
    if (
      !UUID.test(row?.id) ||
      row.endpointId !== endpointId ||
      row.hostSessionId !== sessionId
    )
      throw new Error('invalid Team binding');
    return row;
  }
  /** @param {string} name @param {any} args @param {any} exec */
  async function toolCall(name, args, exec) {
    active();
    if (
      !TOOL_NAMES.has(name) ||
      !opaque(exec?.agent?.session?.id) ||
      !opaque(exec.callId) ||
      !opaque(exec.rootCallId)
    )
      throw new Error('trusted DSH execution required');
    if (
      !args ||
      typeof args !== 'object' ||
      Array.isArray(args) ||
      Object.keys(args).some((k) =>
        [
          '_meta',
          'actor',
          'tenant',
          'tenantId',
          'sessionId',
          'hostSessionId',
          'accountId',
        ].includes(k),
      )
    )
      throw new Error('untrusted Team arguments');
    const row = await binding(exec.agent.session.id, undefined, exec.signal);
    const result = await rpc(
      'tools/call',
      {
        name,
        arguments: args,
        _meta: {
          'auriga/sessionId': exec.agent.session.id,
          'auriga/toolCallId': exec.callId,
          'auriga/rootCallId': exec.rootCallId,
        },
      },
      exec.signal,
      { 'Auriga-Endpoint-Id': row.endpointId, 'Auriga-Binding-Id': row.id },
    );
    if (result.isError) throw new Error('remote Team tool rejected');
    return result;
  }
  async function receive(/** @type {any} */ delivery) {
    active();
    const endpointId = await endpoint(),
      message = delivery?.message;
    if (
      !UUID.test(delivery?.id) ||
      delivery.targetEndpointId !== endpointId ||
      !UUID.test(message?.threadId) ||
      !UUID.test(message?.id) ||
      typeof message.text !== 'string' ||
      !message.text.trim() ||
      Buffer.byteLength(message.text) > 16384 ||
      !opaque(delivery.leaseToken) ||
      !Number.isFinite(Date.parse(delivery.leaseExpiresAt)) ||
      Date.parse(delivery.leaseExpiresAt) <= Date.now()
    )
      throw new Error('invalid delivery');
    let state = vault.snapshot(),
      thread = state.threads[message.threadId];
    if (!thread) {
      thread = { sessionId: randomUUID(), bindingId: null };
      await vault.update((s) => {
        s.threads[message.threadId] = thread;
      });
    }
    const created = await ctx.sessionController.create({
      sessionId: thread.sessionId,
    });
    active();
    if (created.sessionId !== thread.sessionId)
      throw new Error('DSH session identity mismatch');
    if (!thread.bindingId) {
      const bound = await binding(thread.sessionId, message.threadId);
      await vault.update((s) => {
        s.threads[message.threadId].bindingId = bound.id;
      });
      thread.bindingId = bound.id;
    }
    state = vault.snapshot();
    const previous = state.deliveries[delivery.id];
    if (
      previous &&
      (previous.sessionId !== thread.sessionId ||
        previous.messageId !== message.id)
    )
      throw new Error('delivery identity conflict');
    if (previous?.accepted !== true) {
      await vault.update((s) => {
        s.deliveries[delivery.id] = {
          sessionId: thread.sessionId,
          messageId: message.id,
          accepted: false,
        };
      });
      active();
      const accepted = await ctx.sessionController.prompt(
        {
          sessionId: thread.sessionId,
          requestId: delivery.id,
          content: [{ type: 'text', text: message.text }],
          mode: 'queue',
        },
        controller.signal,
      );
      active();
      if (accepted?.accepted !== true)
        throw new Error('DSH did not accept delivery');
      const session = ctx.sessions.get(thread.sessionId);
      if (!session || session.id !== thread.sessionId)
        throw new Error('accepted DSH session is not live');
      // rc.3 returns false when no persistence provider handled session/flush.
      // A memory-only queue must never cause a durable host_accepted ACK.
      if ((await ctx.sessions.flush(session)) !== true)
        throw new Error('DSH durable queue persistence unavailable');
      active();
      await vault.update((s) => {
        s.deliveries[delivery.id].accepted = true;
      });
    }
    // An ACK response may be lost. Durable accepted state skips prompt on re-lease;
    // the server-owned delivery UUID remains the requestId across every retry.
    const ack = await privateCall('ack', {
      endpointId,
      bindingId: thread.bindingId,
      hostSessionId: thread.sessionId,
      deliveryId: delivery.id,
      leaseToken: delivery.leaseToken,
      hostRequestId: delivery.id,
    });
    if (
      ack.delivery?.id !== delivery.id ||
      ack.delivery.status !== 'host_accepted'
    )
      throw new Error('Team ACK not confirmed');
  }
  async function poll() {
    if (closed || polling) return;
    polling = true;
    try {
      const endpointId = await endpoint();
      const response = await privateCall('pull', { endpointId, limit: 20 });
      for (const delivery of response.page?.items ?? []) {
        active();
        await receive(delivery);
      }
    } finally {
      polling = false;
    }
  }
  return {
    toolCall,
    receive,
    poll,
    verifyOnline: () => rpc('ping', {}),
    async start() {
      const initialized = await rpc('initialize', {
        protocolVersion: '2025-06-18',
        capabilities: {},
        clientInfo: { name: 'auriga-dsh', version: '0.0.0' },
      });
      if (initialized.protocolVersion !== '2025-06-18')
        throw new Error('unsupported remote MCP version');
      protocol = initialized.protocolVersion;
      await transport('/ai/mcp', {
        method: 'POST',
        token: await getAccess(),
        headers: { 'MCP-Protocol-Version': protocol },
        body: { jsonrpc: '2.0', method: 'notifications/initialized' },
      });
      const list = await rpc('tools/list', {});
      if (
        !Array.isArray(list.tools) ||
        [...TOOL_NAMES].some(
          (n) =>
            list.tools.filter((/** @type {any} */ t) => t.name === n).length !==
            1,
        )
      )
        throw new Error('Team tool manifest incomplete');
      await endpoint();
      disposers.push(registerTeamTools(ctx.tools, list.tools, toolCall));
      timer = setInterval(() => {
        void poll().catch(() => {
          /* Retry only private lease pull/ACK, never model writes. */
        });
      }, 2000);
      timer.unref();
      return this;
    },
    async revoke() {
      const id = vault.snapshot().endpoint;
      if (id) await privateCall('revoke', { endpointId: id });
    },
    close() {
      closed = true;
      controller.abort();
      clearInterval(timer);
      for (const dispose of disposers.splice(0)) dispose();
    },
  };
}
