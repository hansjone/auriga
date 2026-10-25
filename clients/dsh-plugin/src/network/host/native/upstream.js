import { incompatible } from './runtime.js';

/** Mirrors @deepseek-ai/dsh-api-session-controller@0.2.0-rc.2
 * lib/index.js:1063-1076, backed by dsh-agent-loop lib/index.js:80-85.
 * Missing/malformed evidence is incompatible, never a definite absence.
 * @param {import('./types.js').Agent} agent @param {string} requestId */
export function hasNativePromptRequest(agent, requestId) {
  if (
    !Array.isArray(agent?.inbox?.nextTurn) ||
    !Array.isArray(agent?.inbox?.nextStep) ||
    typeof agent.session?.snapshotEvents !== 'function'
  )
    throw incompatible();
  /** @param {{source?: {kind: string, rpcId?: string}}} message */
  const matches = (message) =>
    message?.source?.kind === 'user' && message.source.rpcId === requestId;
  if (agent.inbox.nextTurn.some(matches) || agent.inbox.nextStep.some(matches))
    return true;
  const events = agent.session.snapshotEvents();
  if (!Array.isArray(events)) throw incompatible();
  return events.some(
    (event) => event.type === 'user/message' && matches(event.data),
  );
}

/** Exact native ownership rule: durable fork lineage is not runtime ownership.
 * dsh-api-session-controller lib/index.js:125-131; dsh-agent :605-607.
 * @param {import('./types.js').NativeContext} ctx
 * @param {import('./types.js').Header} header
 * @param {import('./types.js').Agent|undefined} agent */
export function isNativeSubagentOwned(ctx, header, agent) {
  if (header.origin === 'subagent') return true;
  if (header.parentSession === undefined || agent === undefined) return false;
  const parent = ctx.agents.get(header.parentSession);
  if (!parent) return false;
  if (typeof ctx.agents.isOwnedBy !== 'function') throw incompatible();
  return ctx.agents.isOwnedBy(agent.id, parent);
}
