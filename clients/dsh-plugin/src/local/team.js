import { BUILTIN_TOOL_NAMES } from '../network/base-tools.js';
import { createHash, randomUUID } from 'node:crypto';
import { Buffer } from 'node:buffer';
import { setInterval, clearInterval } from 'node:timers';

const TOOL_NAMES = new Set([
  'auriga_list_endpoints',
  'auriga_open_thread',
  'auriga_reply_thread',
  'auriga_read_thread',
]);
const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
const OPEN_INTENT_TTL_MS = 60000;
const OPEN_INTENT_LIMIT = 256;
/** Expiry releases capacity, never establishes a guessed source identity.
 * Old records without deadlines are unknown outcomes immediately.
 * @param {any} state */
function expireOpenIntents(state) {
  const now = Date.now();
  for (const [key, intent] of Object.entries(state.pendingOpens ?? {})) {
    const row = /** @type {any} */ (intent);
    if (Number.isFinite(row.expiresAt) && row.expiresAt > now) continue;
    state.openOutcomes ??= {};
    state.openOutcomes[key] = {
      ...row,
      status: 'result_unknown',
      endedAt: now,
    };
    delete state.pendingOpens[key];
  }
  const outcomes = Object.keys(state.openOutcomes ?? {});
  for (const key of outcomes.slice(0, -OPEN_INTENT_LIMIT))
    delete state.openOutcomes[key];
}
const opaque = (/** @type {unknown} */ value) =>
  typeof value === 'string' && value.length > 0 && value.length <= 512;
const unsupportedOrigin = (/** @type {any} */ session) =>
  session?.header?.origin === 'subagent';
class UnsupportedSessionOrigin extends Error {
  code = 'session/agent-busy';
  constructor() {
    super('unsupported Team session origin: subagent');
  }
}

/** Native rc.3 output.render receives (arguments, value), independently of MCP content.
 * @param {any} registry @param {any[]} tools @param {(name:string,args:any,exec:any)=>Promise<any>} call */
