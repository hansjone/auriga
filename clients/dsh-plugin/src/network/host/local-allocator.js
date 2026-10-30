import { makeAddress } from './address.js';
import { CODES, HostSeamError } from './errors.js';
import { createKeyedMutex } from './keyed-mutex.js';
import { reserveLocal, registerReservedLocal } from './ledger.js';

/** @type {WeakMap<import('./ledger.js').Ledger, ReturnType<typeof createKeyedMutex>>} */
const nodeQueues = new WeakMap();

/**
 * The durable queue contains only the reserved head. Later callers wait
 * without consuming a number. A restart settles that head before reserving
 * anything else, including a cancelled/failed contact's burned allocation.
 * @param {{ ledger: import('./ledger.js').Ledger, registerLocal: import('./adapter.js').NetworkPort['registerLocal'] }} options
 */
export function createLocalAllocator({ ledger, registerLocal }) {
  let queue = nodeQueues.get(ledger);
  if (!queue) {
    queue = createKeyedMutex();
    nodeQueues.set(ledger, queue);
  }
  const nodeQueue = queue;

  async function recoverHead() {
    ledger.assertAvailable();
    if (ledger.peek().identityConflict)
      throw new HostSeamError(CODES.recoveryConflict);
    const head = ledger.peek().localQueue[0];
    if (head !== undefined)
      await registerReservedLocal(ledger, head, registerLocal);
  }

  return {
    /** Reconcile on reconnect before exposing sources or accepting new work. */
    recover() {
      return nodeQueue.run('allocate', recoverHead);
    },
    /** @param {{ opId: string, purpose: string, mappingDigest: string, addressType: 'session'|'subagent' }} input */
    allocate(input) {
      return nodeQueue.run('allocate', async () => {
        const existing =
          ledger.peek().locals[ledger.peek().registrations[input.opId]];
        if (
          existing &&
          (existing.addressType !== input.addressType ||
            existing.mappingDigest !== input.mappingDigest ||
            existing.purpose !== input.purpose)
        )
          throw new HostSeamError(
            CODES.localAllocationConflict,
            'allocation intent changed',
          );
        await recoverHead();
        await reserveLocal(ledger, input);
        return registerReservedLocal(ledger, input.opId, registerLocal);
      });
    },
    /** Only a durable, registered, live binding can be used as a source.
     * @param {string} local */
    addressFor(local) {
      ledger.assertAvailable();
      const s = ledger.peek();
      if (s.identityConflict) throw new HostSeamError(CODES.recoveryConflict);
      if (local === '0') return makeAddress(ledger.identity, local, 'system');
      const reg = s.locals[local];
      const binding = s.bindings[local];
      if (
        reg?.state !== 'registered' ||
        !reg.registrationId ||
        binding?.state !== 'bound' ||
        binding.lifecycle !== 'open'
      )
        throw new HostSeamError(CODES.targetUnavailable);
      if (reg.addressType !== binding.kind)
        throw new HostSeamError(CODES.recoveryConflict);
      return makeAddress(ledger.identity, local, reg.addressType);
    },
  };
}
