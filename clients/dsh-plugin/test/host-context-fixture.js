import { randomUUID } from 'node:crypto';
import { createHostContexts } from '../src/local/host-contexts.js';
import { createBroker } from '../src/local/broker.js';
import { brokerFixture } from './broker-fixture.js';

export async function nativeContexts(t) {
  const connection = brokerFixture(),
    broker = await createBroker(connection);
  t.after(() => broker.close());
  let who = {
    tenantId: connection.identity.tenantId,
    ownerId: connection.identity.memberId,
    providerId: connection.identity.providerId,
  };
  const session = {
    id: 'host-session',
    header: { id: 'host-session', cwd: '/workspace' },
  };
  const agent = { id: session.id, session };
  const sessions = new Map([[session.id, session]]),
    agents = new Map([[session.id, agent]]);
  const ctx = {
    sessions: { get: (id) => sessions.get(id) },
    agents: { get: (id) => agents.get(id) },
    workspaceRegistry: { archivedSessionIds: [] },
    sessionController: {
      resolveAgent: async (id) => ({ agent: agents.get(id) }),
    },
  };
  const contexts = createHostContexts({ broker, identity: () => who, ctx });
  contexts.setHost(ctx);
  t.after(() => contexts.close());
  const execution = {
    agent,
    callId: randomUUID(),
    rootCallId: randomUUID(),
    signal: new AbortController().signal,
  };
  return {
    broker,
    connection,
    contexts,
    ctx,
    sessions,
    agents,
    session,
    agent,
    execution,
    switchTenant() {
      who = { ...who, tenantId: randomUUID() };
      contexts.invalidate();
    },
  };
}
