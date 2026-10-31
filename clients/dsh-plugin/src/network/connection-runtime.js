import { networkTiming } from './timing.js';
/* global AbortSignal */
import { randomUUID, createHash } from 'node:crypto';
import { setTimeout as retryDelay } from 'node:timers/promises';
import { TransportError } from './transport/errors.js';
import { SessionError } from './session/errors.js';
import { join } from 'node:path';
import { NatsTransport, createWsSocketFactory } from './transport/index.js';
import { createFileLedgerStore } from './host/store.js';
import { loadConnectionLedger } from './host/ledger.js';
import { openTrustStore } from './trust/store.js';
import { canonicalize } from './trust/jcs.js';
import { verifyEnvelopeWithRefresh } from './trust/verifier.js';
import { bootstrapReady } from './session/bootstrap-ready.js';
import { createReadyRetry, readyRetryError } from './session/ready-retry.js';
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
import { createSourceControl } from './source-control/controller.js';
import { connectionSourceClient } from './source-control/connection.js';
import { stopSource } from './source-control/state.js';
import { saveProducerEpoch } from './durable/outbox.js';
import { createDeviceMessaging } from './durable/capability.js';
import { createStableNetworkTools } from './stable-tools.js';
import { hasCursorEvidence } from './durable/recovery.js';
import {
  createDiagnosticLog,
  createAttemptDiagnostics,
  diagnosticReason,
} from './diagnostics.js';

// At most 64 pending protocol payloads (each already bounded by NatsLink).
const MESSAGE_QUEUE_CAPACITY = 64;

/** No identity/signature/store failures qualify as a control-plane retry.
 * @param {unknown} error */
function transientControl(error) {
  return (
    error instanceof DeviceControlUnavailable ||
    [429, 503].includes(
      /** @type {{status?:number}|null} */ (error)?.status ?? 0,
    )
  );
}

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
    // The ledger revision includes local/source writes. Only the independent
    // durable receipt witness can prove an empty receipt history after those
    // writes. Preserve the conservative value for ledgers lacking that proof.
    checkpoint_revision: String(state.receiptRevision ?? state.revision),
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
  /** @type {ReturnType<typeof createReadyRetry>|undefined} */
  let retry;
  let verifying = false,
    finished = false;
  /** @type {()=>void} */
  let abort = () => {};
  try {
    const payload = encodeReady({
      requestId,
      channelNonce,
      bootId,
      keysetVersion: trust.version,
      resumeCursor: state.durable?.resumeCursor ?? '0',
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
    });
    const result = await new Promise((resolve, reject) => {
      const attemptRetry = createReadyRetry(
        () => transport.send(payload),
        current,
        reject,
      );
      retry = attemptRetry;
      abort = () => reject(new Error('READY interrupted'));
      signal.addEventListener('abort', abort, { once: true });
      timer = setTimeout(
        () =>
          reject(diagnosticReason(new Error('READY timeout'), 'ready_timeout')),
        networkTiming.operationMs,
      );
      off = transport.onMessage((bytes, receivedEpoch) => {
        void (async () => {
          current();
          if (finished || verifying || receivedEpoch !== epoch) return;
          const frame = decodeJson(bytes);
          if (frame.request_id !== requestId) return;
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
          const retryable = await readyRetryError(frame, {
            trust: guardedTrust,
            context: { ...identity, requestId, channelNonce, epoch: '0' },
            now,
          });
          current();
          if (retryable) {
            verifying = false;
            attemptRetry.schedule();
            return;
          }
          const candidate = readyCandidate(frame);
          const context = {
            ...identity,
            requestId,
            channelNonce,
            epoch: candidate.epoch,
          };
          await verifyEnvelopeWithRefresh(frame, {
            trust: guardedTrust,
            context,
            now,
          });
          current();
          assertReadyResult(frame, { ...context, allowRecoveryConflict: true });
          const recoveringReceipt =
            frame.status === 'recovery_conflict' &&
            hasCursorEvidence(state, frame.committed_cursor);
          if (frame.status !== 'ready' && !recoveringReceipt)
            throw new Error('READY receipt recovery evidence missing');
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
            (!recoveringReceipt &&
              frame.committed_cursor !==
                (state.durable?.resumeCursor ?? '0')) ||
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
          resolve({
            ...frame,
            recoverCursor: recoveringReceipt ? frame.committed_cursor : null,
          });
        })().catch(reject);
      });
      current();
      void transport.send(payload).catch(reject);
    });
    current();
    return { ...result, channelNonce, bootId };
  } finally {
    finished = true;
    retry?.close();
    off();
    clearTimeout(timer);
    signal.removeEventListener('abort', abort);
  }
}

