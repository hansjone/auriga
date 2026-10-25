import { isUint64String } from '../address.js';
import { createNativeGate, incompatible } from './runtime.js';

/** In-process tool callback only. Never expose this as a JSON/HTTP/IPC resolver.
 * The broker must bind its IPC peer to the capability before any serialization.
 * Human intent, rebinding and credentials are deliberately not capabilities here.
 * @param {{ctx: import('./types.js').NativeContext, security: import('./types.js').NativeSecurity, lookupBinding: (sessionId: string) => Promise<import('./types.js').SourceBinding|null>}} options */
export function createNativeSourceResolver({ ctx, security, lookupBinding }) {
  const ready = createNativeGate(security);
  if (typeof lookupBinding !== 'function') throw incompatible();
  /** @param {unknown} execution */
  return async (execution) => {
    try {
      await ready();
      const agent = /** @type {{agent?: import('./types.js').Agent}|null} */ (
        execution
      )?.agent;
      const id = agent?.session?.id;
      const live = () =>
        !!agent &&
        !!id &&
        ctx.agents.get(id) === agent &&
        ctx.sessions.get(id) === agent.session &&
        agent.id === id &&
        agent.session.header.id === id &&
        agent.session.header.origin !== 'subagent' &&
        !ctx.workspaceRegistry.archivedSessionIds.includes(id);
      if (!live() || !id) throw incompatible();
      const native = await ctx.sessionController.resolveAgent(id);
      if (native.error || native.agent !== agent || !live())
        throw incompatible();
      const binding = await lookupBinding(id);
      await ready();
      if (
        !live() ||
        !binding ||
        binding.hostSessionId !== id ||
        typeof binding.cwd !== 'string' ||
        !binding.cwd ||
        binding.cwd !== agent?.session.header.cwd ||
        binding.state !== 'bound' ||
        binding.lifecycle !== 'open' ||
        binding.kind !== 'session' ||
        !isUint64String(binding.local) ||
        binding.local === '0' ||
        !binding.bindingId
      )
        throw incompatible();
      // Only the local source reference is released; no source_binding token,
      // credential, human intent, arbitrary session history or whole ledger.
      return Object.freeze({
        local: binding.local,
        bindingId: binding.bindingId,
        hostSessionId: id,
        cwd: binding.cwd,
      });
    } catch {
      throw incompatible();
    }
  };
}
