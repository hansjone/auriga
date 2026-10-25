import { CODES, HostSeamError } from '../errors.js';
import { createKeyedMutex } from '../keyed-mutex.js';
import { createNativeAuthority, incompatible } from './runtime.js';
import { hasNativePromptRequest, isNativeSubagentOwned } from './upstream.js';

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
    typeof agent.session.snapshotEvents !== 'function'
  )
    throw incompatible();
  validHeader(agent.session.header, id);
}

/** Implements HostPort against the lockfile-pinned upstream rc.2 services.
 * No model calls, module imports, service startup or network registration here.
 * security is a trusted OS broker prerequisite, not an isolation implementation.
 * @param {{ctx: import('./types.js').NativeContext, security: import('./types.js').NativeSecurity}} options
 * @returns {Promise<import('../adapter.js').HostPort>} */
export async function createNativeHostPort({ ctx, security }) {
  return createHostPortOperations({
    ctx,
    security: createNativeAuthority(security),
  });
}

/** Pure HostPort operations over an authority port; NOT an installation gate.
 * Production callers must use createNativeHostPort from index.js.
 * Native field shapes are pinned to the upstream package source evidence.
 * @param {{ctx: import('./types.js').NativeContext, security: import('./types.js').HostOperationAuthority, promptTimeoutMs?: number}} options
 * @returns {Promise<import('../adapter.js').HostPort>} */
export async function createHostPortOperations({
  ctx,
  security,
  promptTimeoutMs = 30000,
}) {
  const ready = async () => {
    try {
      await security.assertReady();
    } catch {
      throw incompatible();
    }
  };
  await ready();
  if (
    !Number.isSafeInteger(promptTimeoutMs) ||
    promptTimeoutMs < 1 ||
    promptTimeoutMs > 30000
  )
    throw incompatible();
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
  const uncertain = new Set();

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

  /** Native preset identity is a projection, not necessarily the initial header.
   * dsh-api-session-controller lib/index.js:339-340.
   * @param {string} id @param {string|null} expected */
  async function assertPreset(id, expected) {
    if (expected === null) return;
    let agent = ctx.agents.get(id);
    if (!agent) {
      const resolved = await ctx.sessionController.resolveAgent(id);
      if (resolved.error || !resolved.agent) throw incompatible();
      agent = resolved.agent;
    }
    liveAgent(agent, id, ctx);
    if (
      isNativeSubagentOwned(ctx, agent.session.header, agent) ||
      typeof ctx.sessionProjections?.stateOf !== 'function'
    )
      throw incompatible();
    if (
      ctx.sessionProjections.stateOf(agent.session, 'agentPreset') !== expected
    )
      throw new HostSeamError(CODES.sessionNotReady);
  }

  /** @param {string} id @param {string} requestId */
  async function hasRequest(id, requestId) {
    validId(requestId);
    const record = await required(id);
    const agent = ctx.agents.get(id);
    if (agent) {
      liveAgent(agent, id, ctx);
      return hasNativePromptRequest(agent, requestId);
    }
    const recorded = record.events.some(
      (event) =>
        event.type === 'user/message' &&
        event.data.source?.kind === 'user' &&
        event.data.source.rpcId === requestId,
    );
    if (recorded) return true;
    return false;
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
            isNativeSubagentOwned(ctx, existing.meta, ctx.agents.get(id))
          )
            throw new HostSeamError(CODES.sessionNotReady);
          await assertPreset(id, preset);
          if (!(await flushSession(id))) throw incompatible();
          await assertPreset(id, preset);
          return { id, cwd };
        }
        // A tombstone cannot be turned into a new conversation by create.
        if (ctx.workspaceRegistry.archivedSessionIds.includes(id))
          throw new HostSeamError(CODES.sessionNotReady);
        try {
          const resolvedPreset = await ctx.agentPresets.resolve(
            preset ?? undefined,
          );
          if (
            !resolvedPreset ||
            resolvedPreset.broken ||
            !opaque(resolvedPreset.id)
          )
            throw incompatible();
          await ready();
          const result = await ctx.sessionController.create({
            sessionId: id,
            cwd,
            agentPreset: resolvedPreset.id,
          });
          if (
            result?.sessionId !== id ||
            result.agentPreset !== resolvedPreset.id
          )
            throw incompatible();
          const created = await required(id);
          if (
            created.meta.cwd !== cwd ||
            isNativeSubagentOwned(ctx, created.meta, ctx.agents.get(id))
          )
            throw incompatible();
          await assertPreset(id, resolvedPreset.id);
          if (!(await flushSession(id))) throw incompatible();
          await assertPreset(id, resolvedPreset.id);
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
        if (isNativeSubagentOwned(ctx, record.meta, ctx.agents.get(id)))
          throw incompatible();
        try {
          await durable(id);
          const requestKey = `${id}\0${input.requestId}`;
          if (await hasRequest(id, input.requestId)) {
            uncertain.delete(requestKey);
            return { accepted: true };
          }
          if (uncertain.has(requestKey)) throw incompatible();
          const resolved = await ctx.sessionController.resolveAgent(id);
          if (resolved.error || !resolved.agent) throw incompatible();
          liveAgent(resolved.agent, id, ctx);
          if (
            isNativeSubagentOwned(
              ctx,
              resolved.agent.session.header,
              resolved.agent,
            ) ||
            resolved.agent.session.header.cwd !== record.meta.cwd
          )
            throw incompatible();
          await ready();
          if (ctx.workspaceRegistry.archivedSessionIds.includes(id))
            throw incompatible();
          // Resolve can attach a previously cold agent. Recheck pending evidence
          // after that transition before invoking the native scheduling API.
          if (await hasRequest(id, input.requestId)) return { accepted: true };
          const controller = new AbortController();
          /** @type {ReturnType<typeof setTimeout>|undefined} */
          let timeout;
          /** @type {Promise<never>} */
          const expired = new Promise((_, reject) => {
            timeout = setTimeout(() => {
              controller.abort();
              reject(incompatible());
            }, promptTimeoutMs);
          });
          let result;
          try {
            if (uncertain.size >= 1024) throw incompatible();
            uncertain.add(requestKey);
            result = await Promise.race([
              ctx.sessionController.prompt(
                {
                  sessionId: id,
                  requestId: input.requestId,
                  mode: 'queue',
                  content: [{ type: 'text', text: input.text }],
                },
                controller.signal,
              ),
              expired,
            ]);
          } finally {
            clearTimeout(timeout);
          }
          await ready();
          liveAgent(resolved.agent, id, ctx);
          if (result?.accepted !== true) throw incompatible();
          uncertain.delete(requestKey);
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
      return locks.run(id, () => flushSession(id));
    },
  };

  /** Called inside the existing session lock, including create adoption.
   * @param {string} id */
  async function flushSession(id) {
    try {
      const before = await required(id);
      if (isNativeSubagentOwned(ctx, before.meta, ctx.agents.get(id)))
        return false;
      const provider = ctx.sessionPersistence;
      await durable(id);
      let session = ctx.sessions.get(id);
      if (!session) {
        const record = await required(id);
        if (isNativeSubagentOwned(ctx, record.meta, ctx.agents.get(id)))
          return false;
        const resolved = await ctx.sessionController.resolveAgent(id);
        if (resolved.error || !resolved.agent) return false;
        liveAgent(resolved.agent, id, ctx);
        session = resolved.agent.session;
      }
      if (
        !session ||
        session.id !== id ||
        isNativeSubagentOwned(ctx, session.header, ctx.agents.get(id)) ||
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
  }
}
