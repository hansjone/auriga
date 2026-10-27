import { SessionError } from './errors.js';
import { performance } from 'node:perf_hooks';
import { verifyEnvelopeWithRefresh } from '../trust/verifier.js';

/** This authenticated refusal is only a retry hint, never an identity/epoch.
 * @param {any} frame
 * @param {Parameters<typeof verifyEnvelopeWithRefresh>[1]} options */
export async function readyRetryError(frame, options) {
  if (
    frame.type !== 'error' ||
    frame.code !== 'network.overloaded' ||
    frame.scope !== 'request' ||
    frame.retryable !== true
  )
    return false;
  await verifyEnvelopeWithRefresh(frame, {
    ...options,
    context: { ...options.context, nodeIncarnation: 'unbound', epoch: '0' },
  });
  return true;
}

/** Resend the captured READY only; its caller still owns the original 5s timer.
 * At most five total sends, 500ms apart after authenticated overload refusals.
 * @param {()=>Promise<void>} send @param {()=>void} current
 * @param {(error:unknown)=>void} fail @param {number} [timeoutMs] */
export function createReadyRetry(send, current, fail, timeoutMs = 5000) {
  const deadline = performance.now() + timeoutMs;
  let sends = 1,
    closed = false;
  /** @type {ReturnType<typeof setTimeout>|undefined} */
  let timer;
  function check() {
    current();
    if (performance.now() >= deadline)
      throw new SessionError('timeout', 'READY timeout');
  }
  return {
    schedule() {
      check();
      if (closed || timer) return;
      if (sends >= 5)
        throw new SessionError('ready_failed', 'READY retry budget exhausted');
      timer = setTimeout(() => {
        timer = undefined;
        if (closed) return;
        try {
          check();
          sends++;
          void send().catch(fail);
        } catch (error) {
          fail(error);
        }
      }, 500);
    },
    close() {
      closed = true;
      clearTimeout(timer);
    },
  };
}
