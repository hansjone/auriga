import { decodeJson, encodeJson } from '../session/frames.js';
import { verifyEnvelopeWithRefresh } from '../trust/verifier.js';
import { assertSourceResult } from './frames.js';

/** One authenticated RPC over the existing mailbox subjects. The controller
 * owns the two per-node lanes; this layer never invents business operations.
 * @param {{transport:import('../session/memory-pipe.js').TransportLike,trust:Awaited<ReturnType<typeof import('../trust/store.js').openTrustStore>>,identity:import('../host/ledger.js').LedgerIdentity,ready:{epoch:string,channelNonce:string,bootId:string},signal:AbortSignal,assertCurrent:()=>void,now:()=>number}} options */
export function connectionSourceClient({
  transport,
  trust,
  identity,
  ready,
  signal,
  assertCurrent,
  now,
}) {
  const transportEpoch = transport.epoch;
  /** @param {import('../../generated/network/SourceControl.js').SourceRegister | import('../../generated/network/SourceControl.js').SourceGet | import('../../generated/network/SourceControl.js').SourceRevoke} request @param {AbortSignal} attempt */
  return async (request, attempt) => {
    let finished = false,
      verifying = false;
    const current = () => {
      signal.throwIfAborted();
      attempt.throwIfAborted();
      assertCurrent();
      trust.assertUsable();
      if (
        finished ||
        transport.epoch !== transportEpoch ||
        transport.state !== 'ready' ||
        request.epoch !== ready.epoch ||
        request.boot_id !== ready.bootId
      )
        throw new Error('source connection changed');
    };
    current();
    let off = () => {},
      timer;
    /** @type {()=>void} */ let abort = () => {};
    try {
      return await new Promise((resolve, reject) => {
        abort = () => reject(new Error('source interrupted'));
        signal.addEventListener('abort', abort, { once: true });
        attempt.addEventListener('abort', abort, { once: true });
        timer = setTimeout(() => reject(new Error('source timeout')), 30000);
        off = transport.onMessage((bytes, receivedEpoch) => {
          void (async () => {
            if (finished || verifying || receivedEpoch !== transportEpoch)
              return;
            current();
            const result = decodeJson(bytes);
            if (result.request_id !== request.request_id) return;
            verifying = true;
            await verifyEnvelopeWithRefresh(result, {
              trust: {
                ...trust,
                key(...args) {
                  current();
                  return trust.key(...args);
                },
                async refreshFor(...args) {
                  current();
                  await trust.refreshFor(...args);
                  current();
                },
              },
              context: {
                ...identity,
                epoch: ready.epoch,
                channelNonce: ready.channelNonce,
                requestId: request.request_id,
              },
              now,
            });
            current();
            assertSourceResult(result, request, now());
            resolve(result);
          })().catch(reject);
        });
        current();
        void transport.send(encodeJson(request)).catch(reject);
      });
    } finally {
      finished = true;
      off();
      clearTimeout(timer);
      signal.removeEventListener('abort', abort);
      attempt.removeEventListener('abort', abort);
    }
  };
}
