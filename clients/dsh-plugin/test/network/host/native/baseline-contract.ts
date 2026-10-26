// Compile-only consumer of public package exports; no internal paths.
import type { Context } from '@deepseek-ai/cordis';
import type { Agent } from '@deepseek-ai/dsh-agent';
import type {
  SessionId,
  Session,
  SessionHeader,
} from '@deepseek-ai/dsh-session';
import type {} from '@deepseek-ai/dsh-api-session-controller';
import type {} from '@deepseek-ai/dsh-agent-preset-registry';
import type {} from '@deepseek-ai/dsh-session-persistence';
import type {} from '@deepseek-ai/dsh-session-projection';
import type {} from '@deepseek-ai/dsh-workspace';

declare const ctx: Context;
declare const id: SessionId;
declare const session: Session;
declare const agent: Agent;
declare const request: Parameters<typeof ctx.sessionController.prompt>[0];

// Adapter-selected API shapes must remain assignable from the public host API.
const checked: [
  Promise<{ meta: { id: string; cwd?: string }; events: readonly unknown[] }>,
  Promise<
    { agent: Agent; error?: undefined } | { agent?: undefined; error: unknown }
  >,
  Promise<{ sessionId: string; agentPreset?: string }>,
  Promise<{ accepted: true }>,
  Promise<boolean>,
  Promise<void>,
  symbol,
  Promise<{ header: { id: string } } | undefined>,
  Session | undefined,
  Agent | undefined,
  boolean,
  readonly string[],
  Promise<{ id: string; broken?: unknown }>,
  string | null | undefined,
  readonly { source?: { kind: string; rpcId?: string } }[],
  readonly { source?: { kind: string; rpcId?: string } }[],
] = [
  ctx.sessionController.inspect(id),
  ctx.sessionController.resolveAgent(id),
  ctx.sessionController.create({
    sessionId: id,
    cwd: '/work/project',
    agentPreset: 'standard',
  }),
  ctx.sessionController.prompt(request, new AbortController().signal),
  ctx.sessions.flush(session),
  ctx.sessionPersistence.flush(),
  ctx.sessionPersistence.identity,
  ctx.sessionPersistence.stat(id),
  ctx.sessions.get(id),
  ctx.agents.get(id),
  ctx.agents.isOwnedBy(id, agent),
  ctx.workspaceRegistry.archivedSessionIds,
  ctx.agentPresets.resolve(undefined),
  ctx.sessionProjections.stateOf(session, 'agentPreset'),
  agent.inbox.nextTurn,
  agent.inbox.nextStep,
];

async function coldInbox() {
  const projection = await ctx.sessionController.projections(
    { sessionId: id },
    new AbortController().signal,
  );
  return projection?.values.inbox?.['next-turn'];
}
async function persistedPrefix() {
  const reader = await ctx.sessionPersistence.open(id, 'read');
  try {
    const prefix: readonly unknown[] = (await reader.read()).events;
    return { id: reader.header.id, cwd: reader.header.cwd, prefix };
  } finally {
    await reader.close();
  }
}
void checked;
void coldInbox;
void persistedPrefix;

// Origin is a coarse product category; parentSession only records fork lineage.
async function inspectedOrigin() {
  const inspected = await ctx.sessionController.inspect(id);
  const header: SessionHeader = inspected.meta;
  const origin: 'subagent' | undefined = header.origin;
  const parent: string | undefined = header.parentSession;
  return { origin, parent };
}
void inspectedOrigin;
