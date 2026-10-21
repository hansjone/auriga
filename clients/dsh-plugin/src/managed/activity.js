// @ts-nocheck
/**
 * In-process activity counters for heartbeat.
 * Each metric can be known (wired to a DSH source) or unknown (must not look like idle 0).
 */

/**
 * @param {{ now?: () => Date }} [opts]
 */
export function createActivityProbe(opts = {}) {
  const now = opts.now ?? (() => new Date());
  /** @type {Record<string, number | null>} */
  const values = {
    runningTurns: null,
    openTerminals: null,
    activeImBots: null,
    connectedClients: null,
  };
  /** @type {Date | undefined} */
  let lastInteractionAt;
  /** @type {Set<() => void>} */
  const listeners = new Set();

  const bumpInteraction = () => {
    lastInteractionAt = now();
  };

  const notify = () => {
    for (const fn of listeners) {
      try {
        fn();
      } catch {
        /* ignore */
      }
    }
  };

  /**
   * @param {keyof typeof values} field
   * @param {number | null | undefined} value null/undefined → unknown
   */
  const set = (field, value) => {
    /** @type {number | null} */
    let next;
    if (!Number.isSafeInteger(value) || value < 0) {
      next = null;
    } else {
      next = Math.max(0, Math.floor(Number(value)));
    }
    const prev = values[field];
    if (prev === next) return;
    values[field] = next;
    // Counts describe liveness, not a user action. Neither initialization,
    // polling nor positive count changes can invent a user interaction.
    notify();
  };

  /** Explicit user-facing interaction (not used by host refresh). */
  const touchInteraction = () => {
    bumpInteraction();
  };

  return {
    setRunningTurns(n) {
      set('runningTurns', n);
    },
    setOpenTerminals(n) {
      set('openTerminals', n);
    },
    setActiveImBots(n) {
      set('activeImBots', n);
    },
    setConnectedClients(n) {
      set('connectedClients', n);
    },
    markUnknown(field) {
      if (field in values)
        set(/** @type {keyof typeof values} */ (field), null);
    },
    markAllUnknown() {
      for (const k of Object.keys(values))
        set(/** @type {keyof typeof values} */ (k), null);
    },
    /**
     * @param {Partial<Record<keyof typeof values, number | null | undefined>>} patch
     */
    update(patch) {
      for (const [k, v] of Object.entries(patch)) {
        if (k in values) set(/** @type {keyof typeof values} */ (k), v);
      }
    },
    /** @param {() => void} fn */
    onChange(fn) {
      listeners.add(fn);
      return () => listeners.delete(fn);
    },
    touchInteraction,
    snapshot() {
      const known = {
        runningTurns: values.runningTurns != null,
        openTerminals: values.openTerminals != null,
        activeImBots: values.activeImBots != null,
        connectedClients: values.connectedClients != null,
      };
      const allKnown = Object.values(known).every(Boolean);
      return {
        runningTurns: values.runningTurns,
        openTerminals: values.openTerminals,
        activeImBots: values.activeImBots,
        connectedClients: values.connectedClients,
        known,
        allKnown,
        unknownFields: Object.entries(known)
          .filter(([, k]) => !k)
          .map(([name]) => name),
        lastInteractionAt: lastInteractionAt
          ? lastInteractionAt.toISOString()
          : undefined,
      };
    },
  };
}

function rows(value) {
  if (Array.isArray(value)) return value;
  if (value instanceof Map) return [...value.values()];
  if (value && typeof value === 'object') return Object.values(value);
  throw new TypeError('invalid host activity collection');
}

function service(ctx, name) {
  // Let each collector's catch distinguish a failed lookup from real absence.
  if (typeof ctx.get !== 'function')
    throw new TypeError('host service lookup unavailable');
  return ctx.get(name);
}

function requireMap(value) {
  if (!(value instanceof Map)) throw new TypeError('unknown terminal registry');
  return value;
}

