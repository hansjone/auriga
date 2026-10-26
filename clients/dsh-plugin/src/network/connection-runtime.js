/* global AbortSignal */
import { randomUUID, createHash } from 'node:crypto';
import { join } from 'node:path';
import { NatsTransport, createWsSocketFactory } from './transport/index.js';
import { createFileLedgerStore } from './host/store.js';
import { loadConnectionLedger } from './host/ledger.js';
import { openTrustStore } from './trust/store.js';
import { canonicalize } from './trust/jcs.js';
import { verifyEnvelopeWithRefresh } from './trust/verifier.js';
import { bootstrapReady } from './session/bootstrap-ready.js';
import {
  assertReadyResult,
  readyCandidate,
  decodeJson,
  encodeReady,
} from './session/frames.js';
import { networkIsolation } from './isolation.js';
import {
  createSessionRegistration,
  connectionRegistrar,
} from './session-registration.js';
import {
  isDeviceRevocationError,
  DeviceControlUnavailable,
} from './device-provider.js';

// At most 64 pending protocol payloads (each already bounded by NatsLink).
const MESSAGE_QUEUE_CAPACITY = 64;

/** Strict known-identity handshake only; no delivery session or business ports.
 * @param {import('./session/memory-pipe.js').TransportLike} transport
 * @param {Awaited<ReturnType<typeof openTrustStore>>} trust
 * @param {import('./host/ledger.js').LedgerState} state
 * @param {AbortSignal} signal @param {()=>number} now */
async function normalReady(transport, trust, state, signal, now) {
  const identity = state.identity,
    epoch = transport.epoch;
  const requestId = randomUUID(),
    channelNonce = randomUUID(),
    bootId = randomUUID();
  // There is no independent durable resume_cursor in this ledger. Never guess
  // a receipt cursor from server output or discard existing recipient evidence.
  if (Object.keys(state.recipients).length)
    throw new Error('durable receipt cursor unavailable');
  const checkpoint = {
    checkpoint_seq: '0',
    checkpoint_revision: String(state.revision),
    summary_digest:
      'sha256:' +
      createHash('sha256').update(canonicalize(state.recipients)).digest('hex'),
  };
  const pending = state.locals[state.registrations[state.localQueue[0]]];
  const current = () => {
    signal.throwIfAborted();
    if (transport.epoch !== epoch || transport.state !== 'ready')
      throw new Error('READY connection changed');
  };
  let off = () => {},
    timer;
  let verifying = false,
    finished = false;
  /** @type {()=>void} */
  let abort = () => {};
  try {
    const result = await new Promise((resolve, reject) => {
      abort = () => reject(new Error('READY interrupted'));
      signal.addEventListener('abort', abort, { once: true });
      timer = setTimeout(() => reject(new Error('READY timeout')), 5000);
      off = transport.onMessage((bytes, receivedEpoch) => {
        void (async () => {
          current();
          if (finished || verifying || receivedEpoch !== epoch) return;
          const frame = decodeJson(bytes);
          if (frame.request_id !== requestId) return;
          verifying = true;
          const candidate = readyCandidate(frame);
          const context = {
            ...identity,
            requestId,
            channelNonce,
            epoch: candidate.epoch,
          };
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
          assertReadyResult(frame, context);
          // Only this durable queue head can explain a missing response. READY
          // never copies server counters into the ledger; LOCAL_REGISTER still
          // reconciles the original op/type/fingerprint before any binding.
          const pendingUncommitted =
            pending &&
            frame.local_high_water ===
              (BigInt(pending.local) - 1n).toString() &&
            frame.registration_revision === state.registrationRevision;
          const pendingCommitted =
            pending &&
            frame.local_high_water === pending.local &&
            frame.registration_revision ===
              (BigInt(pending.expectedRegistrationRevision) + 1n).toString();
          if (
            (!(
              frame.local_high_water === state.localHighWater &&
              frame.registration_revision === state.registrationRevision
            ) &&
              !pendingUncommitted &&
              !pendingCommitted) ||
            frame.committed_cursor !== '0' ||
            BigInt(frame.retention_low_seq) > BigInt(frame.head_seq) ||
            (frame.seen_checkpoint === null
              ? // Registration-only revisions are not receipt checkpoints. The
                // empty recipient map and cursor=0 are already required above.
                false
              : canonicalize(frame.seen_checkpoint) !==
                canonicalize(checkpoint))
          )
            throw new Error('READY recovery evidence mismatch');
          trust.assertUsable();
          resolve(frame);
        })().catch(reject);
      });
      current();
      void transport
        .send(
          encodeReady({
            requestId,
            channelNonce,
            bootId,
            keysetVersion: trust.version,
            resumeCursor: '0',
            localHighWater: state.localHighWater,
            registrationRevision: state.registrationRevision ?? '0',
            receiptCheckpoint: checkpoint,
            pendingRegistration: pending
              ? {
                  candidate_local: pending.local,
                  allocation_op_id: pending.opId,
                  allocation_fingerprint: 'sha256:' + pending.fingerprint,
                  address_type: pending.addressType,
                }
              : null,
          }),
        )
        .catch(reject);
    });
    current();
    return { ...result, channelNonce, bootId };
  } finally {
    finished = true;
    off();
    clearTimeout(timer);
    signal.removeEventListener('abort', abort);
  }
}

