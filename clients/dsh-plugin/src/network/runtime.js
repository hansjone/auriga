/* global structuredClone */
import { createHash, randomUUID } from 'node:crypto';
import { join } from 'node:path';
import { NatsTransport, createWsSocketFactory } from './transport/index.js';
import { openTrustStore } from './trust/store.js';
import { openLedger } from './host/ledger.js';
import { createFileLedgerStore } from './host/store.js';
import { createHostAdapter } from './host/adapter.js';
import { openConsent, createFileConsentStore } from './consent/index.js';
import { createNetworkSession } from './session/session.js';
import { createAgentSendTool } from './tools/send.js';
import { createConsentUIAdapter } from '../consent-ui/adapter.js';
import { parseAddress } from './host/address.js';
import { SessionError } from './session/errors.js';
import { openPendingBuffer } from './pending-buffer.js';
import { canonicalize } from './trust/jcs.js';

/** @param {unknown} error */
export function isRevocationError(error) {
  const value = /** @type {{status?:number,code?:string}|null} */ (error);
  return (
    value?.status === 401 ||
    value?.status === 403 ||
    ['network.revoked', 'network.unauthorized'].includes(value?.code ?? '')
  );
}

/** Stream bounded records rather than applying the wire JSON value budget to
 * the whole durable ledger. Normal mailbox history may exceed one wire frame.
 * @param {Record<string,unknown>} recipients */
function receiptSummary(recipients) {
  const hash = createHash('sha256').update('{');
  let first = true;
  for (const id of Object.keys(recipients).sort()) {
    if (!first) hash.update(',');
    first = false;
    hash
      .update(canonicalize(id))
      .update(':')
      .update(canonicalize(recipients[id]));
  }
  hash.update('}');
  return 'sha256:' + hash.digest('hex');
}

/** IAM issues the random ticket; its hash is the gateway's private ingress.
 * This function never accepts subjects or credentials from UI/model input.
 * @param {{tenant:string,node:string,device:string,ticket:string,expiresAtMs:number}} value */
export function ticketCredentials(value) {
  if (
    !value ||
    !/^[A-Za-z0-9_-]{43}$/.test(value.ticket) ||
    !value.tenant ||
    !value.device ||
    !/^[1-9][0-9]{0,19}$/.test(value.node) ||
    !Number.isFinite(value.expiresAtMs) ||
    value.expiresAtMs <= Date.now()
  )
    throw new Error('invalid network ticket');
  const bytes = createHash('sha256')
    .update(value.ticket, 'ascii')
    .digest()
    .subarray(0, 16);
  const alphabet = 'abcdefghijklmnopqrstuvwxyz234567';
  let bits = 0,
    accumulator = 0,
    ingress = '';
  for (const byte of bytes) {
    accumulator = (accumulator << 8) | byte;
    bits += 8;
    while (bits >= 5) {
      bits -= 5;
      ingress += alphabet[(accumulator >>> bits) & 31];
    }
  }
  if (bits) ingress += alphabet[(accumulator << (5 - bits)) & 31];
  return {
    auth: {
      token: JSON.stringify({
        tenant: value.tenant,
        node: value.node,
        device: value.device,
        ticket: value.ticket,
      }),
    },
    expiresAtMs: value.expiresAtMs,
    subjects: {
      upSubject: `auriga.net.up.${ingress}`,
      downSubject: `auriga.net.down.${ingress}`,
      inboxPrefix: `auriga.net.reply.${ingress}`,
    },
  };
}

/** One installation/node, shared by all native sessions. The trusted provider
 * implements missing IAM HTTPS/control ports; no guessed API paths are used.
 * @param {import('./runtime-types.js').RuntimeOptions} options */
