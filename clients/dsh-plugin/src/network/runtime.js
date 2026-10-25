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
import { digest } from './trust/verifier.js';

/** @param {unknown} error */
export function isRevocationError(error) {
  const value = /** @type {{status?:number,code?:string}|null} */ (error);
  return (
    value?.status === 401 ||
    value?.status === 403 ||
    ['network.revoked', 'network.unauthorized'].includes(value?.code ?? '')
  );
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
  let closed = false;
  let connected = false;
  let connectionEpoch = 0;
  let handshake = Promise.resolve();
  let started = false;
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
  /** @param {string} name */
  async function handle(name) {
    const value = options.stores?.[name] ?? (await storeAt(name).open());
    handles.push(value);
    return value;
  }
  try {
    ledger = await openLedger({
      store: options.stores?.ledger ?? storeAt('host'),
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
      store:
        options.stores?.consent ??
        createFileConsentStore({ dir: options.directory ?? '' }),
      identity,
      resolveSource: ports.resolveSource,
      organization: ports.organization,
      authorizeLocal: async (context) =>
        !closed && (await options.authorizeLocal(context)),
      now: options.now,
      ...options.consentTiming,
    });
    resources.push(() => consent.close());
    const outbound = await handle('outbound');
    const intake = await handle('intake');
    /** @type {Record<string, any>} */
    let submissions = (await outbound.load()) ?? {};
    /** @type {Record<string, any>} */
    let intakes = (await intake.load()) ?? {};
    let writes = Promise.resolve();
    /** Serialize both snapshots, and poison the runtime after uncertain durable writes.
     * @param {()=>Promise<any>} operation */
    function write(operation) {
      const work = writes.then(() => {
        active();
        return operation();
      });
      writes = work.then(
        () => {},
        () => {
          void close().catch(() => {});
        },
      );
      return work;
    }
    /** @param {import('../generated/network/SubmitRequest.js').SubmitRequest} request */
    async function submit(request) {
      active();
      const key = `${request.idempotency.producer_epoch}:${request.idempotency.key}`;
      await write(async () => {
        if (Object.keys(submissions).length >= 256)
          throw new Error('outbound capacity exhausted');
        submissions[key] = {
          request: structuredClone(request),
          status: 'pending',
        };
        await outbound.commit(submissions);
      });
      active();
      const result = await ports.submit(
        structuredClone(request),
        controller.signal,
      );
      active();
      await write(async () => {
        delete submissions[key];
        await outbound.commit(submissions);
      });
      return result;
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
          const entry = Object.values(intakes).find((value) =>
            value.recipientIds.includes(input.recipientId),
          );
          if (!entry) throw new Error('verified execution request unavailable');
          let result;
          await consent.withAuthorization(
            {
              ...entry.request,
              layer: 'execution',
              requestId: input.recipientId,
            },
            async () => {
              active();
              if (!connected) throw new Error('network offline');
              result = await ports.network.admit(input);
              active();
              if (!connected || session.epoch !== input.connectionEpoch)
                throw new Error('network admission epoch changed');
            },
          );
          active();
          if (!result) throw new Error('execution authorization required');
          return result;
        },
      },
    });
    /** @param {any[]} items */
    async function verifiedIntake(items) {
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
        if (adapter.inboundPolicy.isBlocked(from)) continue;
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
        const at = options.now?.() ?? Date.now();
        await write(async () => {
          for (const [id, row] of Object.entries(intakes))
            if (row.request.expiresAt <= at) delete intakes[id];
          if (!intakes[key] && Object.keys(intakes).length >= 256)
            throw new Error('intake capacity exhausted');
          if (intakes[key] && intakes[key].request.digest !== request.digest)
            throw new Error('intake digest conflict');
          intakes[key] = {
            request,
            preview:
              raw.type === 'contact_prompt'
                ? String(raw.preview).slice(0, 280)
                : (intakes[key]?.preview ?? ''),
            recipientIds:
              raw.type === 'delivery'
                ? raw.recipients.map((/** @type {any} */ r) => r.recipient_id)
                : [raw.recipient.recipient_id],
            contact:
              raw.type === 'contact_prompt'
                ? raw
                : (intakes[key]?.contact ?? null),
          };
          await intake.commit(intakes);
        });
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
          await consent.request(request);
        }
      }
    }
    /** Native UI actions publish only durable consent events through a trusted
     * control port, never a model tool or browser-provided authority. */
    async function flushConsent(/** @type {unknown} */ context) {
      active();
      const events = await consent.listOutbox(context);
      if (!events.length) return;
      await ports.publishConsent(
        events,
        structuredClone(intakes),
        controller.signal,
      );
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
        await transport.stop();
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
          const entry = Object.values(intakes).find((value) =>
            value.recipientIds.some((/** @type {string} */ id) =>
              item.recipients.some((r) => r.recipientId === id),
            ),
          );
          if (!entry || adapter.inboundPolicy.isBlocked(item.message.from))
            continue;
          try {
            await consent.withAuthorization(entry.request, async () => {
              active();
              if (!connected) throw new Error('network offline');
              receipts.push(
                ...(await adapter.deliver({ ...page, items: [item] })),
              );
            });
          } catch {
            /* Pending/denied consent never enters the host or becomes a false receipt. */
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
            void close().catch(() => {});
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
          session.disconnect();
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
            const pending = snapshot.locals[snapshot.localQueue[0]];
            await session.start({
              localHighWater: snapshot.localHighWater,
              receiptCheckpoint: {
                checkpoint_seq: session.resumeCursor,
                checkpoint_revision: String(snapshot.revision),
                summary_digest: digest(snapshot.recipients),
              },
              pendingRegistration: pending
                ? {
                    candidate_local: pending.local,
                    allocation_op_id: pending.opId,
                    allocation_fingerprint: 'sha256:' + pending.fingerprint,
                  }
                : null,
            });
            active();
            if (expectedEpoch !== connectionEpoch) return;
            connected = true;
            await adapter.recoverLocalRegistrations();
            await session.pull();
            for (const [key, value] of Object.entries(submissions)) {
              if (!ports.reconcileSubmission) continue;
              const resolved = await ports.reconcileSubmission(
                value.request,
                controller.signal,
              );
              active();
              if (resolved)
                await write(async () => {
                  delete submissions[key];
                  await outbound.commit(submissions);
                });
            }
          })
          .catch(() => {
            connected = false;
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
            if (closed || !connected)
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
    function close() {
      if (closing) return closing;
      closed = true;
      connected = false;
      controller.abort();
      for (const dispose of disposers.splice(0)) dispose();
      closing = (async () => {
        await session.stop();
        listeners.clear();
        await handshake.catch(() => {});
        await writes;
        await consent.close();
        await ledger.close();
        await Promise.all(handles.map((h) => h.close()));
      })();
      return closing;
    }
    return {
      session,
      adapter,
      consent,
      get status() {
        return {
          available: true,
          connected: !closed && connected,
          state: closed ? 'stopped' : transport.state,
        };
      },
      get diagnostics() {
        return { trust: trust.diagnostics, session: session.diagnostics };
      },
      async start() {
        active();
        if (started) throw new Error('network runtime already started');
        started = true;
        register();
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
            const row =
              intakes[JSON.stringify([request.layer, request.requestId])];
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
