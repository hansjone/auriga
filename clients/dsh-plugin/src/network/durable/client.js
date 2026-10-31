import { networkTiming } from '../timing.js';
import { performance } from 'node:perf_hooks';
import { randomUUID } from 'node:crypto';
import { setTimeout as delay } from 'node:timers/promises';
import { decodeJson, encodeJson } from '../session/frames.js';
import { verifyEnvelopeWithRefresh } from '../trust/verifier.js';

export class MailboxError extends Error {
  /** @param {any} frame @param {number} attempts */
  constructor(frame, attempts) {
    super(frame.code ?? 'network.protocol_error');
    this.code = frame.code;
    this.retryable = frame.retryable;
    this.retryAfter = frame.retry_after;
    this.attempts = attempts;
  }
}

/** Authenticated exchanges on the current device connection. Retries retain
 * the exact encoded request, nonce and original deadline, including FETCH.
 * @param {{transport:import('../session/memory-pipe.js').TransportLike,trust:Awaited<ReturnType<typeof import('../trust/store.js').openTrustStore>>,identity:import('../host/ledger.js').LedgerIdentity,ready:{epoch:string,channelNonce:string,bootId:string},signal:AbortSignal,assertCurrent:()=>void,now?:()=>number}} options */
export function createMailboxClient({
  transport,
  trust,
  identity,
  ready,
  signal,
  assertCurrent,
  now = Date.now,
}) {
  const generation = transport.epoch;
  /** @param {any} signed @param {string|null} requestId */
  async function verify(signed, requestId) {
    current();
    await verifyEnvelopeWithRefresh(signed, {
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
        requestId,
      },
      now,
    });
    current();
  }
  function current() {
    signal.throwIfAborted();
    assertCurrent();
    trust.assertUsable();
    if (transport.epoch !== generation || transport.state !== 'ready')
      throw new Error('mailbox connection changed');
  }
  /** @param {any} frame @param {string} expected @param {AbortSignal} [execution] */
  async function request(frame, expected, execution) {
    const bytes = encodeJson(frame);
    const abort = new AbortController(),
      started = performance.now();
    /** @type {MailboxError|undefined} */ let overload;
    const stop = () => abort.abort(new Error('mailbox interrupted'));
    signal.addEventListener('abort', stop, { once: true });
    execution?.addEventListener('abort', stop, { once: true });
    if (signal.aborted || execution?.aborted) stop();
    const timer = setTimeout(
      () => abort.abort(overload ?? new Error('mailbox timeout')),
      networkTiming.operationMs,
    );
    const check = () => {
      abort.signal.throwIfAborted();
      current();
    };
    try {
      for (let attempt = 1; attempt <= 5; attempt++) {
        check();
        let off = () => {},
          finished = false,
          verifying = false;
        let onAbort = () => {};
        let result;
        try {
          result = await new Promise((resolve, reject) => {
            onAbort = () => reject(abort.signal.reason);
            abort.signal.addEventListener('abort', onAbort, { once: true });
            off = transport.onMessage((payload, receivedEpoch) => {
              void (async () => {
                if (finished || verifying || receivedEpoch !== generation)
                  return;
                check();
                const value = decodeJson(payload);
                if (
                  (value.request_id ?? value.page?.request_id) !==
                  frame.request_id
                )
                  return;
                verifying = true;
                await verify(
                  value.type === 'fetch_result' ? value.page : value,
                  frame.request_id,
                );
                check();
                if (value.type === 'error') {
                  if (
                    value.scope !== 'request' ||
                    typeof value.retryable !== 'boolean' ||
                    !(
                      value.retry_after === null ||
                      (Number.isSafeInteger(value.retry_after) &&
                        value.retry_after >= 1 &&
                        value.retry_after <= 86400)
                    )
                  )
                    throw new Error('invalid mailbox error');
                } else if (value.type !== expected)
                  throw new Error('unexpected mailbox result');
                resolve(value);
              })().catch(reject);
            });
            check();
            void transport.send(bytes).catch(reject);
          });
        } finally {
          finished = true;
          off();
          abort.signal.removeEventListener('abort', onAbort);
        }
        check();
        if (result.type !== 'error') return result;
        const error = new MailboxError(result, attempt);
        if (result.code !== 'network.overloaded' || result.retryable !== true)
          throw error;
        overload = error;
        const backoff = Math.max(
          100 * 2 ** (attempt - 1),
          (result.retry_after ?? 0) * 1000,
        );
        if (
          attempt === 5 ||
          backoff >= networkTiming.operationMs - (performance.now() - started)
        )
          throw error;
        await delay(backoff, undefined, { signal: abort.signal });
        check();
      }
      throw overload;
    } catch (error) {
      if (abort.signal.aborted) throw abort.signal.reason;
      throw error;
    } finally {
      clearTimeout(timer);
      signal.removeEventListener('abort', stop);
      execution?.removeEventListener('abort', stop);
    }
  }
  return {
    request,
    verify,
    /** @param {string} type @param {any} fields */
    frame: (type, fields) => ({
      app_version: '1.0',
      type,
      request_id: randomUUID(),
      epoch: ready.epoch,
      ...fields,
    }),
  };
}
