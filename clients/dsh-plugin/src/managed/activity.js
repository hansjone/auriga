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
  try {
    return ctx.get?.(name);
  } catch {
    return undefined;
  }
}

/**
 * Collect activity from DSH host services (same sources as oclaw hub-heartbeat).
 * Missing services leave the corresponding metric unknown.
 * @param {object} ctx
 * @param {ReturnType<typeof createActivityProbe>} activity
 * @param {{ browsers?: number, imBots?: 'none' }} [opts]
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
    const termSvc = service(ctx, 'terminals');
    if (termSvc && typeof termSvc.list === 'function') {
      // DSH 0.2.0-rc.2 terminals.list requires an exact Agent owner.
      // Calling list(undefined) silently returns [] even with live terminals.
      if (agentsOk) {
        for (const handle of agents) {
          const agent = handle.agent || handle;
          const terminals = rows(await termSvc.list(agent));
          if (
            terminals.some(
              (terminal) =>
                !['running', 'exited'].includes(terminal?.status?.kind),
            )
          ) {
            throw new TypeError('unknown terminal status');
          }
          openTerminals += terminals.filter(
            (terminal) => terminal.status.kind === 'running',
          ).length;
        }
        terminalsOk = true;
      }
    }
  } catch {
    terminalsOk = false;
  }
  if (terminalsOk) activity.setOpenTerminals(openTerminals);
  else activity.markUnknown('openTerminals');

  let imOk = false;
  let activeImBots = 0;
  try {
    const dshIm = service(ctx, 'dshIm');
    if (!dshIm && opts.imBots === 'none') {
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
 * @param {{ intervalMs?: number, getBrowsers?: () => number, imBots?: 'none' }} [opts]
 */
export function installActivityBridge(ctx, activity, opts = {}) {
  activity.markAllUnknown();
  const intervalMs = opts.intervalMs ?? 2_000;
  let timer;

  const tick = () => {
    void refreshActivityFromHost(ctx, activity, {
      browsers: opts.getBrowsers?.(),
      imBots: opts.imBots,
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

  return () => {
    clearInterval(timer);
  };
}