/** Base connection, explicit registration and automatic internal source control.
 * No consent, FETCH, receipt or SEND. One writer spans all capabilities.
 * @param {import('./runtime-types.js').ConnectionRuntimeOptions} options */
export async function openConnectionRuntime(options) {
  const { prepared } = options,
    controller = new AbortController();
  if (
    prepared.storageKey !== undefined &&
    !/^[a-f0-9]{64}$/.test(prepared.storageKey)
  )
    throw new Error('invalid device storage key');
  const storageDirectory = prepared.storageKey
    ? join(options.directory ?? '', 'devices', prepared.storageKey)
    : options.directory;
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
  const stableRegistries = new Set();
  let closed = false,
    connected = false,
    started = false,
    reason = 'stopped';
  const diagnostics =
    options.diagnostics ??
    createAttemptDiagnostics({
      emit: createDiagnosticLog(options.ctx),
      valid: () => !closed && !options.signal?.aborted,
      signal,
    });
  /** @template T @param {string} phase @param {()=>T|Promise<T>} operation @param {number} [budget] */
  const run = (phase, operation, budget = 0) =>
    diagnostics
      .scope(phase, budget, () => !closed && !signal.aborted)
      .run(operation);
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
  /** Exact local stop snapshot alone may cross shutdown, never protocol writes. */
  let shutdownSnapshot = /** @type {unknown} */ (undefined);
  /** @type {ReturnType<typeof createSourceControl>|undefined} */
  let sourceCapability;
  /** @type {ReturnType<typeof createDeviceMessaging>|undefined} */
  let messagingCapability;
  /** @type {Set<ReturnType<typeof createDeviceMessaging>>} */
  const messengers = new Set();
  /** @type {AbortController|undefined} */ let sourceController;
  /** @type {Set<ReturnType<typeof createSourceControl>>} */ const sources =
    new Set();
  function retireRegistration(retireSource = true) {
    const messenger = messagingCapability;
    messenger?.close();
    if (messenger)
      void messenger.idle().then(() => messengers.delete(messenger));
    messagingCapability = undefined;
    if (retireSource) {
      sourceController?.abort();
      sourceController = undefined;
      const retired = sourceCapability;
      retired?.close();
      if (retired) void retired.idle().then(() => sources.delete(retired));
      sourceCapability = undefined;
    }
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
    if (!connected || closed || !entry?.proof || !ledger || !trust) return;
    const sourceAbort = new AbortController();
    sourceController = sourceAbort;
    const sourceEpoch = entry.transport.epoch,
      sourceProof = entry.proof;
    const sourceCurrent = () => {
      active();
      sourceAbort.signal.throwIfAborted();
      trust.assertUsable();
      if (
        !connected ||
        currentConnection !== entry ||
        !entry.live ||
        entry.transport.state !== 'ready' ||
        entry.transport.epoch !== sourceEpoch ||
        entry.proof !== sourceProof
      )
        throw new Error('source connection changed');
    };
    const source = createSourceControl({
      ledger,
      getContext: () => hostContext,
      ready: sourceProof,
      now,
      assertCurrent: sourceCurrent,
      onStorageFailure: () => {
        void close('storage_failure').catch(() => {});
      },
      request: connectionSourceClient({
        transport: entry.facade,
        trust,
        identity: ledger.identity,
        ready: sourceProof,
        signal: sourceAbort.signal,
        assertCurrent: sourceCurrent,
        now,
      }),
    });
    sourceCapability = source;
    sources.add(source);
    registrationWork = registrationWork.then(async () => {
      try {
        await source.start();
      } catch {
        /* Base connection stays available. */
      }
    });
    if (!hostContext) return;
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
      onRegistered: (local) => source.establish(local),
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
    if (prepared.submit && ledger.peek().durable) {
      const messenger = createDeviceMessaging({
        ctx: hostContext,
        ledger,
        prepared,
        source,
        transport: entry.facade,
        ready: proof,
        trust,
        signal: abort.signal,
        assertCurrent,
        now,
      });
      messagingCapability = messenger;
      messengers.add(messenger);
      registrationWork = registrationWork.then(async () => {
        try {
          await messenger.start();
        } catch {
          messenger.close();
        }
      });
    }
  }
  const active = () => {
    signal.throwIfAborted();
    if (closed) throw new Error('connection closed');
  };
  const onAbort = () => {
    void close().catch(() => {});
  };
  /** @param {Parameters<import('./runtime-types.js').PreparedConnection['refresh']>[0]} value
   * @param {()=>boolean} [current] @returns {Promise<void>} */
  async function refreshTrust(
    value,
    current = () => !closed && !signal.aborted,
  ) {
    await diagnostics
      .scope('trust_refresh', 0, current)
      .run(() => prepared.refresh(value, signal));
    active();
    diagnostics
      .scope('trust_refresh', 0, current)
      .sync(() => value.assertUsable());
    clearTimeout(deadline);
    deadline = setTimeout(
      () => {
        diagnostics
          .scope('trust_refresh')
          .fail({ code: 'network.key_unavailable' });
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
        dir: join(storageDirectory ?? '', name === 'ledger' ? 'host' : name),
      });
    const h = await store.open();
    handles.push(h);
    active();
    return h;
  }
  /** @param {import('./host/store.js').LedgerStoreHandle} h @param {'ledger'|'trust'} [kind] */
  function guarded(h, kind = 'ledger') {
    const writable = (/** @type {unknown} */ value) => {
      if (value !== shutdownSnapshot) active();
    };
    return {
      load: () => h.load(),
      async commit(/** @type {unknown} */ value) {
        writable(value);
        const failure = diagnostics.scope(
          kind === 'trust'
            ? 'trust_refresh'
            : !sessionLedger
              ? 'initial_commit'
              : 'durable_commit',
          0,
          () => !closed && !signal.aborted,
        );
        const writing = h.commit(value);
        writes.add(writing);
        try {
          await writing;
          writable(value);
        } catch (error) {
          failure.fail(error);
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
    const isCurrent = () =>
      entry.live && !closed && currentConnection === entry;
    let transportFailure = diagnostics.scope(
      'wss_connect',
      networkTiming.operationMs,
      isCurrent,
    );
    const transport = (
      options.transportFactory ?? ((o) => new NatsTransport(o))
    )({
      connectTimeoutMs: networkTiming.operationMs,
      url: prepared.url,
      upSubject: 'auriga.net.up.a.' + 'a'.repeat(26),
      downSubject: 'auriga.net.down.' + 'a'.repeat(26),
      inboxPrefix: 'auriga.net.reply.' + 'a'.repeat(26),
      createSocket: createWsSocketFactory({ origin: prepared.origin ?? '' }),
      async getTicket({ reason: ticketReason }) {
        const ticketFailure = diagnostics.scope(
          'ticket_http',
          networkTiming.operationMs,
          () => entry.live && !closed && currentConnection === entry,
        );
        let requestingTicket = false;
        try {
          valid();
          await refreshTrust(trust, isCurrent);
          valid();
          requestingTicket = true;
          const ticket = await prepared.getTicket(signal);
          valid();
          transportFailure = diagnostics.scope(
            'wss_connect',
            networkTiming.operationMs,
            isCurrent,
          );
          return ticket;
        } catch (error) {
          const e = /** @type {any} */ (error);
          if (!entry.live || closed || currentConnection !== entry) throw error;
          ticketFailure.fail(error);
          if (isDeviceRevocationError(error)) {
            options.onRevoked?.();
            void close('revoked').catch(() => {});
          } else if (e?.status === 403) {
            void close('forbidden').catch(() => {});
          } else if (!normal && transientControl(error)) {
            // C1 has no business capability and still owns the original
            // registration/writer. Its caller/transport may safely retry.
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
        void receive().catch((error) => {
          if (entry.live && !closed && currentConnection === entry)
            diagnostics.scope('connection_receive').fail(error);
          void close('unavailable').catch(() => {});
        });
      },
      onState(state) {
        if (!entry.live || closed || currentConnection !== entry) return;
        if (state.error !== undefined) transportFailure.fail(state.error);
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
            const state = sessionLedger.peek();
            const proof = await diagnostics
              .scope(
                'normal_ready',
                networkTiming.operationMs,
                () =>
                  !closed &&
                  entry.live &&
                  currentConnection === entry &&
                  !handshakeSignal.aborted,
              )
              .run(() =>
                normalReady(facade, trust, state, handshakeSignal, now),
              );
            valid();
            handshakeSignal.throwIfAborted();
            if (proof.producer_epoch !== undefined)
              await saveProducerEpoch(sessionLedger, proof.producer_epoch);
            valid();
            handshakeSignal.throwIfAborted();
            if (proof.recoverCursor) {
              // The conflict response never opens business gates. Retain the
              // exact acceptance/terminal proofs, persist only their verified prefix,
              // close this C and perform a fresh strict READY on a new ingress.
              await sessionLedger.update((draft) => {
                if (
                  !draft.durable ||
                  !hasCursorEvidence(draft, proof.recoverCursor)
                )
                  throw new Error('receipt recovery evidence changed');
                draft.durable.resumeCursor = proof.recoverCursor;
              }, valid);
              await entry.stop();
              connections.delete(entry);
              const replacement = connect(trust, true);
              await replacement.transport.start();
              return;
            }
            entry.proof = proof;
            connected = true;
            installRegistration();
          })
          .catch((error) => {
            if (!closed && !handshakeSignal.aborted) {
              diagnostics
                .scope('normal_ready', networkTiming.operationMs)
                .fail(error);
              void close('unavailable').catch(() => {});
            }
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
      const h = await run('ledger_open', () => handle('ledger'));
      const registration = await run(
        'register_http',
        () => prepared.register(signal),
        networkTiming.operationMs,
      );
      active();
      diagnostics.registered(registration.created);
      const ledger = await run('ledger_restore', () =>
        loadConnectionLedger(
          /** @type {import('./host/store.js').LedgerStoreHandle} */ ({
            ...guarded(h),
            close: () => h.close(),
          }),
          registration,
        ),
      );
      active();
      if (ledger.state) diagnostics.milestone('ledger_restored');
      const cache = await run('trust_open', () => handle('trust'));
      /** @type {Awaited<ReturnType<typeof openTrustStore>>} */
      const trust = await run('trust_load', () =>
        openTrustStore({
          cache: guarded(cache, 'trust'),
          roots: [...prepared.roots],
          realm: registration.realm,
          requireAnnouncement: true,
          now,
          refresh: async () => {
            await refreshTrust(trust);
            active();
          },
        }),
      );
      connectionTrust = trust;
      active();
      if (ledger.state) await refreshTrust(trust);
      else {
        let failures = 0;
        for (;;) {
          try {
            await refreshTrust(trust);
            break;
          } catch (error) {
            active();
            if (!transientControl(error)) throw error;
            await retryDelay(
              Math.min(30000, 250 * 2 ** Math.min(failures++, 7)),
              undefined,
              { signal },
            );
          }
        }
      }
      active();
      if (!ledger.state) {
        /** @type {ReturnType<typeof connect>|undefined} */
        let first = undefined;
        /** @type {AbortSignal} */
        let bootstrapSignal;
        /** @type {Awaited<ReturnType<typeof bootstrapReady>>} */
        let output;
        let failures = 0;
        for (;;) {
          active();
          try {
            first = diagnostics
              .scope('wss_connect', networkTiming.operationMs)
              .sync(() => connect(trust, false));
            const currentFirst = first;
            await run(
              'wss_connect',
              () => currentFirst.transport.start(),
              networkTiming.operationMs,
            );
            active();
            first.entry.epoch = first.transport.epoch;
            bootstrapSignal = AbortSignal.any([
              signal,
              first.entry.attempt.signal,
            ]);
            output = await run(
              'bootstrap_ready',
              () =>
                bootstrapReady({
                  registration: { ...registration, created: true },
                  transport: currentFirst.facade,
                  trust,
                  signal: bootstrapSignal,
                  now,
                }),
              networkTiming.operationMs,
            );
            active();
            break;
          } catch (error) {
            active();
            const transient =
              transientControl(error) ||
              (error instanceof TransportError &&
                [
                  'connection_closed',
                  'connection_lost',
                  'not_connected',
                  'connect_failed',
                  'timeout',
                  'write_stalled',
                ].includes(error.code)) ||
              (error instanceof SessionError &&
                ['timeout', 'stopped'].includes(error.code));
            if (!transient) throw error;
            // No initialize/commit has been attempted. Retain this live
            // registration and its original writer, never persist created=true
            // or reopen an absent ledger on a subsequent registration.
            if (first) {
              await first.entry.stop();
              connections.delete(first.entry);
            }
            active();
            await retryDelay(
              Math.min(30000, 250 * 2 ** Math.min(failures++, 7)),
              undefined,
              { signal },
            );
          }
        }
        if (!first || !bootstrapSignal || !output)
          throw new Error('bootstrap attempt unavailable');
        const completedFirst = first;
        const firstCurrent = () => {
          active();
          bootstrapSignal.throwIfAborted();
          if (
            completedFirst.transport.state !== 'ready' ||
            completedFirst.transport.epoch !== completedFirst.entry.epoch
          )
            throw new Error('bootstrap connection changed');
        };
        firstCurrent();
        await run('initial_commit', () =>
          ledger.initialize(output.identity, output.readyResult.producer_epoch),
        );
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
              const stable = createStableNetworkTools(ctx.tools);
              stableRegistries.add(stable);
              const facade = {
                register: (/** @type {any} */ definition) =>
                  stable.register(definition),
              };
              ctx = new Proxy(ctx, {
                get(target, key) {
                  return key === 'tools' ? facade : Reflect.get(target, key);
                },
              });
              hostContext = ctx;
              installRegistration();
              const stopSession = (/** @type {string} */ id) => {
                for (const e of Object.values(
                  sessionLedger?.peek().sourceControl?.entries ?? {},
                ))
                  if (e.mapping.hostSessionId === id) {
                    if (sourceCapability)
                      void sourceCapability
                        .stop(e.local, 'host_closed')
                        .catch(() => {});
                    else
                      void sessionLedger
                        ?.update((draft) =>
                          stopSource(draft, e.local, 'host_closed'),
                        )
                        .catch(() => {});
                  }
              };
              // Documented rc.2 lifecycle hooks; every operation also probes
              // live objects, archive set and provider after each await.
              const disposers =
                typeof ctx.on === 'function'
                  ? [
                      ctx.on('session/disposed', (/** @type {any} */ session) =>
                        stopSession(session.id),
                      ),
                      ctx.on('agent/disposed', (/** @type {any} */ payload) =>
                        stopSession(payload.agent.id),
                      ),
                      ctx.on(
                        'workspace/session-stop',
                        (/** @type {any} */ request) =>
                          stopSession(request.sessionId),
                      ),
                    ]
                  : [];
              ctx.effect(
                () => () => {
                  stable.close();
                  stableRegistries.delete(stable);
                  if (hostContext === ctx) {
                    hostContext = undefined;
                    if (sourceCapability)
                      void sourceCapability.stopAll().catch(() => {});
                    else
                      void sessionLedger
                        ?.update((draft) => {
                          for (const local of Object.keys(
                            draft.sourceControl?.entries ?? {},
                          ))
                            stopSource(draft, local, 'host_unavailable');
                        })
                        .catch(() => {});
                    retireRegistration(false);
                  }
                  for (const dispose of disposers) dispose();
                },
                'auriga-dsh: session registration host',
              );
            },
          );
        }
      } catch {
        /* Host unavailable: base READY remains independent. */
      }
      const normal = diagnostics
        .scope('wss_connect', networkTiming.operationMs)
        .sync(() => connect(trust, true));
      await run(
        'wss_connect',
        () => normal.transport.start(),
        networkTiming.operationMs,
      );
      active();
      let pending;
      do {
        pending = handshakes;
        await pending;
      } while (pending !== handshakes);
      active();
      if (!connected) throw new Error('strict READY unavailable');
      diagnostics.milestone('ready');
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
    for (const stable of stableRegistries) stable.close();
    stableRegistries.clear();
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
      await Promise.allSettled([...sources].map((source) => source.idle()));
      await Promise.allSettled(
        [...messengers].map((messenger) => messenger.idle()),
      );
      await Promise.allSettled([...writes]);
      if (
        Object.keys(sessionLedger?.peek().sourceControl?.entries ?? {}).length
      )
        await sessionLedger
          ?.update((draft) => {
            for (const local of Object.keys(draft.sourceControl?.entries ?? {}))
              stopSource(draft, local, 'connection_closed');
            shutdownSnapshot = draft;
          })
          .catch(() => {});
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
        messaging: messagingCapability?.status ?? {
          available: false,
          reason: 'not_ready',
        },
      };
    },
    get status() {
      let ready = !closed && !closing && connected;
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
    messagingUI(/** @type {unknown} */ context) {
      const capability = messagingCapability;
      const authorized = async () => {
        active();
        if (
          !capability ||
          capability !== messagingCapability ||
          !(await options.authorizeLocal(context))
        )
          throw new Error('messaging unavailable');
        active();
        if (capability !== messagingCapability)
          throw new Error('messaging changed');
        return capability;
      };
      return {
        snapshot: async () => (await authorized()).snapshot(),
        setReceive: async (
          /** @type {{local:string,enabled:boolean}} */ input,
        ) => (await authorized()).setReceive(input),
      };
    },
  };
}