// Pinned DSH 0.2.0-rc.2 browser terminals outlive online Agents. Reading only
// agents.list() misses those owners, and list(sessionId) omits pending spawns.
function browserTerminalCount(controller) {
  if (
    typeof controller.list !== 'function' ||
    typeof controller.lifetime?.signal.aborted !== 'boolean' ||
    controller.lifetime.signal.aborted
  )
    throw new TypeError('browser terminal service unavailable');
  let count = 0;
  for (const [sessionId, owner] of requireMap(controller.owners)) {
    const terminals = requireMap(owner?.terminals);
    if (
      typeof sessionId !== 'string' ||
      requireMap(owner.pending).size ||
      requireMap(owner.allocations).size ||
      !(owner.closedIds instanceof Set) ||
      typeof owner.lifetime?.signal.aborted !== 'boolean' ||
      owner.lifetime.signal.aborted ||
      owner.cleanup !== undefined
    )
      throw new TypeError('browser terminal cleanup or allocation unsettled');
    const listed = controller.list(sessionId);
    if (!Array.isArray(listed) || listed.length !== terminals.size)
      throw new TypeError('inconsistent browser terminal list');
    for (const [id, terminal] of terminals) {
      if (
        terminal?.info?.id !== id ||
        !listed.includes(terminal.info) ||
        terminal.closing !== undefined ||
        terminal.retention?.closing === true ||
        terminal.retention?.disposed === true ||
        owner.closedIds.has(id) ||
        terminal.info.state !== 'running'
      )
        // Exited/failed metadata does not prove the owned process range was
        // cleaned. Keep protection until the controller removes its record.
        throw new TypeError('browser terminal process state unconfirmed');
      count++;
    }
  }
  return count;
}

function modelTerminalCount(terminals, agents) {
  if (
    typeof terminals.list !== 'function' ||
    terminals.disposing !== false ||
    !(terminals.disposedOwners instanceof WeakSet)
  )
    throw new TypeError('model terminal service unavailable');
  const owners = new Map(agents.map((agent) => [agent, new Set()]));
  for (const [owner, detach] of requireMap(terminals.ownerCleanups)) {
    if (!owner || typeof owner !== 'object' || typeof detach !== 'function')
      throw new TypeError('invalid model terminal owner cleanup');
    owners.set(owner, new Set());
  }
  for (const pending of requireMap(terminals.pendingSpawns).values()) {
    if (!(pending instanceof Set) || pending.size)
      throw new TypeError('model terminal allocation unsettled');
  }
  for (const [id, record] of requireMap(terminals.sessions)) {
    if (
      typeof id !== 'string' ||
      record?.id !== id ||
      !record.owner ||
      typeof record.owner !== 'object' ||
      typeof record.session?.status !== 'function' ||
      terminals.disposedOwners.has(record.owner) ||
      record.closing !== undefined
    )
      throw new TypeError('model terminal cleanup unsettled');
    if (!owners.has(record.owner)) owners.set(record.owner, new Set());
    owners.get(record.owner).add(id);
  }
  let count = 0;
  for (const [owner, ids] of owners) {
    // The exact owner comes from the registry, including retained offline
    // owners. Never call list(undefined), which silently returns an empty list.
    const listed = terminals.list(owner);
    if (!Array.isArray(listed) || listed.length !== ids.size)
      throw new TypeError('inconsistent model terminal list');
    for (const terminal of listed) {
      if (
        !ids.delete(terminal?.sessionId) ||
        terminal.status?.kind !== 'running'
      )
        // kill()/owner cleanup can reset record.closing after a failed close.
        // An exited backend record still retained here is not proof of cleanup.
        throw new TypeError('unknown model terminal status');
      count++;
    }
  }
  return count;
}

/**
 * Collect activity from DSH host services (same sources as oclaw hub-heartbeat).
 * Missing services leave the corresponding metric unknown.
 * @param {object} ctx
 * @param {ReturnType<typeof createActivityProbe>} activity
 * @param {{ browsers?: number, imBots?: 'none', terminalSources?: 'browser-only' }} [opts]
 *   imBots 'none' is an explicit declaration that no IM service exists.
 */
