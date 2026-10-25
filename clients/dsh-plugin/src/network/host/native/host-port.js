import { CODES, HostSeamError } from '../errors.js';
import { createKeyedMutex } from '../keyed-mutex.js';
import { createNativeGate, incompatible } from './runtime.js';

/** @param {unknown} value @returns {value is string} */
const opaque = (value) =>
  typeof value === 'string' &&
  value.length > 0 &&
  value.length <= 512 &&
  !value.includes('\0');
/** @param {string} id */
function validId(id) {
  if (!opaque(id)) throw new HostSeamError(CODES.protocolError);
}
/** @param {import('./types.js').Header} header @param {string} id */
function validHeader(header, id) {
  if (
    !header ||
    header.id !== id ||
    typeof header.cwd !== 'string' ||
    !header.cwd ||
    header.cwd.includes('\0')
  )
    throw incompatible();
  return {
    id,
    cwd: header.cwd,
    ...(header.origin ? { origin: header.origin } : {}),
  };
}
/** @param {import('./types.js').Agent} agent @param {string} id @param {import('./types.js').NativeContext} ctx */
function liveAgent(agent, id, ctx) {
  if (
    !agent ||
    agent.id !== id ||
    agent.session?.id !== id ||
    ctx.agents.get(id) !== agent ||
    ctx.sessions.get(id) !== agent.session ||
    !Array.isArray(agent.inbox?.nextTurn) ||
    !Array.isArray(agent.inbox?.nextStep) ||
    typeof agent.session.snapshotEvents !== 'function'
  )
    throw incompatible();
  validHeader(agent.session.header, id);
}

/** Implements host/adapter.js HostPort against the native rc.3 services.
 * No model calls, module imports, service startup or network registration here.
 * security is a trusted OS broker prerequisite, not an isolation implementation.
 * @param {{ctx: import('./types.js').NativeContext, security: import('./types.js').NativeSecurity}} options
 * @returns {Promise<import('../adapter.js').HostPort>} */
