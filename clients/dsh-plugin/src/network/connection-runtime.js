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
          if (
            frame.local_high_water !== state.localHighWater ||
            frame.registration_revision !== state.registrationRevision ||
            frame.committed_cursor !== '0' ||
            BigInt(frame.retention_low_seq) > BigInt(frame.head_seq) ||
            (frame.seen_checkpoint === null
              ? state.revision !== 0
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
    return result;
  } finally {
    finished = true;
    off();
    clearTimeout(timer);
    signal.removeEventListener('abort', abort);
  }
}

/** Connection-only capability. Owns no HostAdapter, consent, source, FETCH,
 * receipt, registration or SEND. One writer lock spans C1, commit and C2.
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
      epoch: 0,
      attempt,
      listeners: /** @type {Set<(bytes:Uint8Array,epoch:number)=>void>} */ (
        new Set()
      ),
      transport: /** @type {any} */ (undefined),
      stop() {
        entry.live = false;
        entry.attempt.abort();
        entry.listeners.clear();
        return (stopping ??= Promise.resolve(entry.transport.stop()));
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
      async getTicket() {
        try {
          valid();
          await refreshTrust(trust);
          valid();
          const ticket = await prepared.getTicket(signal);
          valid();
          return ticket;
        } catch (error) {
          const e = /** @type {any} */ (error);
          if ([401, 403].includes(e?.status)) {
            options.onRevoked?.();
            void close('revoked').catch(() => {});
          } else if (![429, 503].includes(e?.status))
            void close('unavailable').catch(() => {});
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
        if (entry.receiving) {
          void close('message_backpressure').catch(() => {});
          return;
        }
        entry.receiving = true;
        const receive = async () => {
          const frame = decodeJson(bytes),
            expected = transport.epoch;
          if (frame.keyset_announcement) {
            const resume = connected;
            connected = false;
            await trust.announce(frame.keyset_announcement);
            valid();
            await refreshTrust(trust);
            valid();
            if (transport.epoch !== expected || transport.state !== 'ready')
              throw new Error('announcement connection changed');
            trust.assertUsable();
            connected = resume;
          }
          valid();
          for (const listener of entry.listeners) listener(bytes, epoch);
        };
        void receive()
          .catch(() => {
            void close('unavailable').catch(() => {});
          })
          .finally(() => {
            entry.receiving = false;
          });
      },
      onState(state) {
        if (!entry.live || closed || currentConnection !== entry) return;
        if (state.state !== 'ready' || state.error) {
          connected = false;
          entry.attempt.abort();
          return;
        }
        if (state.epoch === entry.epoch) return;
        entry.epoch = state.epoch;
        connected = false;
        entry.attempt.abort();
        entry.attempt = new AbortController();
        if (!normal) return;
        const handshakeSignal = AbortSignal.any([signal, entry.attempt.signal]);
        handshakes = handshakes
          .then(async () => {
            valid();
            handshakeSignal.throwIfAborted();
            if (!snapshot)
              throw new Error('complete connection ledger required');
            await normalReady(facade, trust, snapshot, handshakeSignal, now);
            valid();
            handshakeSignal.throwIfAborted();
            connected = true;
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
    return { entry, transport, facade };
  }
  /** @type {import('./host/ledger.js').LedgerState|undefined} */
  let snapshot;
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
      snapshot = ledger.state;
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
    controller.abort();
    clearTimeout(deadline);
    options.signal?.removeEventListener('abort', onAbort);
    const stopping = [...connections].map((entry) => entry.stop());
    closing = (async () => {
      await Promise.allSettled(stopping);
      await Promise.allSettled([work, handshakes]);
      await Promise.allSettled([...writes]);
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
    get status() {
      return {
        available: !closed,
        connected: !closed && connected,
        isolation: networkIsolation,
        state: closed
          ? reason === 'stopped'
            ? 'stopped'
            : 'unavailable'
          : connected
            ? 'ready'
            : 'connecting',
      };
    },
    consentUI(/** @type {unknown} */ _context) {
      throw new Error('network consent unavailable');
    },
  };
}