export async function openNetworkRuntime(options) {
  const { prepared, ports, ctx } = options;
  const required = [
    ports?.prepareTicket,
    ports?.submit,
    ports?.resolveSource,
    ports?.organization,
    ports?.organizationPolicy,
    ports?.sourceFor,
    ports?.resolveTarget,
    ports?.releaseResult,
    ports?.publishConsent,
    ports?.host?.getSession,
    ports?.host?.createSession,
    ports?.host?.prompt,
    ports?.host?.hasPromptRequest,
    ports?.host?.flush,
    ports?.network?.admit,
    ports?.network?.registerLocal,
    ports?.network?.assignContact,
  ];
  if (required.some((port) => typeof port !== 'function'))
    throw new Error('trusted network control/host ports required');
  const identity = Object.freeze({ ...prepared.identity });
  if (!options.directory && !options.stores)
    throw new Error('private network directory required');
  const url = new URL(prepared.url);
  if (
    url.protocol !== 'wss:' ||
    url.username ||
    url.password ||
    url.search ||
    url.hash
  )
    throw new Error('trusted WSS network endpoint required');
  const controller = new AbortController();
  /** @type {import('./host/store.js').LedgerStoreHandle[]} */
  const handles = [];
  /** @type {Array<()=>Promise<void>>} */
  const resources = [];
  /** @type {Array<()=>void>} */
  const disposers = [];
  function unregister() {
    for (const dispose of disposers.splice(0)) {
      try {
        dispose();
      } catch {
        /* Continue releasing the remaining tools. */
      }
    }
  }
  let closed = false;
  let connected = false;
  let connectionEpoch = 0;
  let handshake = Promise.resolve();
  let started = false;
  let assembled = false;
  let closeReason = 'stopped';
  const controlContext = Object.freeze({});
  const clock = options.now ?? Date.now;
  /** @type {Record<string,number>} */
  const counters = {};
  /** @type {Array<{code:string,reason:string}>} */
  const recent = [];
  /** @param {string} reason */
  function audit(reason) {
    counters[reason] = (counters[reason] ?? 0) + 1;
    recent.push({ code: 'network.target_unavailable', reason });
    if (recent.length > 64) recent.shift();
  }
  /** @type {Promise<void>|undefined} */
  let closing;
  const bootId = randomUUID();
  /** @type {any} */
  let transport;
  /** @type {ReturnType<typeof createNetworkSession>} */
  let session;
  /** @type {Awaited<ReturnType<typeof openConsent>>} */
  let consent;
  /** @type {Awaited<ReturnType<typeof openLedger>>} */
  let ledger;
  const active = () => {
    if (closed) throw new Error('network runtime closed');
  };
  const storeAt = (/** @type {string} */ name) =>
    createFileLedgerStore({ dir: join(options.directory ?? '', name) });
  let onStorageFailure = () => {};
  /** Host/consent commits poison their service instances after uncertain writes.
   * @param {{open():Promise<import('./host/store.js').LedgerStoreHandle>}} store */
  function guardedStore(store) {
    return {
      async open() {
        const value = await store.open();
        return {
          load: () => value.load(),
          close: () => value.close(),
          async commit(/** @type {unknown} */ input) {
            try {
              await value.commit(input);
            } catch (error) {
              onStorageFailure();
              throw error;
            }
          },
          ...(value.abandon ? { abandon: () => value.abandon?.() } : {}),
        };
      },
    };
  }
  /** @param {string} name */
  async function handle(name) {
    const value = options.stores?.[name] ?? (await storeAt(name).open());
    handles.push(value);
    return value;
  }
  try {
    onStorageFailure = () => {
      audit('storage_failure');
      if (assembled) void close('storage_failure').catch(() => {});
    };
    ledger = await openLedger({
      store: guardedStore(options.stores?.ledger ?? storeAt('host')),
      identity,
      initialize: prepared.initialize === true,
      now: options.now
        ? () => new Date(options.now?.() ?? Date.now())
        : undefined,
    });
    resources.push(() => ledger.close());
    const cache = await handle('trust');
    const refreshKeys = ports.refreshKeys;
    const trust = await openTrustStore({
      cache,
      roots: prepared.roots,
      config: prepared.keyset,
      now: options.now,
      refresh: refreshKeys
        ? (hint) => refreshKeys(hint, controller.signal)
        : undefined,
    });
    consent = await openConsent({
      store: guardedStore(
        options.stores?.consent ??
          createFileConsentStore({ dir: options.directory ?? '' }),
      ),
      identity,
      resolveSource: ports.resolveSource,
      organization: ports.organization,
      authorizeLocal: async (context) =>
        !closed &&
        (context === controlContext || (await options.authorizeLocal(context))),
      now: options.now,
      ...options.consentTiming,
    });
    resources.push(() => consent.close());
    const onCommitFailure = () => onStorageFailure();
    const submissions = await openPendingBuffer({
      handle: await handle('outbound'),
      now: clock,
      limit: 256,
      perOwner: 16,
      ttlMs: 600000,
      onCommitFailure,
      onDrop: () => audit('outbound_unknown_expired'),
      // Legacy records have no trustworthy age/retry budget. They are never replayed.
    });
    const sourceKey = (/** @type {any} */ value) =>
      JSON.stringify([
        value.request.source.tenant,
        value.request.source.realm,
        value.request.source.node,
      ]);
    const intakes = await openPendingBuffer({
      handle: await handle('intake'),
      now: clock,
      limit: 256,
      perOwner: 8,
      ttlMs: 86400000,
      onCommitFailure,
      onDrop: () => audit('intake_expired_or_trimmed'),
      migrate: (value) =>
        value?.request?.source && Array.isArray(value.recipientIds)
          ? {
              value: {
                request: value.request,
                recipientIds: value.recipientIds,
                preview: String(value.preview ?? '').slice(0, 280),
              },
              owner: sourceKey(value),
              expiresAt: Math.min(value.request.expiresAt, clock() + 86400000),
              attempts: 0,
            }
          : undefined,
    });
    /** A single verified page only; ordinary accepted traffic is not retained for its TTL. */
    const pageIntakes = new Map();
    /** @param {string} recipientId */
    const findIntake = (recipientId) =>
      [
        ...pageIntakes.values(),
        ...intakes.entries().map(([, value]) => value),
      ].find((value) => value.recipientIds.includes(recipientId));
    /** @param {any} entry */
    async function retain(entry) {
      active();
      return intakes.put(
        JSON.stringify([entry.request.layer, entry.request.requestId]),
        entry,
        sourceKey(entry),
        entry.request.expiresAt,
      );
    }
    /** @param {any} error */
    const definitive = (error) =>
      error?.definitive === true ||
      (error?.status >= 400 &&
        error.status < 500 &&
        ![408, 429].includes(error.status));
    /** @param {import('../generated/network/SubmitRequest.js').SubmitRequest} request */
    async function submit(request) {
      active();
      const key = `${request.idempotency.producer_epoch}:${request.idempotency.key}`;
      if (submissions.get(key))
        throw new SessionError(
          'network.outcome_unknown',
          'submission already pending',
        );
      if (
        !(await submissions.put(
          key,
          { request: structuredClone(request) },
          ('source_binding' in request.source
            ? request.source.source_binding
            : undefined) ?? 'human',
        ))
      ) {
        audit('outbound_backpressure');
        throw new SessionError(
          'network.quota_exceeded',
          'outbound metadata backpressure',
        );
      }
      active();
      try {
        const result = await ports.submit(
          structuredClone(request),
          controller.signal,
        );
        active();
        await submissions.remove([key]);
        return result;
      } catch (error) {
        if (!closed && definitive(error)) await submissions.remove([key]);
        throw error;
      }
    }
    /** @type {import('./host/adapter.js').HostPort} */
    const host = {
      getSession: (id) => ports.host.getSession(id),
      hasPromptRequest: (id, requestId) =>
        ports.host.hasPromptRequest(id, requestId),
      flush: (id) => ports.host.flush(id),
      ...(ports.host.hasDurableProvider
        ? {
            hasDurableProvider: (id) =>
              ports.host.hasDurableProvider?.(id) ?? Promise.resolve(false),
          }
        : {}),
      async prompt(id, input) {
        active();
        if (!connected) throw new Error('network offline');
        const admitted = Object.values(ledger.peek().recipients).some(
          (row) =>
            row.hostSessionId === id &&
            row.hostRequestId === input.requestId &&
            row.state === 'INVOCATION_STARTED' &&
            row.admissionEpoch === session.epoch,
        );
        if (!admitted) throw new Error('network admission epoch changed');
        return ports.host.prompt(id, input);
      },
      async createSession(input) {
        active();
        if (!connected) throw new Error('network offline');
        return ports.host.createSession(input);
      },
    };
    const adapter = createHostAdapter({
      ledger,
      host,
      network: {
        registerLocal: (entry) => ports.network.registerLocal(entry),
        assignContact: (input) => ports.network.assignContact(input),
        ...(ports.network.queryAdmission
          ? {
              queryAdmission: (input) =>
                ports.network.queryAdmission?.(input) ??
                Promise.resolve({ status: /** @type {const} */ ('none') }),
            }
          : {}),
        async admit(input) {
          active();
          if (!connected || session.epoch !== input.connectionEpoch)
            throw new Error('network offline or epoch changed');
          const entry = findIntake(input.recipientId);
          if (!entry) throw new Error('verified execution request unavailable');
          const execution = {
            ...entry,
            request: {
              ...entry.request,
              layer: 'execution',
              requestId: input.recipientId,
            },
          };
          if (!(await retain(execution))) {
            audit('execution_backpressure');
            return { status: 'denied', code: 'network.target_unavailable' };
          }
          let result;
          try {
            await consent.withAuthorization(execution.request, async () => {
              active();
              if (!connected) throw new Error('network offline');
              result = await ports.network.admit(input);
              active();
              if (!connected || session.epoch !== input.connectionEpoch)
                throw new Error('network admission epoch changed');
            });
          } catch (error) {
            const decision = await consent.getRequest(controlContext, {
              layer: 'execution',
              requestId: input.recipientId,
            });
            if (
              decision &&
              ['denied', 'expired', 'revoked'].includes(decision.status)
            ) {
              audit('execution_denied');
              return { status: 'denied', code: 'network.target_unavailable' };
            }
            throw error;
          }
          active();
          if (!result) throw new Error('execution authorization required');
          return result;
        },
      },
    });
    /** @param {any[]} items */
    async function verifiedIntake(items) {
      pageIntakes.clear();
      for (const raw of items) {
        active();
        if (!['delivery', 'contact_prompt'].includes(raw.type)) continue;
        const from = parseAddress(raw.from ?? raw.message?.from, {
          realm: identity.realm,
          role: 'source',
        });
        if (
          raw.source_context?.realm !== from.realm ||
          raw.source_context?.node !== from.node
        )
          throw new SessionError(
            'network.sender_mismatch',
            'verified intake source mismatch',
          );
        if (!/^sha256:[a-f0-9]{64}$/.test(raw.source_context.message_digest))
          throw new SessionError(
            'network.protocol_error',
            'verified intake digest unavailable',
          );
        const request = {
          layer: /** @type {const} */ ('conversation'),
          requestId:
            raw.contact_id ?? raw.contact?.contact_id ?? raw.message.message_id,
          source: {
            tenant: identity.tenant,
            realm: from.realm,
            node: from.node,
          },
          digest: raw.source_context.message_digest.slice(7),
          // Preview envelopes carry no created_at/body length. Reserve a whole
          // wire budget and use the immutable expiry window for both stages.
          createdAt:
            Date.parse(raw.expires_at ?? raw.message.expires_at) - 86400000,
          expiresAt: Date.parse(raw.expires_at ?? raw.message.expires_at),
          bytes: 65536,
        };
        const key = JSON.stringify([request.layer, request.requestId]);
        const old = intakes.get(key);
        const entry = {
          request,
          preview:
            raw.type === 'contact_prompt'
              ? String(raw.preview).slice(0, 280)
              : (old?.preview ?? ''),
          recipientIds:
            raw.type === 'delivery'
              ? raw.recipients.map((/** @type {any} */ r) => r.recipient_id)
              : [raw.recipient.recipient_id],
        };
        pageIntakes.set(key, entry);
        if (raw.type === 'contact_prompt') {
          const attemptId = randomUUID();
          const result = await session.request(
            'attempt_begin',
            {
              epoch: session.epoch,
              boot_id: bootId,
              attempt_id: attemptId,
              delivery_id: raw.delivery_id,
              recipient_id: raw.recipient.recipient_id,
              purpose: 'preview',
            },
            'attempt_result',
          );
          if (
            result.result !== 'entered' ||
            result.attempt_id !== attemptId ||
            result.recipient_id !== raw.recipient.recipient_id
          )
            throw new SessionError(
              'network.protocol_error',
              'preview attempt unavailable',
            );
          const blocked = adapter.inboundPolicy.isBlocked(from);
          const retained = !blocked && (await retain(entry));
          if (blocked || !retained)
            audit(blocked ? 'contact_blocked' : 'intake_backpressure');
          await consent.request(request);
          let decision = await consent.getRequest(controlContext, {
            layer: 'conversation',
            requestId: request.requestId,
          });
          if (
            (blocked || !retained) &&
            decision &&
            !['denied', 'expired', 'revoked'].includes(decision.status)
          ) {
            const settings = await consent.getSettings(controlContext);
            await consent.decide(controlContext, {
              layer: 'conversation',
              requestId: request.requestId,
              choice: 'deny',
              expectedRevision: settings.policyRevision,
            });
            decision = await consent.getRequest(controlContext, {
              layer: 'conversation',
              requestId: request.requestId,
            });
          }
          if (decision?.status === 'denied') audit('contact_denied');
          try {
            await flushConsent(controlContext);
          } catch {
            audit('consent_publish_deferred');
          }
        }
      }
    }
    /** Native UI actions publish only durable consent events through a trusted
     * control port, never a model tool or browser-provided authority. */
    async function flushConsent(/** @type {unknown} */ context) {
      active();
      const events = await consent.listOutbox(context);
      if (!events.length) return;
      /** @type {Record<string,import('./runtime-types.js').ConsentPublication>} */
      const batch = {};
      for (const event of events) {
        if (!event.layer || !event.requestId) continue;
        const key = JSON.stringify([event.layer, event.requestId]);
        const entry = pageIntakes.get(key) ?? intakes.get(key);
        if (entry)
          batch[key] = {
            requestId: event.requestId,
            digest: entry.request.digest,
            recipientIds: [...entry.recipientIds],
          };
      }
      await ports.publishConsent(events, batch, controller.signal);
      active();
      await consent.acknowledgeOutbox(
        context,
        events.map((event) => event.id),
      );
    }
    /** @type {Set<(bytes:Uint8Array,epoch:number)=>void>} */
    const listeners = new Set();
    /** @type {import('./session/memory-pipe.js').TransportLike} */
    const facade = {
      send: (bytes) => transport.send(bytes),
      onMessage(listener) {
        listeners.add(listener);
        return () => listeners.delete(listener);
      },
      async stop() {
        await transport?.stop();
      },
      get state() {
        return transport.state;
      },
      get epoch() {
        return transport.epoch;
      },
    };
    /** @type {typeof adapter} */
    const deliveryAdapter = {
      ...adapter,
      async markCommitted(ids) {
        await adapter.markCommitted(ids);
        const finished = intakes
          .entries()
          .filter(([, entry]) =>
            entry.recipientIds.every(
              (/** @type {string} */ id) =>
                ledger.peek().recipients[id]?.platformCommitted,
            ),
          );
        await intakes.remove(finished.map(([key]) => key));
      },
      async deliver(input) {
        const page = /** @type {import('./host/adapter.js').Page} */ (input);
        const receipts = [];
        for (const item of page.items) {
          active();
          const existing = item.recipients.every((r) =>
            [
              'HOST_ACCEPTED',
              'INBOX_ACCEPTED',
              'REJECTED',
              'QUARANTINED',
              'OUTCOME_UNKNOWN',
            ].includes(ledger.peek().recipients[r.recipientId]?.state),
          );
          if (item.message.kind !== 'first_contact' || existing) {
            receipts.push(
              ...(await adapter.deliver({ ...page, items: [item] })),
            );
            continue;
          }
          const entry = findIntake(item.recipients[0]?.recipientId);
          if (adapter.inboundPolicy.isBlocked(item.message.from)) {
            audit('contact_blocked');
            receipts.push(
              ...(await adapter.reject(page, item, 'source_blocked')),
            );
            continue;
          }
          if (!entry || !(await retain(entry))) {
            audit('intake_backpressure');
            receipts.push(
              ...(await adapter.reject(page, item, 'intake_capacity')),
            );
            continue;
          }
          try {
            await consent.withAuthorization(entry.request, async () => {
              active();
              if (!connected) throw new Error('network offline');
              receipts.push(
                ...(await adapter.deliver({ ...page, items: [item] })),
              );
            });
          } catch {
            const decision = await consent.getRequest(controlContext, {
              layer: 'conversation',
              requestId: entry.request.requestId,
            });
            if (
              decision &&
              ['denied', 'expired', 'revoked'].includes(decision.status)
            ) {
              audit('contact_denied');
              receipts.push(
                ...(await adapter.reject(page, item, 'consent_denied')),
              );
              try {
                await flushConsent(controlContext);
              } catch {
                audit('consent_publish_deferred');
              }
            } else audit('contact_pending');
          }
        }
        return receipts;
      },
    };
    session = createNetworkSession({
      transport: facade,
      adapter: deliveryAdapter,
      identity,
      bootId,
      trust,
      now: options.now,
      onVerifiedItems: verifiedIntake,
      submit,
      async prepareDelivery(raw) {
        const recipients = [];
        for (const recipient of raw.recipients) {
          const attemptId = randomUUID();
          const result = await session.request(
            'attempt_begin',
            {
              epoch: session.epoch,
              boot_id: bootId,
              attempt_id: attemptId,
              delivery_id: raw.delivery_id,
              recipient_id: recipient.recipient_id,
              purpose: 'process',
            },
            'attempt_result',
          );
          if (
            result.result !== 'entered' ||
            result.attempt_id !== attemptId ||
            result.recipient_id !== recipient.recipient_id
          )
            throw new SessionError(
              'network.protocol_error',
              'network attempt unavailable',
            );
          recipients.push({ ...recipient, attempt_id: attemptId });
        }
        return { ...raw, recipients };
      },
    });
    resources.push(() => session.stop());
    /** @type {import('./transport/nats-transport.js').TransportOptions} */
    const transportOptions = {
      url: url.href,
      // Exact subject names are supplied per ticket before opening the link.
      upSubject: 'auriga.net.up.' + 'a'.repeat(26),
      downSubject: 'auriga.net.down.' + 'a'.repeat(26),
      inboxPrefix: 'auriga.net.reply.' + 'a'.repeat(26),
      createSocket: createWsSocketFactory({ ca: prepared.tls?.ca }),
      async getTicket(reason) {
        active();
        const accessToken = await options.getAccess();
        active();
        let ticket;
        try {
          ticket = await ports.prepareTicket({
            ...reason,
            accessToken,
            identity,
            signal: controller.signal,
          });
        } catch (error) {
          if (isRevocationError(error)) {
            options.onRevoked?.();
            void close('revoked').catch(() => {});
          }
          throw error;
        }
        active();
        if (ticket.tenant !== identity.tenant || ticket.node !== identity.node)
          throw new Error('network ticket identity mismatch');
        return ticketCredentials(ticket);
      },
      onMessage(bytes, epoch) {
        if (!closed && epoch === transport.epoch)
          for (const listener of listeners) listener(bytes, epoch);
      },
      onState(state) {
        if (closed) return;
        if (state.state !== 'ready') {
          connected = false;
          unregister();
          session.disconnect();
          return;
        }
        if (state.error) {
          unregister();
          return;
        }
        if (state.epoch === connectionEpoch) return;
        connectionEpoch = state.epoch;
        connected = false;
        session.disconnect();
        const expectedEpoch = connectionEpoch;
        handshake = handshake
          .catch(() => {})
          .then(async () => {
            active();
            if (expectedEpoch !== connectionEpoch) return;
            const snapshot = ledger.read();
            if (snapshot.identityConflict)
              throw new Error('local recovery conflict');
            const pending =
              snapshot.locals[snapshot.registrations[snapshot.localQueue[0]]];
            await session.start({
              localHighWater: snapshot.localHighWater,
              registrationRevision: snapshot.registrationRevision ?? '0',
              receiptCheckpoint: {
                checkpoint_seq: session.resumeCursor,
                checkpoint_revision: String(snapshot.revision),
                summary_digest: receiptSummary(snapshot.recipients),
              },
              pendingRegistration: pending
                ? {
                    candidate_local: pending.local,
                    allocation_op_id: pending.opId,
                    allocation_fingerprint: 'sha256:' + pending.fingerprint,
                    address_type: pending.addressType,
                  }
                : null,
            });
            active();
            if (expectedEpoch !== connectionEpoch) return;
            connected = true;
            await adapter.recoverLocalRegistrations();
            register();
            await session.pull();
            for (const [key, value] of submissions.entries()) {
              if (!ports.reconcileSubmission) continue;
              if (!(await submissions.claim(key, 3))) continue;
              try {
                const resolved = await ports.reconcileSubmission(
                  value.request,
                  controller.signal,
                );
                active();
                if (resolved) await submissions.remove([key]);
              } catch (error) {
                if (!closed && definitive(error))
                  await submissions.remove([key]);
                audit('reconcile_unknown');
              }
            }
          })
          .catch(() => {
            if (expectedEpoch === connectionEpoch) {
              connected = false;
              unregister();
            }
          });
      },
    };
    transport = (
      options.transportFactory ?? ((value) => new NatsTransport(value))
    )(transportOptions);
    const sendTool = createAgentSendTool({
      ledger,
      localAddress: adapter.localAddress,
      sourceFor: ports.sourceFor,
      resolveTarget: ports.resolveTarget,
      releaseResult: ports.releaseResult,
      rateLimit: prepared.sendRateLimit ?? {
        messages: 60,
        localMessages: 20,
        windowMs: 60000,
      },
      now: options.now,
    });
    const schema = structuredClone(sendTool.inputSchema);
    const parameters = {
      ...schema,
      properties: {
        to: schema.properties.target,
        body: schema.properties.body,
        thread: schema.properties.thread,
      },
      required: schema.required.map((key) => (key === 'target' ? 'to' : key)),
    };
    const register = () => {
      if (disposers.length) return;
      disposers.push(
        ctx.tools.register({
          name: 'auriga_send_message',
          description:
            'Send an Auriga network message from the current authorized native session.',
          parameters,
          output: {
            schema: { type: 'object', additionalProperties: true },
            render: (
              /** @type {unknown} */ _args,
              /** @type {unknown} */ result,
            ) => [{ type: 'text', text: JSON.stringify(result) }],
          },
          async execute(
            /** @type {any} */ input,
            /** @type {import('./tools/send.js').ExecutionContext} */ exec,
          ) {
            if (closed || !connected || !disposers.length)
              return { status: 'error', code: 'network.target_unavailable' };
            if (
              !input ||
              Object.keys(input).some(
                (key) => !['to', 'body', 'thread'].includes(key),
              )
            )
              return { status: 'error', code: 'network.protocol_error' };
            const id = exec?.agent?.session?.id;
            if (!id)
              return {
                status: 'error',
                code: 'network.local_registration_required',
              };
            try {
              const native = await ports.host.getSession(id);
              active();
              if (!native) throw new Error('native session unavailable');
              await adapter.bindExistingSession({
                opId: `host:${id}`,
                hostSessionId: id,
                cwd: native.cwd,
              });
              return await sendTool.execute(
                {
                  target: input.to,
                  body: input.body,
                  ...(input.thread === undefined
                    ? {}
                    : { thread: input.thread }),
                },
                exec,
                session.submit,
              );
            } catch {
              return {
                status: 'error',
                code: 'network.local_registration_required',
              };
            }
          },
        }),
      );
    };
    function close(reason = 'stopped') {
      if (closing) return closing;
      closeReason = reason;
      closed = true;
      connected = false;
      controller.abort();
      unregister();
      closing = (async () => {
        try {
          await session.stop();
        } finally {
          listeners.clear();
          await handshake.catch(() => {});
          await Promise.all([submissions.settled, intakes.settled]);
          await Promise.allSettled([consent.close(), ledger.close()]);
          await Promise.allSettled(handles.map((h) => h.close()));
        }
      })().finally(() => {
        try {
          options.onClosed?.(closeReason);
        } catch {
          /* Observer cannot reopen this instance. */
        }
      });
      return closing;
    }
    assembled = true;
    return {
      session,
      adapter,
      consent,
      get status() {
        return {
          available: !closed,
          isolation: adapter.isolation,
          connected: !closed && connected,
          state: closed
            ? closeReason === 'stopped'
              ? 'stopped'
              : 'unavailable'
            : transport.state,
        };
      },
      get diagnostics() {
        return {
          trust: trust.diagnostics,
          isolation: adapter.isolation,
          session: session.diagnostics,
          runtime: { ...counters },
          events: structuredClone(recent),
          pending: { intake: intakes.size, outbound: submissions.size },
        };
      },
      async start() {
        active();
        if (started) throw new Error('network runtime already started');
        started = true;
        if (!adapter.isolation.isolated)
          ctx.logger?.warn?.(
            '[auriga-dsh] 未隔离（开发阶段） / Unisolated (development): local and managed network isolation is deferred',
          );
        await transport.start();
        await handshake;
      },
      /** Bind every method to the HTTP request; never capture a browser identity string. */
      consentUI(/** @type {unknown} */ context) {
        const ui = createConsentUIAdapter({
          consent,
          context,
          organizationPolicy: ports.organizationPolicy,
          preview: async (request) => {
            const row = intakes.get(
              JSON.stringify([request.layer, request.requestId]),
            );
            return row?.request.digest === request.digest ? row.preview : '';
          },
        });
        return {
          ...ui,
          async snapshot() {
            await flushConsent(context);
            return ui.snapshot();
          },
          async decide(/** @type {Parameters<typeof ui.decide>[0]} */ input) {
            await ui.decide(input);
            await flushConsent(context);
            session.rescan();
          },
          async setDefault(
            /** @type {import('./consent/types.js').Layer} */ layer,
            /** @type {import('./consent/types.js').DefaultMode} */ mode,
          ) {
            await ui.setDefault(layer, mode);
            await flushConsent(context);
            session.rescan();
          },
          async revoke(/** @type {Parameters<typeof ui.revoke>[0]} */ input) {
            await ui.revoke(input);
            await flushConsent(context);
            session.rescan();
          },
        };
      },
      close,
    };
  } catch (error) {
    controller.abort();
    for (const release of resources.reverse()) await release();
    await Promise.all(handles.map((h) => h.close()));
    throw error;
  }
}
