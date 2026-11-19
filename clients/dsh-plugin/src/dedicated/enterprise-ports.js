// @ts-nocheck
import { fail } from '../shared/protocol.js';

/** A job stop fences helpers before its retained budget can be released. The
 * helper capability is mapped by the real production port, never body IDs. */
export function createDedicatedEnterprisePorts(ports, environment) {
  if (typeof environment?.helperContext !== 'function')
    fail('common.unavailable');
  return {
    ...ports,
    async refreshCapability(token, sessionId, scope) {
      await environment.helperContext(sessionId, { token });
      const result = await ports.refreshCapability(token, sessionId, scope);
      await environment.helperContext(sessionId, { token: result });
      return result;
    },
    async credentials(binding, claims) {
      await environment.helperContext(binding.sessionId, {
        taskId: claims.taskId,
      });
      const result = await ports.credentials(binding, claims);
      await environment.helperContext(binding.sessionId, {
        taskId: claims.taskId,
      });
      return result;
    },
    async outbound(request, claims, binding, token) {
      const context = await environment.helperContext(binding.sessionId, {
        token,
      });
      const result = await ports.outbound(
        {
          ...request,
          signal: globalThis.AbortSignal.any(
            [request.signal, context.signal].filter(Boolean),
          ),
        },
        claims,
        binding,
        token,
      );
      await environment.helperContext(binding.sessionId, { token });
      return result;
    },
  };
}
