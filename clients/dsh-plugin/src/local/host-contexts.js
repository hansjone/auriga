/** Verified native execution -> K2 private context handles. No page/model/skill
 * input can register an owner or choose another host's session.
 * @param {{broker:any,identity:()=>any,ctx:any}} options */
export function createHostContexts({ broker, identity, ctx }) {
  /** @type {Map<string,{stamp:string,handle:any,native:any}>} */ const sessions =
    new Map();
  /** @type {Map<string,string>} */ const owners = new Map();
  /** @type {Map<string,Promise<any>>} */ const flights = new Map();
  /** @type {Map<string,{handle:any,alive:()=>boolean,dispose:()=>void,retain:()=>void}>} */
  const tools = new Map();
  let closed = false,
    generation = 0;
  /** @type {any} */ let host;
  const fail = () => {
    throw Object.assign(new Error('common.forbidden'), {
      code: 'common.forbidden',
    });
  };
  const stamp = () => {
    const who = identity();
    return who
      ? JSON.stringify([who.tenantId, who.ownerId, who.providerId])
      : '';
  };
  function invalidate() {
    generation++;
    for (const id of tools.keys()) void retireTool(id);
    for (const row of sessions.values())
      void broker.revoke(row.handle.executionContextId).catch(() => {});
    sessions.clear();
    flights.clear();
  }
  async function retireTool(/** @type {string} */ id) {
    const row = tools.get(id);
    if (!row) return;
    tools.delete(id);
    row.dispose();
    await broker.revoke(id).catch(() => {});
  }
  async function session(/** @type {string} */ id) {
    const current = stamp(),
      native = ctx.sessions?.get(id),
      g = generation;
    if (
      closed ||
      !current ||
      !native ||
      (owners.has(id) && owners.get(id) !== current)
    )
      return fail();
    owners.set(id, current);
    const check = broker.state.fence();
    const row = sessions.get(id);
    if (
      row &&
      row.native === native &&
      row.stamp === current &&
      Date.parse(row.handle.expiresAt) > Date.now()
    )
      return row.handle;
    if (row) {
      sessions.delete(id);
      await broker.revoke(row.handle.executionContextId).catch(() => {});
      check();
    }
    let pending = flights.get(id);
    if (!pending) {
      pending = (async () => {
        const handle = await broker.register({
          kind: 'session',
          hostSessionId: id,
          expiresAt: new Date(Date.now() + 600000).toISOString(),
        });
        check();
        if (
          closed ||
          g !== generation ||
          current !== stamp() ||
          ctx.sessions?.get(id) !== native
        ) {
          await broker.revoke(handle.executionContextId).catch(() => {});
          return fail();
        }
        sessions.set(id, { stamp: current, handle, native });
        return handle;
      })();
      flights.set(id, pending);
    }
    try {
      return await pending;
    } finally {
      if (flights.get(id) === pending) flights.delete(id);
    }
  }
  async function tool(/** @type {any} */ execution) {
    const agent = execution?.agent,
      native = agent?.session,
      id = native?.header?.id;
    const currentHost = host;
    let background = false;
    const live = () =>
      !closed &&
      currentHost &&
      host === currentHost &&
      typeof id === 'string' &&
      agent?.id === id &&
      native === agent.session &&
      ctx.sessions?.get(id) === native &&
      currentHost.agents?.get(id) === agent &&
      !currentHost.workspaceRegistry?.archivedSessionIds?.includes(id) &&
      (background || !execution.signal?.aborted);
    if (
      !live() ||
      typeof execution.callId !== 'string' ||
      !execution.callId ||
      typeof execution.rootCallId !== 'string' ||
      !execution.rootCallId
    )
      return fail();
    const resolved = await ctx.sessionController.resolveAgent(id);
    if (!live() || resolved.error || resolved.agent !== agent) return fail();
    const parent = await session(id),
      check = broker.state.fence();
    const handle = await broker.register({
      kind: 'tool',
      hostSessionId: id,
      toolCallId: execution.callId,
      rootCallId: execution.rootCallId,
      parentExecutionContextId: parent.executionContextId,
      expiresAt: new Date(Date.now() + 600000).toISOString(),
    });
    check();
    if (!live()) {
      await broker.revoke(handle.executionContextId).catch(() => {});
      return fail();
    }
    const abort = () => {
      void retireTool(handle.executionContextId);
    };
    tools.set(handle.executionContextId, {
      handle,
      alive: live,
      dispose: () => execution.signal?.removeEventListener('abort', abort),
      retain() {
        background = true;
        execution.signal?.removeEventListener('abort', abort);
      },
    });
    execution.signal?.addEventListener('abort', abort, { once: true });
    if (execution.signal?.aborted) {
      await retireTool(handle.executionContextId);
      return fail();
    }
    return handle;
  }
  const timer = setInterval(() => {
    for (const [id, row] of tools)
      if (!row.alive() || Date.parse(row.handle.expiresAt) <= Date.now())
        void retireTool(id);
    for (const [id, row] of sessions)
      if (
        ctx.sessions?.get(id) !== row.native ||
        Date.parse(row.handle.expiresAt) <= Date.now()
      ) {
        sessions.delete(id);
        void broker.revoke(row.handle.executionContextId).catch(() => {});
      }
  }, 1000);
  timer.unref();
  return {
    session,
    tool,
    finish: retireTool,
    valid(/** @type {string} */ id) {
      const row = tools.get(id);
      const current =
        !!row && row.alive() && Date.parse(row.handle.expiresAt) > Date.now();
      if (!current && row) void retireTool(id);
      return current;
    },
    retain(/** @type {string} */ id) {
      const row = tools.get(id);
      if (!row || !row.alive()) return false;
      row.retain();
      return true;
    },
    invalidate,
    setHost(/** @type {any} */ value) {
      if (host && host !== value) invalidate();
      host = value;
    },
    owner(/** @type {string} */ id) {
      const row = sessions.get(id);
      return !closed &&
        row &&
        row.stamp === stamp() &&
        ctx.sessions?.get(id) === row.native &&
        Date.parse(row.handle.expiresAt) > Date.now()
        ? (identity()?.ownerId ?? null)
        : null;
    },
    close() {
      closed = true;
      clearInterval(timer);
      host = undefined;
      invalidate();
    },
  };
}
