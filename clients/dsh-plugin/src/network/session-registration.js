import { randomUUID } from 'node:crypto';
import { performance } from 'node:perf_hooks';
import { setTimeout as delay } from 'node:timers/promises';
import {
  createNativeHostPort,
  registrationSession,
} from './host/native/host-port.js';
import { createHostAdapter } from './host/adapter.js';
import { formatAddress } from './host/address.js';
import {
  decodeJson,
  encodeLocalRegister,
  assertRegisterResult,
} from './session/frames.js';
import { verifyEnvelopeWithRefresh } from './trust/verifier.js';
import { createKeyedMutex } from './host/keyed-mutex.js';
import { digestOf } from './host/ledger.js';

/** @typedef {{signal?:AbortSignal,assertCurrent?:()=>void}} RegistrationOperation
 * @typedef {(entry:import('./host/ledger.js').LocalRegistration,operation?:RegistrationOperation)=>Promise<import('./host/ledger.js').RegistrationResult>} RegisterLocal */

class RegistrationOverload extends Error {
  /** @param {any} frame @param {number} attempts */
  constructor(frame, attempts) {
    super('network.overloaded');
    this.retryable = frame.retryable;
    this.retryAfter = frame.retry_after;
    this.attempts = attempts;
  }
}

/** Explicit registration capability; source establishment is automatic afterward.
 * @param {{ctx:any,ledger:import('./host/ledger.js').Ledger,registerLocal:RegisterLocal,assertCurrent:()=>void,onStorageFailure?:()=>void,onRegistered?:(local:string)=>Promise<void>}} options */
