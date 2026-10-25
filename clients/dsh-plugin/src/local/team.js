/* global AbortSignal */
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
    if (closed || polling) return;
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
      protocol = initialized.protocolVersion;
      await transport('/ai/mcp', {
        method: 'POST',
        token: await getAccess(),
        headers: {
          'MCP-Protocol-Version': protocol,
          accept: 'application/json, text/event-stream',
        },
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
