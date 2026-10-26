import { randomUUID } from 'node:crypto';
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

/** Explicit registration capability; source establishment is automatic afterward.
 * @param {{ctx:any,ledger:import('./host/ledger.js').Ledger,registerLocal:import('./host/adapter.js').NetworkPort['registerLocal'],assertCurrent:()=>void,onStorageFailure?:()=>void,onRegistered?:(local:string)=>Promise<void>}} options */
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
                    const result = await registerLocal(entry);
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
            } catch {
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
 * @returns {import('./host/adapter.js').NetworkPort['registerLocal']} */
export function connectionRegistrar({
  transport,
  trust,
  identity,
  ready,
  signal,
  assertCurrent,
  now,
}) {
  return async (entry) => {
    const current = () => {
      signal.throwIfAborted();
      assertCurrent();
      trust.assertUsable();
    };
    current();
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
    let off = () => {},
      timer;
    /** @type {()=>void} */
    let abort = () => {};
    let finished = false,
      verifying = false;
    try {
      const result = await new Promise((resolve, reject) => {
        abort = () => reject(new Error('registration interrupted'));
        signal.addEventListener('abort', abort, { once: true });
        timer = setTimeout(
          () => reject(new Error('registration timeout')),
          5000,
        );
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
              context: {
                ...identity,
                epoch: ready.epoch,
                channelNonce: ready.channelNonce,
                requestId,
              },
              now,
            });
            current();
            assertRegisterResult(frame, request);
            resolve(frame);
          })().catch(reject);
        });
        current();
        void transport.send(bytes).catch(reject);
      });
      current();
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
    } finally {
      finished = true;
      off();
      clearTimeout(timer);
      signal.removeEventListener('abort', abort);
    }
  };
}