export async function refreshActivityFromHost(ctx, activity, opts = {}) {
  let agents = [];
  let agentsOk = false;
  try {
    const agentsSvc = service(ctx, 'agents');
    if (agentsSvc && typeof agentsSvc.list === 'function') {
      agents = rows(await agentsSvc.list());
      if (
        agents.some((handle) => {
          const agent = handle?.agent || handle;
          return (
            !agent ||
            (!['idle', 'running'].includes(agent.status) &&
              typeof agent.isRunning !== 'boolean' &&
              !['idle', 'running'].includes(agent.turn?.status))
          );
        })
      )
        throw new TypeError('unknown agent status');
      agentsOk = true;
    }
  } catch {
    agents = [];
    agentsOk = false;
  }

  if (agentsOk) {
    const runningTurns = agents.filter((handle) => {
      const agent = handle.agent || handle;
      return (
        agent.isRunning === true ||
        agent.status === 'running' ||
        agent.turn?.status === 'running'
      );
    }).length;
    activity.setRunningTurns(runningTurns);
  } else {
    activity.markUnknown('runningTurns');
  }

  let terminalsOk = false;
  let openTerminals = 0;
  try {
    if (!agentsOk) throw new TypeError('terminal owners unavailable');
    const browser = service(ctx, 'terminalController');
    const models = new Set();
    const globalModel = service(ctx, 'terminals');
    if (globalModel != null) models.add(globalModel);
    for (const handle of agents) {
      const agent = handle.agent || handle;
      const scoped = service(agent.ctx, 'terminals');
      if (scoped != null) models.add(scoped);
    }
    if (browser != null) openTerminals += browserTerminalCount(browser);
    if (opts.terminalSources === 'browser-only' && browser == null)
      throw new TypeError('declared browser terminal source unavailable');
    if (!models.size && opts.terminalSources !== 'browser-only')
      throw new TypeError('model terminal source unknown');
    const registries = new Set();
    for (const model of models) {
      // Validate every discovered facade, even when it shares a registry.
      const count = modelTerminalCount(
        model,
        agents.map((handle) => handle.agent || handle),
      );
      if (!registries.has(model.sessions)) openTerminals += count;
      registries.add(model.sessions);
    }
    terminalsOk = Number.isSafeInteger(openTerminals);
  } catch {
    terminalsOk = false;
  }
  if (terminalsOk) activity.setOpenTerminals(openTerminals);
  else activity.markUnknown('openTerminals');

  let imOk = false;
  let activeImBots = 0;
  try {
    const dshIm = service(ctx, 'dshIm');
    if (dshIm == null && opts.imBots === 'none') {
      // The rendered config declares this build has no IM service: absence is
      // a known 0. A present-but-failing service below stays unknown.
      imOk = true;
    } else if (dshIm?.listDeliveryCatalog) {
      const catalog = await dshIm.listDeliveryCatalog();
      activeImBots = new Set(
        rows(catalog?.bots || catalog)
          .filter((row) => row.enabled !== false)
          .map((row) => row.botId || row.id)
          .filter(Boolean),
      ).size;
      imOk = true;
    }
  } catch {
    imOk = false;
  }
  if (imOk) activity.setActiveImBots(activeImBots);
  else activity.markUnknown('activeImBots');

  // Match oclaw hub-member: browsers come from an external callback
  // (WebSocket upgrade count), not from guessing connection.clientCount.
  if (Number.isSafeInteger(opts.browsers) && opts.browsers >= 0) {
    activity.setConnectedClients(opts.browsers);
  } else {
    activity.markUnknown('connectedClients');
  }
}

/**
 * Wire periodic refresh + optional inject hooks. Returns disposer.
 * @param {object} ctx
 * @param {ReturnType<typeof createActivityProbe>} activity
 * @param {{ intervalMs?: number, getBrowsers?: () => number, imBots?: 'none', terminalSources?: 'browser-only' }} [opts]
 */
export function installActivityBridge(ctx, activity, opts = {}) {
  activity.markAllUnknown();
  const intervalMs = opts.intervalMs ?? 2_000;
  let timer;

  const tick = () => {
    void refreshActivityFromHost(ctx, activity, {
      browsers: opts.getBrowsers?.(),
      imBots: opts.imBots,
      terminalSources: opts.terminalSources,
    }).catch(() => {
      /* keep previous / unknown */
    });
  };

  tick();
  timer = setInterval(tick, intervalMs);
  if (typeof timer.unref === 'function') timer.unref();

  // Best-effort: refresh when agents service appears.
  try {
    ctx.inject?.(['agents'], () => tick());
  } catch {
    /* optional */
  }
  try {
    ctx.inject?.(['terminals'], () => tick());
  } catch {
    /* optional */
  }
  try {
    ctx.inject?.(['terminalController'], () => tick());
  } catch {
    /* optional */
  }

  return () => {
    clearInterval(timer);
  };
}