export function createSessionRegistration({
  ctx,
  ledger,
  registerLocal,
  assertCurrent,
  onStorageFailure,
  onRegistered,
}) {
  let closed = false;
  let reason = 'host_unavailable';
  const operations = createKeyedMutex();
  /** @type {(()=>void)|undefined} */
  let unregister;
  const current = () => {
    if (closed) throw new Error('registration retired');
    assertCurrent();
  };
  /** @param {(draft:import('./host/ledger.js').LedgerState)=>any} mutate
   * @param {()=>void} fence */
  async function update(mutate, fence) {
    try {
      return await ledger.update(mutate, fence);
    } catch (error) {
      try {
        ledger.assertAvailable();
      } catch {
        onStorageFailure?.();
      }
      throw error;
    }
  }
  // One stable ledger facade for this capability, including reconnect recovery.
  const guarded = {
    ...ledger,
    update(
      /** @type {(draft:import('./host/ledger.js').LedgerState)=>any} */ mutate,
    ) {
      return update(mutate, current);
    },
    assertAvailable() {
      current();
      ledger.assertAvailable();
    },
  };
  return {
    get status() {
      return { available: !!unregister && !closed, reason };
    },
    async start() {
      current();
      const host = await createNativeHostPort({ ctx });
      current();
      const capabilities = host.getDiagnostics().capabilities;
      if (
        !capabilities.source.available ||
        !capabilities.durability.available ||
        typeof ctx.tools?.register !== 'function'
      )
        return;
      const unavailable = async () => {
        throw new Error('registration-only capability');
      };
      const recovery = createHostAdapter({
        ledger: guarded,
        host,
        network: {
          registerLocal,
          admit: unavailable,
          assignContact: unavailable,
        },
      });
      reason = 'recovery_unavailable';
      await recovery.recoverLocalRegistrations();
      current();
      unregister = ctx.tools.register({
        name: 'auriga_network_join',
        description:
          'Durably register this native session’s opaque Auriga address. Registration grants no sending, source or execution permission.',
        parameters: {
          type: 'object',
          properties: {},
          additionalProperties: false,
        },
        output: {
          schema: {
            type: 'object',
            properties: {
              status: { type: 'string', enum: ['registered', 'unavailable'] },
              address: { type: 'string' },
              reason: { type: 'string', enum: ['network.overloaded'] },
              retryable: { type: 'boolean' },
              retryAfter: {
                type: ['integer', 'null'],
                minimum: 1,
                maximum: 86400,
              },
              attempts: { type: 'integer', minimum: 1, maximum: 5 },
            },
            required: ['status'],
            additionalProperties: false,
          },
          render: (
            /** @type {unknown} */ _input,
            /** @type {unknown} */ value,
          ) => [{ type: 'text', text: JSON.stringify(value) }],
        },
        async execute(
          /** @type {unknown} */ input,
          /** @type {any} */ execution,
        ) {
          return operations.run('join', async () => {
            try {
              current();
              if (
                !input ||
                typeof input !== 'object' ||
                Array.isArray(input) ||
                Reflect.ownKeys(input).length
              )
                throw new Error('zero arguments required');
              const native = registrationSession(ctx, execution);
              const check = () => {
                current();
                execution.signal?.throwIfAborted();
                native.check();
              };
              check();
              const scopedLedger = {
                ...guarded,
                update(
                  /** @type {(draft:import('./host/ledger.js').LedgerState)=>any} */ mutate,
                ) {
                  return update(mutate, check);
                },
              };
              // bindExistingSession reads once before allocation and again before
              // binding. Both reads must prove the actual native durable barrier.
              const durableHost = {
                ...host,
                async getSession(/** @type {string} */ id) {
                  check();
                  if (id !== native.id) throw new Error('wrong native session');
                  await native.verify();
                  check();
                  const record = await host.getSession(id);
                  check();
                  if (
                    !record ||
                    record.cwd !== native.cwd ||
                    record.origin !== undefined
                  )
                    throw new Error('native mapping changed');
                  const durable = await host.hasDurableProvider?.(id);
                  check();
                  if (durable !== true)
                    throw new Error('durable provider required');
                  const flushed = await host.flush(id);
                  check();
                  if (flushed !== true)
                    throw new Error('durable session barrier required');
                  const persisted = await host.getSession(id);
                  check();
                  if (
                    !persisted ||
                    persisted.cwd !== native.cwd ||
                    persisted.origin !== undefined
                  )
                    throw new Error('durable native mapping changed');
                  return persisted;
                },
              };
              const adapter = createHostAdapter({
                ledger: scopedLedger,
                host: durableHost,
                network: {
                  registerLocal: async (entry) => {
                    check();
                    const result = await registerLocal(entry, {
                      signal: execution.signal,
                      assertCurrent: check,
                    });
                    check();
                    return result;
                  },
                  admit: unavailable,
                  assignContact: unavailable,
                },
              });
              const state = ledger.peek();
              const prior = Object.values(state.bindings).filter(
                (binding) => binding.hostSessionId === native.id,
              );
              if (
                prior.length > 1 ||
                (prior[0] &&
                  (prior[0].cwd !== native.cwd ||
                    prior[0].kind !== 'session' ||
                    prior[0].lifecycle !== 'open'))
              )
                throw new Error('native binding changed');
              const original =
                state.locals[
                  prior[0]?.local ?? state.registrations[`host:${native.id}`]
                ];
              const local = await adapter.bindExistingSession({
                // Host ids are opaque and may exceed protocol ID bounds. Only
                // a bounded digest goes on the wire; existing ops never change.
                opId: original?.opId ?? `host:${digestOf(native.id)}`,
                hostSessionId: native.id,
                cwd: native.cwd,
                kind: 'session',
                receiveMode: 'receive_none',
              });
              check();
              // The local registration remains successful even if the optional
              // source path is unavailable. Source work is never a model choice.
              try {
                await onRegistered?.(local);
              } catch {
                /* Source stays closed. */
              }
              check();
              return {
                status: 'registered',
                address: formatAddress(adapter.localAddress(local)),
              };
            } catch (error) {
              if (error instanceof RegistrationOverload) {
                try {
                  current();
                  execution.signal?.throwIfAborted();
                  registrationSession(ctx, execution).check();
                  return {
                    status: 'unavailable',
                    reason: 'network.overloaded',
                    retryable: error.retryable,
                    retryAfter: error.retryAfter,
                    attempts: error.attempts,
                  };
                } catch {
                  /* Retired execution must not report a live retry hint. */
                }
              }
              return { status: 'unavailable' };
            }
          });
        },
      });
      current();
      reason = 'ready';
    },
    close() {
      closed = true;
      if (reason === 'ready') reason = 'stopped';
      unregister?.();
      unregister = undefined;
    },
  };
}