export async function createNativeHostPort({ ctx, security }) {
  const ready = createNativeGate(security);
  await ready();
  if (
    !ctx ||
    typeof ctx.sessionController?.inspect !== 'function' ||
    typeof ctx.sessionController?.resolveAgent !== 'function' ||
    typeof ctx.sessionController?.create !== 'function' ||
    typeof ctx.sessionController?.prompt !== 'function' ||
    typeof ctx.sessionPersistence?.stat !== 'function' ||
    typeof ctx.sessions?.get !== 'function' ||
    typeof ctx.sessions?.flush !== 'function' ||
    typeof ctx.agents?.get !== 'function' ||
    typeof ctx.agentPresets?.resolve !== 'function' ||
    !Array.isArray(ctx.workspaceRegistry?.archivedSessionIds)
  )
    throw incompatible();
  const locks = createKeyedMutex();

  /** Cold reads do not resume or create. Only authoritative not-found is null.
   * @param {string} id */
  async function inspect(id) {
    validId(id);
    await ready();
    try {
      if (ctx.workspaceRegistry.archivedSessionIds.includes(id)) return null;
      const stored = await ctx.sessionPersistence.stat(id);
      if (!stored) return null;
      validHeader(stored.header, id);
      const result = await ctx.sessionController.inspect(id);
      validHeader(result.meta, id);
      if (!Array.isArray(result.events)) throw incompatible();
      await ready();
      if (ctx.workspaceRegistry.archivedSessionIds.includes(id)) return null;
      return result;
    } catch {
      throw incompatible();
    }
  }

  /** @param {string} id */
  async function required(id) {
    const record = await inspect(id);
    if (!record) throw new HostSeamError(CODES.sessionNotReady);
    return record;
  }

  /** @param {string} id */
  async function durable(id) {
    await ready();
    const stored = await ctx.sessionPersistence.stat(id);
    if (!stored) throw incompatible();
    validHeader(stored.header, id);
    if (
      (await security.assertDurableProvider(ctx.sessionPersistence, id)) !==
      undefined
    )
      throw incompatible();
    await ready();
  }

  /** @param {string} id @param {string} requestId */
  async function hasRequest(id, requestId) {
    validId(requestId);
    const record = await required(id);
    const agent = ctx.agents.get(id);
    /** @param {{source?: {kind: string, rpcId?: string}}} message */
    const matches = (message) =>
      message.source?.kind === 'user' && message.source.rpcId === requestId;
    if (agent) {
      liveAgent(agent, id, ctx);
      if (
        agent.inbox.nextTurn.some(matches) ||
        agent.inbox.nextStep.some(matches)
      )
        return true;
    }
    return record.events.some(
      (event) => event.type === 'user/message' && matches(event.data),
    );
  }

  return {
    async getSession(id) {
      const record = await inspect(id);
      return record ? validHeader(record.meta, id) : null;
    },
    async createSession({ id, cwd, preset }) {
      validId(id);
      if (
        typeof cwd !== 'string' ||
        !cwd ||
        cwd.includes('\0') ||
        (preset !== null && !opaque(preset))
      )
        throw new HostSeamError(CODES.protocolError);
      return locks.run(id, async () => {
        const existing = await inspect(id);
        if (existing) {
          if (
            existing.meta.cwd !== cwd ||
            (existing.meta.agentPreset ?? null) !== preset ||
            existing.meta.origin === 'subagent'
          )
            throw new HostSeamError(CODES.sessionNotReady);
          return { id, cwd };
        }
        // A tombstone cannot be turned into a new conversation by create.
        if (ctx.workspaceRegistry.archivedSessionIds.includes(id))
          throw new HostSeamError(CODES.sessionNotReady);
        try {
          if (preset !== null) {
            const resolved = await ctx.agentPresets.resolve(preset);
            if (!resolved || resolved.broken) throw incompatible();
          }
          await ready();
          const result = await ctx.sessionController.create({
            sessionId: id,
            cwd,
            ...(preset === null ? {} : { agentPreset: preset }),
          });
          if (result?.sessionId !== id) throw incompatible();
          const created = await required(id);
          if (
            created.meta.cwd !== cwd ||
            (created.meta.agentPreset ?? null) !== preset ||
            created.meta.origin === 'subagent'
          )
            throw incompatible();
          await durable(id);
          const session = ctx.sessions.get(id);
          if (
            !session ||
            session.id !== id ||
            (await ctx.sessions.flush(session)) !== true
          )
            throw incompatible();
          await ready();
          return { id, cwd };
        } catch {
          // Unknown create is queried by the core with this same durable id.
          throw incompatible();
        }
      });
    },
    async prompt(id, input) {
      validId(id);
      validId(input.requestId);
      if (input.mode !== 'queue' || typeof input.text !== 'string')
        throw new HostSeamError(CODES.protocolError);
      return locks.run(id, async () => {
        const record = await required(id);
        if (record.meta.origin === 'subagent') throw incompatible();
        try {
          await durable(id);
          if (await hasRequest(id, input.requestId)) return { accepted: true };
          const resolved = await ctx.sessionController.resolveAgent(id);
          if (resolved.error || !resolved.agent) throw incompatible();
          liveAgent(resolved.agent, id, ctx);
          if (
            resolved.agent.session.header.origin === 'subagent' ||
            resolved.agent.session.header.cwd !== record.meta.cwd
          )
            throw incompatible();
          await ready();
          if (ctx.workspaceRegistry.archivedSessionIds.includes(id))
            throw incompatible();
          const result = await ctx.sessionController.prompt(
            {
              sessionId: id,
              requestId: input.requestId,
              mode: 'queue',
              content: [{ type: 'text', text: input.text }],
            },
            new AbortController().signal,
          );
          await ready();
          liveAgent(resolved.agent, id, ctx);
          if (result?.accepted !== true) throw incompatible();
          return result;
        } catch {
          throw incompatible();
        }
      });
    },
    async hasPromptRequest(id, requestId) {
      return locks.run(id, async () => {
        try {
          return await hasRequest(id, requestId);
        } catch {
          throw incompatible();
        }
      });
    },
    async hasDurableProvider(id) {
      try {
        validId(id);
        await durable(id);
        return true;
      } catch {
        return false;
      }
    },
    async flush(id) {
      return locks.run(id, async () => {
        try {
          const before = await required(id);
          if (before.meta.origin === 'subagent') return false;
          const provider = ctx.sessionPersistence;
          await durable(id);
          let session = ctx.sessions.get(id);
          if (!session) {
            const record = await required(id);
            if (record.meta.origin === 'subagent') return false;
            const resolved = await ctx.sessionController.resolveAgent(id);
            if (resolved.error || !resolved.agent) return false;
            liveAgent(resolved.agent, id, ctx);
            session = resolved.agent.session;
          }
          if (
            !session ||
            session.id !== id ||
            session.header.origin === 'subagent' ||
            session.header.cwd !== before.meta.cwd
          )
            return false;
          if ((await ctx.sessions.flush(session)) !== true) return false;
          await durable(id);
          return (
            ctx.sessionPersistence === provider &&
            ctx.sessions.get(id) === session &&
            !ctx.workspaceRegistry.archivedSessionIds.includes(id)
          );
        } catch {
          return false;
        }
      });
    },
  };
}
