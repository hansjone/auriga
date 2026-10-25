/* global structuredClone */
import { PINNED_HOST } from '../../../../src/network/host/native/index.js';

// Contract fixture, not an OS isolation or installed-host compatibility proof.
export function fixture() {
  const pending = new Map();
  const stored = new Map();
  const agents = new Map();
  const sessions = new Map();
  const calls = [];
  const archivedSessionIds = [];
  const state = {
    isolated: true,
    durable: true,
    flush: true,
    flushError: false,
  };
  function attach(id) {
    const record = stored.get(id);
    const session = {
      id,
      header: record.meta,
      snapshotEvents: () => record.events,
    };
    const agent = { id, session };
    sessions.set(id, session);
    agents.set(id, agent);
    return agent;
  }
  function seed(id = 'session-a', meta = {}) {
    stored.set(id, {
      meta: { id, cwd: '/work/project', ...meta },
      events: [],
    });
  }
  const ctx = {
    agents: { get: (id) => agents.get(id) },
    sessions: {
      get: (id) => sessions.get(id),
      async flush(session) {
        calls.push(['flush', session.id]);
        if (state.flushError) throw new Error('private path');
        return state.flush;
      },
    },
    workspaceRegistry: { archivedSessionIds },
    agentPresets: {
      async resolve(id) {
        calls.push(['preset', id]);
        if (id === 'missing') throw new Error('missing');
        return { id, broken: id === 'broken' ? 'bad preset' : undefined };
      },
    },
    sessionPersistence: {
      async stat(id) {
        const record = stored.get(id);
        return record && { header: record.meta };
      },
    },
    sessionController: {
      async inspect(id) {
        calls.push(['inspect', id]);
        if (!stored.has(id))
          throw Object.assign(new Error(), {
            code: 'SESSION_QUERY_SESSION_NOT_FOUND',
          });
        return stored.get(id);
      },
      async resolveAgent(id) {
        calls.push(['resume', id]);
        return { agent: agents.get(id) ?? attach(id) };
      },
      async create(input) {
        calls.push(['create', input]);
        seed(input.sessionId, {
          cwd: input.cwd,
          agentPreset: input.agentPreset,
        });
        attach(input.sessionId);
        return { sessionId: input.sessionId, agentPreset: input.agentPreset };
      },
      async prompt(input, signal) {
        calls.push(['prompt', input, signal]);
        if (!agents.has(input.sessionId)) attach(input.sessionId);
        pending.set(`${input.sessionId}:${input.requestId}`, true);
        return { accepted: true };
      },
    },
  };
  const security = {
    // Port-algorithm fixture authority ONLY. Not a branded native installation.
    async assertReady() {
      if (
        JSON.stringify(await security.inspectRuntime()) !==
          JSON.stringify(PINNED_HOST) ||
        (await security.assertIsolated()) !== undefined
      )
        throw new Error('fixture authority unavailable');
    },
    async hasPromptRequest(id, requestId) {
      return pending.has(`${id}:${requestId}`);
    },
    async inspectRuntime() {
      return structuredClone(PINNED_HOST);
    },
    async assertIsolated() {
      if (!state.isolated) throw new Error('private isolation diagnostics');
    },
    async assertDurableProvider(provider, id) {
      if (
        !state.durable ||
        provider !== ctx.sessionPersistence ||
        !stored.has(id)
      )
        throw new Error('not durable');
    },
  };
  seed();
  return {
    ctx,
    security,
    state,
    calls,
    stored,
    agents,
    sessions,
    attach,
    seed,
    archivedSessionIds,
    pending,
  };
}