/** Existing formal LOCAL_REGISTER on the current strict READY connection.
 * Signature context uses READY nonce/epoch; fingerprint stays correlated with
 * the immutable original request, never invented from a response projection.
 * @param {{transport:import('./session/memory-pipe.js').TransportLike,trust:Awaited<ReturnType<typeof import('./trust/store.js').openTrustStore>>,identity:import('./host/ledger.js').LedgerIdentity,ready:{epoch:string,channelNonce:string,bootId:string},signal:AbortSignal,assertCurrent:()=>void,now:()=>number}} options
 * @returns {RegisterLocal} */
export function connectionRegistrar({
  transport,
  trust,
  identity,
  ready,
  signal,
  assertCurrent,
  now,
}) {
  return async (entry, operation = {}) => {
    // One logical request and original five-second budget across all attempts.
    entry = { ...entry };
    const requestId = randomUUID();
    const bytes = encodeLocalRegister({
      requestId,
      epoch: ready.epoch,
      bootId: ready.bootId,
      candidate_local: entry.local,
      allocation_op_id: entry.opId,
      allocation_fingerprint: 'sha256:' + entry.fingerprint,
      address_type: entry.addressType,
      expected_registration_revision: entry.expectedRegistrationRevision,
    });
    const request = decodeJson(bytes);
    const context = {
      ...identity,
      epoch: ready.epoch,
      channelNonce: ready.channelNonce,
      requestId,
    };
    const interrupted = new AbortController();
    const abort = () =>
      interrupted.abort(new Error('registration interrupted'));
    const signals = [signal, operation.signal];
    const started = performance.now();
    /** @type {RegistrationOverload|undefined} */
    let overload;
    const timer = setTimeout(
      () => interrupted.abort(overload ?? new Error('registration timeout')),
      5000,
    );
    for (const value of signals) {
      value?.addEventListener('abort', abort, { once: true });
      if (value?.aborted) abort();
    }
    const current = () => {
      interrupted.signal.throwIfAborted();
      assertCurrent();
      operation.assertCurrent?.();
      trust.assertUsable();
    };
    try {
      for (let attempt = 1; attempt <= 5; attempt++) {
        current();
        if (overload) overload.attempts = attempt;
        let off = () => {},
          finished = false,
          verifying = false;
        let onAbort = () => {};
        let result;
        try {
          result = await new Promise((resolve, reject) => {
            onAbort = () => reject(interrupted.signal.reason);
            interrupted.signal.addEventListener('abort', onAbort, {
              once: true,
            });
            off = transport.onMessage((payload) => {
              void (async () => {
                current();
                if (finished || verifying) return;
                const frame = decodeJson(payload);
                if (frame.request_id !== requestId) return;
                verifying = true;
                await verifyEnvelopeWithRefresh(frame, {
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
                  context,
                  now,
                });
                current();
                if (!(
                  frame.type === 'error' &&
                  frame.code === 'network.overloaded' &&
                  frame.scope === 'request' &&
                  typeof frame.retryable === 'boolean' &&
                  (frame.retry_after === null ||
                    (Number.isSafeInteger(frame.retry_after) &&
                      frame.retry_after >= 1 &&
                      frame.retry_after <= 86400))
                ))
                  assertRegisterResult(frame, request);
                resolve(frame);
              })().catch(reject);
            });
            current();
            void transport.send(bytes).catch(reject);
          });
        } finally {
          finished = true;
          off();
          interrupted.signal.removeEventListener('abort', onAbort);
        }
        current();
        if (result.type !== 'error')
          return {
            status: 'registered',
            registrationId: result.registration_id,
            opId: entry.opId,
            local: entry.local,
            addressType: entry.addressType,
            fingerprint: entry.fingerprint,
            registrationRevision: result.registration_revision,
            localHighWater: result.local_high_water,
          };
        overload = new RegistrationOverload(result, attempt);
        const backoff = Math.max(
          100 * 2 ** (attempt - 1),
          (overload.retryAfter ?? 0) * 1000,
        );
        if (
          !overload.retryable ||
          attempt === 5 ||
          backoff >= 5000 - (performance.now() - started)
        )
          throw overload;
        await delay(backoff, undefined, { signal: interrupted.signal });
      }
      throw overload;
    } catch (error) {
      if (interrupted.signal.aborted) throw interrupted.signal.reason;
      throw error;
    } finally {
      clearTimeout(timer);
      for (const value of signals) value?.removeEventListener('abort', abort);
    }
  };
}