/** Base connection plus an optional, explicitly invoked registration capability.
 * No consent, source, FETCH, receipt or SEND. One writer spans C1, C2 and join.
 * @param {import('./runtime-types.js').ConnectionRuntimeOptions} options */
export async function openConnectionRuntime(options) {
  const { prepared } = options,
    controller = new AbortController();
  const url = new URL(prepared.url);
  if (
    url.protocol !== 'wss:' ||
    url.username ||
    url.password ||
    url.search ||
    url.hash
  )
    throw new Error('trusted WSS endpoint required');
  const signal = controller.signal,
    now = options.now ?? Date.now;
  let closed = false,
    connected = false,
    started = false,
    reason = 'stopped';
  let work = Promise.resolve(),
    handshakes = Promise.resolve();
  /** @type {Promise<void>|undefined} */
  let closing;
  /** @type {ReturnType<typeof setTimeout>|undefined} */
  let deadline;
  /** @type {import('./host/store.js').LedgerStoreHandle[]} */
  const handles = [];
  /** @type {Set<Promise<unknown>>} */
  const writes = new Set();
  /** @type {Set<any>} */
  const connections = new Set();
  /** @type {any} */
  let currentConnection;
  /** @type {Awaited<ReturnType<typeof openTrustStore>>|undefined} */
  let connectionTrust;
  /** @type {import('./host/ledger.js').Ledger|undefined} */
  let sessionLedger;
  /** @type {ReturnType<typeof createSessionRegistration>|undefined} */
  let registrationCapability;
  /** @type {AbortController|undefined} */
  let registrationController;
  /** @type {any} */
  let hostContext;
  /** @type {any} */
  let hostInjection;
  let registrationWork = Promise.resolve();
  function retireRegistration() {
    registrationController?.abort();
    registrationController = undefined;
    registrationCapability?.close();
    registrationCapability = undefined;
  }
  function installRegistration() {
    retireRegistration();
    const entry = currentConnection;
    const ledger = sessionLedger;
    const trust = connectionTrust;
    if (
      !connected ||
      closed ||
      !hostContext ||
      !entry?.proof ||
      !ledger ||
      !trust
    )
      return;
    const abort = new AbortController();
    registrationController = abort;
    const epoch = entry.transport.epoch;
    const proof = entry.proof;
    const assertCurrent = () => {
      active();
      abort.signal.throwIfAborted();
      trust.assertUsable();
      if (
        !connected ||
        currentConnection !== entry ||
        !entry.live ||
        entry.transport.state !== 'ready' ||
        entry.transport.epoch !== epoch ||
        entry.proof !== proof
      )
        throw new Error('registration connection changed');
    };
    const capability = createSessionRegistration({
      ctx: hostContext,
      ledger,
      assertCurrent,
      onStorageFailure: () => {
        void close('storage_failure').catch(() => {});
      },
      registerLocal: connectionRegistrar({
        transport: entry.facade,
        trust,
        identity: ledger.identity,
        ready: proof,
        signal: abort.signal,
        assertCurrent,
        now,
      }),
    });
    registrationCapability = capability;
    registrationWork = registrationWork.then(async () => {
      try {
        await capability.start();
      } catch {
        capability.close();
      }
    });
  }
  const active = () => {
    signal.throwIfAborted();
    if (closed) throw new Error('connection closed');
  };
  const onAbort = () => {
    void close().catch(() => {});
  };
  /** @param {Parameters<import('./runtime-types.js').PreparedConnection['refresh']>[0]} value
   * @returns {Promise<void>} */
  async function refreshTrust(value) {
    await prepared.refresh(value, signal);
    active();
    value.assertUsable();
    clearTimeout(deadline);
    deadline = setTimeout(
      () => {
        void close('key_unavailable').catch(() => {});
      },
      Math.max(0, value.expiresAt - now()),
    );
    deadline.unref?.();
  }
  /** @param {string} name */
  async function handle(name) {
    active();
    const store =
      options.stores?.[name] ??
      createFileLedgerStore({
        dir: join(options.directory ?? '', name === 'ledger' ? 'host' : name),
      });
    const h = await store.open();
    handles.push(h);
    active();
    return h;
  }
  /** @param {import('./host/store.js').LedgerStoreHandle} h */
  function guarded(h) {
    return {
      load: () => h.load(),
      async commit(/** @type {unknown} */ value) {
        active();
        const writing = h.commit(value);
        writes.add(writing);
        try {
          await writing;
          active();
        } catch (error) {
          void close('storage_failure').catch(() => {});
          throw error;
        } finally {
          writes.delete(writing);
        }
      },
    };
  }
  /** @param {Awaited<ReturnType<typeof openTrustStore>>} trust */
  function connect(trust, /** @type {boolean} */ normal) {
    const attempt = new AbortController();
    let stopping;
    const entry = {
      live: true,
      receiving: false,
      messages: /** @type {{bytes:Uint8Array,epoch:number}[]} */ ([]),
      epoch: 0,
      attempt,
      listeners: /** @type {Set<(bytes:Uint8Array,epoch:number)=>void>} */ (
        new Set()
      ),
      transport: /** @type {any} */ (undefined),
      proof: /** @type {any} */ (undefined),
      facade: /** @type {any} */ (undefined),
      stop() {
        entry.live = false;
        entry.attempt.abort();
        entry.listeners.clear();
        entry.messages.length = 0;
        return (stopping ??= Promise.resolve().then(() =>
          entry.transport?.stop(),
        ));
      },
    };
    currentConnection = entry;
    connections.add(entry);
    const valid = () => {
      active();
      if (!entry.live || currentConnection !== entry)
        throw new Error('connection replaced');
    };
    const transport = (
      options.transportFactory ?? ((o) => new NatsTransport(o))
    )({
      url: prepared.url,
      upSubject: 'auriga.net.up.a.' + 'a'.repeat(26),
      downSubject: 'auriga.net.down.' + 'a'.repeat(26),
      inboxPrefix: 'auriga.net.reply.' + 'a'.repeat(26),
      createSocket: createWsSocketFactory(),
      async getTicket({ reason: ticketReason }) {
        let requestingTicket = false;
        try {
          valid();
          await refreshTrust(trust);
          valid();
          requestingTicket = true;
          const ticket = await prepared.getTicket(signal);
          valid();
          return ticket;
        } catch (error) {
          const e = /** @type {any} */ (error);
          if (!entry.live || closed || currentConnection !== entry) throw error;
          if (isDeviceRevocationError(error)) {
            options.onRevoked?.();
            void close('revoked').catch(() => {});
          } else if (e?.status === 403) {
            void close('forbidden').catch(() => {});
          } else if (
            ![429, 503].includes(e?.status) &&
            !(
              ticketReason === 'refresh' &&
              transport.state === 'ready' &&
              (e instanceof DeviceControlUnavailable ||
                (requestingTicket &&
                  (e instanceof TypeError ||
                    ['TimeoutError', 'AbortError'].includes(e?.name))))
            )
          )
            void close('unavailable').catch(() => {});
          else {
            try {
              trust.assertUsable();
            } catch {
              void close('key_unavailable').catch(() => {});
            }
          }
          throw error;
        }
      },
      onMessage(bytes, epoch) {
        if (
          !entry.live ||
          closed ||
          currentConnection !== entry ||
          epoch !== transport.epoch
        )
          return;
        if (entry.messages.length >= MESSAGE_QUEUE_CAPACITY) {
          void close('message_backpressure').catch(() => {});
          return;
        }
        entry.messages.push({ bytes, epoch });
        if (entry.receiving) return;
        entry.receiving = true;
        const receive = async () => {
          try {
            while (entry.messages.length) {
              const message = entry.messages.shift();
              if (!message) return;
              valid();
              if (
                message.epoch !== transport.epoch ||
                transport.state !== 'ready'
              )
                continue;
              try {
                const frame = decodeJson(message.bytes);
                if (frame.keyset_announcement) {
                  const resume = connected;
                  connected = false;
                  retireRegistration();
                  await trust.announce(frame.keyset_announcement);
                  valid();
                  await refreshTrust(trust);
                  valid();
                  if (
                    message.epoch !== transport.epoch ||
                    transport.state !== 'ready'
                  )
                    continue;
                  trust.assertUsable();
                  connected = resume;
                  if (connected) installRegistration();
                }
                valid();
                for (const listener of entry.listeners)
                  listener(message.bytes, message.epoch);
              } catch (error) {
                if (message.epoch === transport.epoch) throw error;
              }
            }
          } finally {
            entry.receiving = false;
          }
        };
        void receive().catch(() => {
          void close('unavailable').catch(() => {});
        });
      },
      onState(state) {
        if (!entry.live || closed || currentConnection !== entry) return;
        if (state.state !== 'ready') {
          connected = false;
          retireRegistration();
          entry.proof = undefined;
          entry.attempt.abort();
          return;
        }
        if (state.epoch === entry.epoch) return;
        entry.epoch = state.epoch;
        connected = false;
        retireRegistration();
        entry.proof = undefined;
        entry.attempt.abort();
        entry.attempt = new AbortController();
        if (!normal) return;
        const handshakeSignal = AbortSignal.any([signal, entry.attempt.signal]);
        handshakes = handshakes
          .then(async () => {
            valid();
            handshakeSignal.throwIfAborted();
            if (!sessionLedger)
              throw new Error('complete connection ledger required');
            const proof = await normalReady(
              facade,
              trust,
              sessionLedger.peek(),
              handshakeSignal,
              now,
            );
            valid();
            handshakeSignal.throwIfAborted();
            entry.proof = proof;
            connected = true;
            installRegistration();
          })
          .catch(() => {
            if (!closed && !handshakeSignal.aborted)
              void close('unavailable').catch(() => {});
          });
      },
    });
    entry.transport = transport;
    /** @type {import('./session/memory-pipe.js').TransportLike} */
    const facade = {
      send: (bytes) => transport.send(bytes),
      stop: () => entry.stop(),
      onMessage(listener) {
        entry.listeners.add(listener);
        return () => entry.listeners.delete(listener);
      },
      get state() {
        return entry.live ? transport.state : 'stopped';
      },
      get epoch() {
        return transport.epoch;
      },
    };
    entry.facade = facade;
    return { entry, transport, facade };
  }
  async function start() {
    active();
    if (started) throw new Error('connection already started');
    started = true;
    work = (async () => {
      if (!options.directory && !options.stores)
        throw new Error('private connection directory required');
      const h = await handle('ledger');
      const registration = await prepared.register(signal);
      active();
      const ledger = await loadConnectionLedger(
        /** @type {import('./host/store.js').LedgerStoreHandle} */ ({
          ...guarded(h),
          close: () => h.close(),
        }),
        registration,
      );
      active();
      const cache = await handle('trust');
      /** @type {Awaited<ReturnType<typeof openTrustStore>>} */
      const trust = await openTrustStore({
        cache: guarded(cache),
        roots: [...prepared.roots],
        realm: registration.realm,
        requireAnnouncement: true,
        now,
        refresh: async () => {
          await refreshTrust(trust);
          active();
        },
      });
      connectionTrust = trust;
      active();
      await refreshTrust(trust);
      active();
      if (!ledger.state) {
        const first = connect(trust, false);
        await first.transport.start();
        active();
        first.entry.epoch = first.transport.epoch;
        const bootstrapSignal = AbortSignal.any([
          signal,
          first.entry.attempt.signal,
        ]);
        const output = await bootstrapReady({
          registration: { ...registration, created: true },
          transport: first.facade,
          trust,
          signal: bootstrapSignal,
          now,
        });
        active();
        const firstCurrent = () => {
          active();
          bootstrapSignal.throwIfAborted();
          if (
            first.transport.state !== 'ready' ||
            first.transport.epoch !== first.entry.epoch
          )
            throw new Error('bootstrap connection changed');
        };
        firstCurrent();
        await ledger.initialize(output.identity);
        active();
        firstCurrent();
        await first.entry.stop();
        active();
        connections.delete(first.entry);
      }
      sessionLedger = ledger.adopt();
      // Optional public services do not gate the base connection. Cordis owns
      // this child scope and immediately retires tools when a service unloads.
      try {
        if (typeof options.ctx?.inject === 'function') {
          hostInjection = options.ctx.inject(
            [
              'tools',
              'sessions',
              'sessionController',
              'agents',
              'sessionPersistence',
              'workspaceRegistry',
            ],
            (/** @type {any} */ ctx) => {
              hostContext = ctx;
              installRegistration();
              ctx.effect(
                () => () => {
                  if (hostContext === ctx) {
                    hostContext = undefined;
                    retireRegistration();
                  }
                },
                'auriga-dsh: session registration host',
              );
            },
          );
        }
      } catch {
        /* Host unavailable: base READY remains independent. */
      }
      const normal = connect(trust, true);
      await normal.transport.start();
      active();
      await handshakes;
      active();
      if (!connected) throw new Error('strict READY unavailable');
    })();
    try {
      await work;
    } catch (error) {
      void close('unavailable').catch(() => {});
      throw error;
    }
  }
  function close(why = 'stopped') {
    if (closing) return closing;
    reason = why;
    closed = true;
    connected = false;
    retireRegistration();
    controller.abort();
    clearTimeout(deadline);
    options.signal?.removeEventListener('abort', onAbort);
    const stopping = [...connections].map((entry) => entry.stop());
    closing = (async () => {
      await Promise.allSettled(stopping);
      await Promise.allSettled([work, handshakes]);
      await registrationWork;
      await Promise.allSettled([...writes]);
      await Promise.allSettled([
        sessionLedger?.close(),
        Promise.resolve().then(() => hostInjection?.dispose()),
      ]);
      await Promise.allSettled(handles.map((h) => h.close()));
      options.onClosed?.(reason);
    })();
    return closing;
  }
  options.signal?.addEventListener('abort', onAbort, { once: true });
  if (options.signal?.aborted) onAbort();
  return {
    start,
    close,
    get diagnostics() {
      return {
        registration: registrationCapability?.status ?? {
          available: false,
          reason: connected ? 'host_unavailable' : 'not_ready',
        },
      };
    },
    get status() {
      let ready = !closed && connected;
      if (ready) {
        try {
          connectionTrust?.assertUsable();
        } catch {
          ready = false;
        }
      }
      return {
        available: !closed,
        connected: ready,
        isolation: networkIsolation,
        reason,
        state: closed
          ? reason === 'stopped'
            ? 'stopped'
            : 'unavailable'
          : ready
            ? 'ready'
            : 'connecting',
      };
    },
    consentUI(/** @type {unknown} */ _context) {
      throw new Error('network consent unavailable');
    },
  };
}
