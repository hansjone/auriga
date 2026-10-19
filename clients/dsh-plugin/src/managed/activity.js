// @ts-nocheck
/**
 * In-process activity counters for health and heartbeat.
 */

/**
 * @param {{ now?: () => Date }} [opts]
 */
export function createActivityProbe(opts = {}) {
  const now = opts.now ?? (() => new Date());
  let runningTurns = 0;
  let openTerminals = 0;
  let activeImBots = 0;
  let connectedClients = 0;
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
   * @param {'runningTurns'|'openTerminals'|'activeImBots'|'connectedClients'} field
   * @param {number} value
   */
  const set = (field, value) => {
    const n = Math.max(0, Math.floor(Number(value) || 0));
    if (field === 'runningTurns') runningTurns = n;
    else if (field === 'openTerminals') openTerminals = n;
    else if (field === 'activeImBots') activeImBots = n;
    else if (field === 'connectedClients') connectedClients = n;
    bumpInteraction();
    notify();
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
    /** @param {Partial<{ runningTurns: number, openTerminals: number, activeImBots: number, connectedClients: number }>} patch */
    update(patch) {
      if (patch.runningTurns != null) runningTurns = Math.max(0, Math.floor(patch.runningTurns));
      if (patch.openTerminals != null)
        openTerminals = Math.max(0, Math.floor(patch.openTerminals));
      if (patch.activeImBots != null)
        activeImBots = Math.max(0, Math.floor(patch.activeImBots));
      if (patch.connectedClients != null)
        connectedClients = Math.max(0, Math.floor(patch.connectedClients));
      bumpInteraction();
      notify();
    },
    /** @param {() => void} fn */
    onChange(fn) {
      listeners.add(fn);
      return () => listeners.delete(fn);
    },
    snapshot() {
      return {
        runningTurns,
        openTerminals,
        activeImBots,
        connectedClients,
        lastInteractionAt: lastInteractionAt
          ? lastInteractionAt.toISOString()
          : undefined,
      };
    },
  };
}
