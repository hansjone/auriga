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
    if (value == null || Number.isNaN(Number(value))) {
      next = null;
    } else {
      next = Math.max(0, Math.floor(Number(value)));
    }
    const prev = values[field];
    if (prev === next) return;
    values[field] = next;
    // Only real count changes bump idle clock; refreshes with the same
    // numbers must not. Going idle (→0 / unknown) keeps the prior stamp
    // so the platform can reclaim; positive activity refreshes it.
    if (next != null && next > 0) bumpInteraction();
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
        // Contract requires integers ≥ 0; unknown stays 0 but flagged via known/problems.
        runningTurns: values.runningTurns ?? 0,
        openTerminals: values.openTerminals ?? 0,
        activeImBots: values.activeImBots ?? 0,
        connectedClients: values.connectedClients ?? 0,
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
  return [];
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
 * @param {{ browsers?: number }} [opts]
 */
export async function refreshActivityFromHost(ctx, activity, opts = {}) {
  let agents = [];
  let agentsOk = false;
  try {
    const agentsSvc = service(ctx, 'agents');
    if (agentsSvc && typeof agentsSvc.list === 'function') {
      agents = rows(agentsSvc.list());
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
      terminalsOk = true;
      if (agentsOk && agents.length) {
        openTerminals = agents.reduce((n, handle) => {
          const agent = handle.agent || handle;
          try {
            return n + rows(termSvc.list(agent)).length;
          } catch {
            return n;
          }
        }, 0);
      } else {
        openTerminals = rows(termSvc.list()).length;
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
    if (dshIm?.listDeliveryCatalog) {
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
  if (typeof opts.browsers === 'number') {
    activity.setConnectedClients(opts.browsers);
  } else {
    activity.markUnknown('connectedClients');
  }
}

/**
 * Wire periodic refresh + optional inject hooks. Returns disposer.
 * @param {object} ctx
 * @param {ReturnType<typeof createActivityProbe>} activity
 * @param {{ intervalMs?: number, getBrowsers?: () => number }} [opts]
 */
export function installActivityBridge(ctx, activity, opts = {}) {
  activity.markAllUnknown();
  const intervalMs = opts.intervalMs ?? 2_000;
  let timer;

  const tick = () => {
    void refreshActivityFromHost(ctx, activity, {
      browsers: opts.getBrowsers?.(),
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
