// @ts-nocheck
import { placementBinding } from '../shared/runtime.js';
import { sameBinding, normalizeView } from './intent.js';

/** Fences internal as well as exported production-factory lookups. These are
 * installation services, not executable validators from browser/config. */
export function createDedicatedNodeBridge({ transport, settings, binding }) {
  sameBinding(binding, binding);
  return {
    async call(operation, body, signal) {
      if (body?.binding) sameBinding(placementBinding(body.binding), binding);
      // Compensation may Finish our pinned old task after replacement. It
      // cannot start work, refresh materials or publish an old response.
      if (operation !== 'finishTask') settings();
      const result = await transport.call(operation, body, signal);
      if (result?.binding)
        sameBinding(placementBinding(result.binding), binding);
      if (result?.view)
        sameBinding(normalizeView(result.view).binding, binding);
      if (operation !== 'finishTask') settings();
      return result;
    },
    stream(operation, body, signal) {
      settings();
      return transport.stream(operation, body, signal);
    },
    close: () => transport.close(),
  };
}
