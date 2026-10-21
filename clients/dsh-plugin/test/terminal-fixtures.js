// Registry shapes from the pinned DSH 0.2.0-rc.2 implementations. These are
// in-process collector fixtures, not evidence that a real PTY was launched.
export function terminalAgent(id = 'agent-1', get = () => undefined) {
  return { id, status: 'idle', ctx: { get } };
}

export function browserOwner() {
  return {
    terminals: new Map(),
    allocations: new Map(),
    pending: new Map(),
    closedIds: new Set(),
    lifetime: new AbortController(),
  };
}

export function browserController() {
  return {
    lifetime: new AbortController(),
    owners: new Map(),
    list(sessionId) {
      const owner = this.owners.get(sessionId);
      return owner
        ? [...owner.terminals.values(), ...owner.allocations.values()].map(
            (terminal) => terminal.info,
          )
        : [];
    },
  };
}

export function modelTerminals() {
  return {
    sessions: new Map(),
    pendingSpawns: new Map(),
    ownerCleanups: new Map(),
    disposedOwners: new WeakSet(),
    disposing: false,
    list(owner) {
      return [...this.sessions.values()]
        .filter((record) => record.owner === owner)
        .map((record) => ({
          sessionId: record.id,
          status: record.session.status(),
        }));
    },
  };
}

export function modelRecord(owner, id, kind = 'running') {
  return { id, owner, session: { status: () => ({ kind }) } };
}

export function terminalHost(browser, model, agents = []) {
  return {
    get(name) {
      if (name === 'agents') return { list: () => agents };
      if (name === 'terminalController') return browser;
      if (name === 'terminals') return model;
      return undefined;
    },
  };
}