export function registerTeamTools(registry, tools, call, logger = console) {
  /** @type {(()=>void)[]} */
  const disposers = [];
  try {
    for (const tool of tools.filter((t) => {
      if (
        (typeof t.name === 'string' && t.name.startsWith('auriga_net_')) ||
        BUILTIN_TOOL_NAMES.has(t.name)
      ) {
        logger.warn?.('[auriga-dsh] rejected conflicting remote tool');
        return false;
      }
      return TOOL_NAMES.has(t.name);
    }))
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

/** Fixed broker MCP operations; source context is taken from native execution only.
 * @param {{broker:any,vault:Awaited<ReturnType<typeof import('./vault.js').createVault>>,getSessionContext:(id:string)=>Promise<any>,getToolContext:(execution:any)=>Promise<any>,readStream:(id:string)=>Promise<Buffer>,finishToolContext?:(id:string)=>Promise<void>,assertActive:()=>void,ctx:any,label:string,onState?:(state:any)=>void}} options */
export function createTeam({
  broker,
  vault,
  getSessionContext,
  getToolContext,
  readStream,
  finishToolContext = (id) => broker.revoke(id),
  assertActive,
  ctx,
  label,
  onState = () => {},
}) {
  let closed = false,
    polling = false;
  /** @type {string|undefined} */ let controlStream;
  /** @type {string|undefined} */ let primarySession;
  /** @type {Map<string,Promise<string>>} */ const endpointFlights = new Map();
  /** @type {Map<string,{context:string,id:string}>} */ const endpointContexts =
    new Map();
  /** @type {ReturnType<typeof setInterval>|undefined} */
  let timer;
  /** @type {(()=>void)[]} */
  const disposers = [],
    controller = new AbortController();
  const active = () => {
    if (closed) throw new Error('Team connection closed');
    assertActive();
  };
  async function expireSourceIntents() {
    if (
      Object.values(vault.snapshot().pendingOpens ?? {}).some(
        (/** @type {any} */ row) =>
          !Number.isFinite(row.expiresAt) || row.expiresAt <= Date.now(),
      )
    )
      await vault.update((state) => {
        active();
        expireOpenIntents(state);
      });
  }
  async function decode(/** @type {any} */ output) {
    const value = output.body?.streamId
      ? JSON.parse((await readStream(output.body.streamId)).toString('utf8'))
      : output.body;
    active();
    return value;
  }
  async function endpoint(
    /** @type {string|undefined} */ sessionId = primarySession,
  ) {
    active();
    if (!sessionId) throw new Error('trusted DSH session required');
    const context = await getSessionContext(sessionId);
    const existing = endpointContexts.get(sessionId);
    if (existing && existing.context === context.executionContextId)
      return existing.id;
    let pending = endpointFlights.get(sessionId);
    if (!pending) {
      pending = (async () => {
        const result = await decode(
          await broker.call('team_node_register', {
            executionContextId: context.executionContextId,
            arguments: { label, receiveEnabled: true },
          }),
        );
        if (!UUID.test(result.endpoint?.id) || result.endpoint.revoked)
          throw new Error('invalid Team endpoint');
        const old = vault.snapshot().endpoint;
        if (old && old !== result.endpoint.id)
          throw new Error('Team endpoint changed across native sessions');
        await vault.update((state) => {
          active();
          state.endpoint = result.endpoint.id;
        });
        active();
        endpointContexts.set(sessionId, {
          context: context.executionContextId,
          id: result.endpoint.id,
        });
        onState({ connected: true });
        return result.endpoint.id;
      })();
      endpointFlights.set(sessionId, pending);
    }
    try {
      return await pending;
    } finally {
      if (endpointFlights.get(sessionId) === pending)
        endpointFlights.delete(sessionId);
    }
  }
  /** @param {string} method @param {any} body @param {AbortSignal} [signal] */
  async function privateCall(method, body, signal) {
    active();
    signal?.throwIfAborted();
    const sid = body.hostSessionId ?? primarySession;
    await endpoint(sid);
    const executionContextId = (await getSessionContext(sid))
      .executionContextId;
    const input = { executionContextId, arguments: body };
    let output;
    switch (method) {
      case 'bind':
        output = await broker.call('team_node_bind', input);
        break;
      case 'pull':
        output = await broker.call('team_node_pull', input);
        break;
      case 'ack':
        output = await broker.call('team_node_ack', input);
        break;
      case 'revoke':
        output = await broker.call('team_node_revoke', input);
        break;
      default:
        throw new Error('unknown fixed Team node operation');
    }
    signal?.throwIfAborted();
    return decode(output);
  }
  /** @param {string} method @param {any} params @param {AbortSignal} [signal] @param {Record<string,string>} [_references] @param {any} [execution] */
  async function rpc(method, params, signal, _references = {}, execution) {
    active();
    signal?.throwIfAborted();
    let output;
    if (method === 'initialize') {
      output = await broker.call('team_initialize', {
        protocolVersion: '2025-06-18',
      });
      controlStream = output.body.streamId;
    } else if (method === 'tools/list')
      output = await broker.call('team_list_tools', {
        streamId: controlStream,
      });
    else if (method === 'ping')
      output = await broker.call('team_ping', { streamId: controlStream });
    else if (method === 'tools/call') {
      const context = execution;
      const input = {
        executionContextId: context.executionContextId,
        arguments: params.arguments,
      };
      switch (params.name) {
        case 'auriga_list_endpoints':
          output = await broker.call('team_list_endpoints', input);
          break;
        case 'auriga_open_thread':
          output = await broker.call('team_open_thread', input);
          break;
        case 'auriga_reply_thread':
          output = await broker.call('team_reply_thread', input);
          break;
        case 'auriga_read_thread':
          output = await broker.call('team_read_thread', input);
          break;
        default:
          throw new Error('unknown fixed Team tool');
      }
      const result = await decode(output);
      if (result.jsonrpc !== '2.0' || result.error || !result.result)
        throw new Error('remote MCP operation failed');
      return result.result;
    } else throw new Error('unknown fixed Team operation');
    const value = await decode(output);
    signal?.throwIfAborted();
    onState({ connected: endpointContexts.size > 0 });
    return output.body?.streamId ? value.result : value;
  }
  /** @param {string} sessionId @param {string} [threadId] @param {AbortSignal} [signal] */
  async function binding(sessionId, threadId, signal) {
    if (!opaque(sessionId)) throw new Error('real DSH session required');
    const endpointId = await endpoint(sessionId);
    primarySession = sessionId;
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
    const sessionId = exec.agent.session.id;
    if (unsupportedOrigin(exec.agent.session))
      throw new UnsupportedSessionOrigin();
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
    const context = await getToolContext(exec);
    try {
      const row = await binding(sessionId, undefined, exec.signal);
      // Persist bounded recovery evidence before sending. Independent first
      // messages remain admissible; subsequent messages require a source mapping.
      // Never automatically replay a model write or renew a retry's deadline.
      let intentKey = '';
      if (name === 'auriga_open_thread') {
        if (!opaque(args.idempotencyKey))
          throw new Error('Team open idempotency key required');
        intentKey = createHash('sha256')
          .update(JSON.stringify([row.endpointId, row.id, args.idempotencyKey]))
          .digest('hex');
        const intent = {
          endpointId: row.endpointId,
          sessionId,
          bindingId: row.id,
          targetEndpointId: args.targetEndpointId,
          fingerprint: createHash('sha256')
            .update(JSON.stringify([args.targetEndpointId, args.text]))
            .digest('hex'),
          expiresAt: Date.now() + OPEN_INTENT_TTL_MS,
        };
        await vault.update((state) => {
          active();
          if (state.endpoint !== row.endpointId)
            throw new Error('Team endpoint changed');
          state.pendingOpens ??= {};
          expireOpenIntents(state);
          const previous = state.pendingOpens[intentKey];
          if (
            previous &&
            (previous.endpointId !== intent.endpointId ||
              previous.sessionId !== intent.sessionId ||
              previous.bindingId !== intent.bindingId ||
              previous.fingerprint !== intent.fingerprint)
          )
            throw new Error('Team open intent conflict');
          if (
            !previous &&
            Object.keys(state.pendingOpens).length >= OPEN_INTENT_LIMIT
          )
            throw new Error('too many unresolved Team opens');
          state.pendingOpens[intentKey] = previous ?? intent;
        });
        active();
      }
      const result = await rpc(
        'tools/call',
        {
          name,
          arguments: args,
          _meta: {
            'auriga/sessionId': sessionId,
            'auriga/toolCallId': exec.callId,
            'auriga/rootCallId': exec.rootCallId,
          },
        },
        exec.signal,
        { 'Auriga-Endpoint-Id': row.endpointId, 'Auriga-Binding-Id': row.id },
        context,
      );
      // A rejection on a retry cannot prove an earlier request did not commit.
      // Keep its fixed deadline instead of reopening the random-mapping window.
      if (result.isError) throw new Error('remote Team tool rejected');
      if (name === 'auriga_open_thread') {
        const message = result.structuredContent?.message;
        if (
          !UUID.test(message?.id) ||
          !UUID.test(message?.threadId) ||
          message.authorEndpointId !== row.endpointId ||
          !UUID.test(row.id) ||
          row.hostSessionId !== sessionId
        )
          throw new Error('invalid Team source thread response');
        await vault.update((state) => {
          active();
          if (state.endpoint !== row.endpointId)
            throw new Error('Team endpoint changed');
          const existing = state.threads[message.threadId];
          if (
            existing &&
            (existing.sessionId !== sessionId ||
              (existing.bindingId !== null && existing.bindingId !== row.id))
          )
            throw new Error('Team source thread binding conflict');
          state.threads[message.threadId] = { sessionId, bindingId: row.id };
          delete state.pendingOpens[intentKey];
          if (state.openOutcomes) delete state.openOutcomes[intentKey];
        });
        active();
      }
      return result;
    } finally {
      await finishToolContext(context.executionContextId).catch(() => {});
    }
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
    await expireSourceIntents();
    let state = vault.snapshot(),
      thread = state.threads[message.threadId];
    if (!thread) {
      await vault.update((s) => {
        active();
        if (s.endpoint !== endpointId) throw new Error('Team endpoint changed');
        if (s.threads[message.threadId]) {
          thread = s.threads[message.threadId];
          return;
        }
        // The core leases only the earliest unacknowledged message. A new
        // receiver therefore starts at sequence 1. A later message without a
        // mapping may be a source reply, even after its open intent expired.
        if (
          String(message.sequence) !== '1' ||
          message.authorEndpointId === endpointId
        ) {
          const pending = Object.values(s.pendingOpens ?? {}).some(
            (/** @type {any} */ intent) =>
              intent.targetEndpointId === message.authorEndpointId,
          );
          throw new Error(
            pending
              ? 'Team source open pending; retry with its idempotency key'
              : 'Team source result unknown; mapping unavailable',
          );
        }
        thread = { sessionId: randomUUID(), bindingId: null };
        s.threads[message.threadId] = thread;
      });
    }
    if (!thread.bindingId) {
      // rc.3 create is a blank-session placement operation, not a resume API:
      // it may reject another cwd or mint a new ID after a turn has started.
      // A durable binding already owns the identity; prompt resumes it natively.
      const created = await ctx.sessionController.create({
        sessionId: thread.sessionId,
      });
      active();
      if (created.sessionId !== thread.sessionId)
        throw new Error('DSH session identity mismatch');
      const session = ctx.sessions.get(thread.sessionId);
      if (!session || session.id !== thread.sessionId)
        throw new Error('created DSH session is not live');
      // Once bindingId is durable, every retry uses prompt's cold resolver.
      // Persist the native identity before publishing that resume-only state.
      if ((await ctx.sessions.flush(session)) !== true)
        throw new Error('created DSH session persistence unavailable');
      active();
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
    if (previous?.status === 'unsupported')
      throw new UnsupportedSessionOrigin();
    const markUnsupported = async () => {
      await vault.update((s) => {
        active();
        s.deliveries[delivery.id] = {
          sessionId: thread.sessionId,
          messageId: message.id,
          accepted: false,
          status: 'unsupported',
          reason: 'subagent_origin',
        };
      });
      throw new UnsupportedSessionOrigin();
    };
    if (previous?.accepted !== true) {
      if (unsupportedOrigin(ctx.sessions.get(thread.sessionId)))
        await markUnsupported();
      await vault.update((s) => {
        s.deliveries[delivery.id] = {
          sessionId: thread.sessionId,
          messageId: message.id,
          accepted: false,
        };
      });
      active();
      let accepted;
      try {
        accepted = await ctx.sessionController.prompt(
          {
            sessionId: thread.sessionId,
            requestId: delivery.id,
            content: [
              {
                type: 'text',
                text:
                  'Team communication context (server-issued JSON):\n' +
                  JSON.stringify({
                    threadId: message.threadId,
                    messageId: message.id,
                  }),
              },
              { type: 'text', text: message.text },
            ],
            mode: 'queue',
          },
          controller.signal,
        );
      } catch (error) {
        active();
        // The native cold resolver may only expose ownership after restoration.
        // A busy ordinary agent is retryable; only known subagent ownership is terminal.
        if (unsupportedOrigin(ctx.sessions.get(thread.sessionId)))
          await markUnsupported();
        throw error;
      }
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
    if (closed || polling || !primarySession) return;
    polling = true;
    try {
      await expireSourceIntents();
      const endpointId = await endpoint();
      const response = await privateCall('pull', { endpointId, limit: 20 });
      let firstError;
      // Transitional pull transport: isolate each lease without bypassing native
      // ownership or acknowledging a failed/unsupported delivery.
      for (const delivery of response.page?.items ?? []) {
        active();
        try {
          await receive(delivery);
        } catch (error) {
          active();
          if (
            /** @type {any} */ (error).status === 401 ||
            /** @type {any} */ (error).status === 403
          )
            throw error;
          if (!(error instanceof UnsupportedSessionOrigin))
            firstError ??= error;
        }
      }
      if (firstError) throw firstError;
    } finally {
      polling = false;
    }
  }
  return {
    get connected() {
      return !closed && endpointContexts.size > 0;
    },
    async activateSession(/** @type {string} */ sessionId) {
      await endpoint(sessionId);
      primarySession = sessionId;
    },
    toolCall,
    receive,
    poll,
    verifyOnline: () => rpc('ping', {}),
    async start() {
      await expireSourceIntents();
      const initialized = await rpc('initialize', {
        protocolVersion: '2025-06-18',
        capabilities: {},
        clientInfo: { name: 'auriga-dsh', version: '0.0.0' },
      });
      if (initialized.protocolVersion !== '2025-06-18')
        throw new Error('unsupported remote MCP version');
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
      disposers.push(
        registerTeamTools(ctx.tools, list.tools, toolCall, ctx.logger),
      );
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
