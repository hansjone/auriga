import { networkTiming } from '../timing.js';
import { randomBytes } from 'node:crypto';
import {
  assertReadyResult,
  readyCandidate,
  decodeJson,
  encodeReady,
} from './frames.js';
import { SessionError } from './errors.js';
import { verifyEnvelopeWithRefresh } from '../trust/verifier.js';
import { createReadyRetry, readyRetryError } from './ready-retry.js';

/** @type {WeakMap<object, symbol>} */
const attempts = new WeakMap();
/** @param {any} value */
function freeze(value) {
  if (value && typeof value === 'object') {
    for (const child of Object.values(value)) freeze(child);
    Object.freeze(value);
  }
  return value;
}

/** Explicit internal first READY only. The caller has already checked HTTPS
 * register tenant/device against JWT, realm against installation, and owns this
 * real device's freshly ticketed transport and root-approved trust. No token,
 * ledger, host or business port belongs here. Output is NOT initialize authority.
 * @param {{
 * registration: {tenant:string,realm:string,node:string,device:string,created:true},
 * transport: import('./memory-pipe.js').TransportLike,
 * trust: Awaited<ReturnType<import('../trust/store.js').openTrustStore>>,
 * signal: AbortSignal, timeoutMs?:number, now?:()=>number
 * }} options
 */
export async function bootstrapReady({
  registration,
  transport,
  trust,
  signal,
  timeoutMs = networkTiming.operationMs,
  now = Date.now,
}) {
  const candidateRegistration = { ...registration };
  const positive = (/** @type {unknown} */ v) =>
    typeof v === 'string' &&
    /^[1-9][0-9]{0,19}$/.test(v) &&
    BigInt(v) <= 18446744073709551615n;
  if (
    candidateRegistration.created !== true ||
    !positive(candidateRegistration.realm) ||
    !positive(candidateRegistration.node) ||
    ![candidateRegistration.tenant, candidateRegistration.device].every(
      (v) =>
        typeof v === 'string' && /^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$/.test(v),
    ) ||
    !signal ||
    !Number.isFinite(timeoutMs) ||
    timeoutMs <= 0 ||
    timeoutMs > 2147483647
  )
    throw new SessionError(
      'network.protocol_error',
      'trusted first registration required',
    );
  if (attempts.has(transport))
    throw new SessionError('invalid_state', 'READY already in flight');
  const attempt = Symbol('bootstrap-ready');
  const transportEpoch = transport.epoch;
  const keysetVersion = trust.version;
  const requestId = `req-ready-${randomBytes(16).toString('hex')}`;
  const channelNonce = `nonce-${randomBytes(16).toString('hex')}`;
  const bootId = `boot-${randomBytes(16).toString('hex')}`;
  attempts.set(transport, attempt);
  let active = true,
    verifying = false;
  /** @type {(()=>void)|undefined} */
  let unsubscribe;
  /** @type {ReturnType<typeof setTimeout>|undefined} */
  let timer;
  /** @type {ReturnType<typeof createReadyRetry>|undefined} */
  let retry;
  function current() {
    if (
      !active ||
      signal.aborted ||
      attempts.get(transport) !== attempt ||
      transport.epoch !== transportEpoch ||
      transport.state !== 'ready'
    )
      throw new SessionError('stopped', 'bootstrap READY interrupted');
  }
  try {
    current();
    const output = await new Promise((resolve, reject) => {
      /** @param {unknown} error */
      function fail(error) {
        reject(error);
      }
      const payload = encodeReady({
        requestId,
        channelNonce,
        bootId,
        keysetVersion,
        resumeCursor: '0',
        localHighWater: '0',
        registrationRevision: '0',
        receiptCheckpoint: {
          checkpoint_seq: '0',
          checkpoint_revision: '0',
          summary_digest: 'sha256:' + '0'.repeat(64),
        },
        pendingRegistration: null,
      });
      const attemptRetry = createReadyRetry(
        () => transport.send(payload),
        current,
        fail,
        timeoutMs,
      );
      retry = attemptRetry;
      const abort = () =>
        fail(new SessionError('stopped', 'bootstrap READY aborted'));
      signal.addEventListener('abort', abort, { once: true });
      // Cleanup is shared by send/setup exceptions, abort, timeout and verification.
      const removeAbort = () => signal.removeEventListener('abort', abort);
      unsubscribe = removeAbort;
      timer = setTimeout(
        () => fail(new SessionError('timeout', 'bootstrap READY timeout')),
        timeoutMs,
      );
      /** @param {Uint8Array} payload @param {number} receivedEpoch */
      async function receive(payload, receivedEpoch) {
        try {
          current();
          if (receivedEpoch !== transportEpoch)
            throw new SessionError('stopped', 'bootstrap connection changed');
          if (verifying) return;
          const result = decodeJson(payload);
          // A late response from another attempt cannot finish this one.
          if (result?.request_id && result.request_id !== requestId) return;
          verifying = true;
          /** @type {typeof trust} */
          const guardedTrust = {
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
          };
          const retryable = await readyRetryError(result, {
            trust: guardedTrust,
            context: {
              realm: candidateRegistration.realm,
              node: candidateRegistration.node,
              nodeIncarnation: 'unbound',
              epoch: '0',
              requestId,
              channelNonce,
            },
            now,
          });
          current();
          if (retryable) {
            verifying = false;
            attemptRetry.schedule();
            return;
          }
          if (result?.type !== 'ready_result')
            throw new SessionError('ready_failed', 'expected READY_RESULT');
          const candidate = readyCandidate(result);
          const context = {
            realm: candidateRegistration.realm,
            node: candidateRegistration.node,
            ...candidate,
            requestId,
            channelNonce,
          };
          current();
          await verifyEnvelopeWithRefresh(result, {
            trust: guardedTrust,
            context,
            now,
          });
          current();
          assertReadyResult(result, context);
          const identity = {
            tenant: candidateRegistration.tenant,
            realm: candidateRegistration.realm,
            node: candidateRegistration.node,
            nodeIncarnation: candidate.nodeIncarnation,
          };
          const output = freeze({ identity, readyResult: result });
          current();
          resolve(output);
        } catch (error) {
          fail(error);
        }
      }
      try {
        const off = transport.onMessage((payload, epoch) => {
          void receive(payload, epoch);
        });
        unsubscribe = () => {
          removeAbort();
          off();
        };
        current();
        void transport.send(payload).catch(fail);
      } catch (error) {
        fail(error);
      }
    });
    current();
    return output;
  } finally {
    active = false;
    retry?.close();
    clearTimeout(timer);
    try {
      unsubscribe?.();
    } finally {
      if (attempts.get(transport) === attempt) attempts.delete(transport);
    }
  }
}
