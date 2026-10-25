import { incompatible } from './runtime.js';

/** Public Agent.Inbox contract, not the loop driver's implementation.
 * dsh-agent exports Inbox with readonly nextTurn/nextStep. History is read
 * separately through sessionController.inspect, never deprecated Session readers.
 * @param {Pick<import('./types.js').Agent, 'inbox'>} agent @param {string} requestId */
export function hasNativePromptRequest(agent, requestId) {
  if (
    !Array.isArray(agent?.inbox?.nextTurn) ||
    !Array.isArray(agent?.inbox?.nextStep)
  )
    throw incompatible();
  /** @param {{source?: {kind: string, rpcId?: string}}} message */
  const matches = (message) =>
    message?.source?.kind === 'user' && message.source.rpcId === requestId;
  return (
    agent.inbox.nextTurn.some(matches) || agent.inbox.nextStep.some(matches)
  );
}

/** Exact native ownership rule: durable fork lineage is not runtime ownership.
 * Uses the public AgentRegistry.isOwnedBy method.
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
