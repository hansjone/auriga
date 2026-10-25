import { CODES, HostSeamError } from '../errors.js';
import { createKeyedMutex } from '../keyed-mutex.js';
import { createNativeAuthority, incompatible } from './runtime.js';

/** @param {unknown} value @returns {value is string} */
const opaque = (value) =>
  typeof value === 'string' &&
  value.length > 0 &&
  value.length <= 512 &&
  !value.includes('\0');
/** Parent lineage alone does not prove subagent ownership (forks also have it).
 * It remains unsupported until the host supplies verified ownership evidence.
 * @param {import('./types.js').Header} header */
const unsupportedLineage = (header) =>
  header.origin === 'subagent' || header.parentSession !== undefined;
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

/** Implements host/adapter.js HostPort against the native rc.3 services.
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
 * The separate evidence port avoids inventing native inbox field names.
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

  /** @param {string} id @param {string} requestId */
  async function hasRequest(id, requestId) {
    validId(requestId);
    const record = await required(id);
    const agent = ctx.agents.get(id);
    const recorded = record.events.some(
      (event) =>
        event.type === 'user/message' &&
        event.data.source?.kind === 'user' &&
        event.data.source.rpcId === requestId,
    );
    if (recorded) return true;
    if (agent) {
      liveAgent(agent, id, ctx);
      // A definite absence requires verified complete native pending evidence.
      // oclaw does not establish an inbox layout; never infer one from a fixture.
      if (typeof security.hasPromptRequest !== 'function') throw incompatible();
      const found = await security.hasPromptRequest(id, requestId);
      await ready();
      if (typeof found !== 'boolean') throw incompatible();
      return found;
    }
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
            (existing.meta.agentPreset ?? null) !== preset ||
            unsupportedLineage(existing.meta)
          )
            throw new HostSeamError(CODES.sessionNotReady);
          if (!(await flushSession(id))) throw incompatible();
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
            unsupportedLineage(created.meta)
          )
            throw incompatible();
          if (!(await flushSession(id))) throw incompatible();
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
        if (unsupportedLineage(record.meta)) throw incompatible();
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
            unsupportedLineage(resolved.agent.session.header) ||
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
      if (unsupportedLineage(before.meta)) return false;
      const provider = ctx.sessionPersistence;
      await durable(id);
      let session = ctx.sessions.get(id);
      if (!session) {
        const record = await required(id);
        if (unsupportedLineage(record.meta)) return false;
        const resolved = await ctx.sessionController.resolveAgent(id);
        if (resolved.error || !resolved.agent) return false;
        liveAgent(resolved.agent, id, ctx);
        session = resolved.agent.session;
      }
      if (
        !session ||
        session.id !== id ||
        unsupportedLineage(session.header) ||
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
