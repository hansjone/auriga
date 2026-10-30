import {
  createNativeHostPort,
  registrationSession,
} from '../host/native/host-port.js';
import { createAgentSendTool, mapSendReceipt } from '../tools/send.js';
import { createDurableOutbox } from './outbox.js';
import { createDurableInbox } from './inbox.js';
import { createMailboxClient } from './client.js';
import { formatAddress } from '../host/address.js';
import { decodeJson } from '../session/frames.js';
import { canonicalize } from '../trust/jcs.js';
import {
  observeTool,
  sourceEvent,
  sourceReject,
  sourceStage,
} from '../../observability/source.js';

/** Small default-device capability; all identities come from native execution,
 * typed-local bindings and the verified source controller, never tool input.
 * @param {{ctx:any,ledger:import('../host/ledger.js').Ledger,prepared:import('../runtime-types.js').PreparedConnection,source:ReturnType<typeof import('../source-control/controller.js').createSourceControl>,transport:import('../session/memory-pipe.js').TransportLike,ready:any,trust:Awaited<ReturnType<typeof import('../trust/store.js').openTrustStore>>,signal:AbortSignal,assertCurrent:()=>void,now:()=>number}} options */
export function createDeviceMessaging(options) {
  const {
    ctx,
    ledger,
    prepared,
    source,
    transport,
    signal,
    assertCurrent,
    now,
  } = options;
  let closed = false,
    reason = 'not_ready';
  /** @type {(()=>void)|undefined} */ let unregister;
  /** @type {(()=>void)|undefined} */ let off;
  /** @type {ReturnType<typeof setInterval>|undefined} */ let timer;
  /** @type {ReturnType<typeof createDurableInbox>|undefined} */ let inbox;
  /** @type {Set<Promise<unknown>>} */ const work = new Set();
  let pulling = false;
  const current = () => {
    if (closed) throw new Error('messaging retired');
    signal.throwIfAborted();
    assertCurrent();
  };
  /** @param {Promise<unknown>} task */
  function track(task) {
    work.add(task);
    void task
      .catch((error) => {
        reason =
          error instanceof Error ? error.message.split(':')[0] : 'unavailable';
      })
      .finally(() => work.delete(task));
  }
  const client = createMailboxClient({
    ...options,
    identity: ledger.identity,
    assertCurrent: current,
  });
  const outbox = createDurableOutbox({
    ledger,
    submit: async (request, active) => {
      current();
      if (!prepared.submit) throw new Error('device submit unavailable');
      return prepared.submit(request, active);
    },
    assertCurrent: current,
    signal,
    now,
  });
  function pull() {
    if (closed || pulling || !inbox) return;
    pulling = true;
    track(
      inbox
        .pull()
        .then(() => {
          reason = 'ready';
        })
        .finally(() => {
          pulling = false;
        }),
    );
  }
  return {
    async start() {
      current();
      if (!ledger.peek().durable || !prepared.submit) return;
      const host = await createNativeHostPort({ ctx });
      current();
      if (
        !host.getDiagnostics().capabilities.durability.available ||
        !host.getDiagnostics().capabilities.source.available ||
        typeof ctx.tools?.register !== 'function'
      )
        throw new Error('network.host_incompatible');
      inbox = createDurableInbox({
        ...options,
        host,
        client,
        assertCurrent: current,
      });
      const tool = createAgentSendTool({
        ledger,
        localAddress: (local) => ({
          version: 1,
          realm: ledger.identity.realm,
          node: ledger.identity.node,
          local,
          type: 'session',
        }),
        async sourceFor({ hostSessionId, address }) {
          sourceStage('source_native_registration');
          const n = registrationSession(ctx, {
            agent: ctx.agents.get(hostSessionId),
          });
          sourceStage('source_generation');
          current();
          sourceStage('source_native_check');
          n.check();
          sourceStage('source_refresh');
          await source.refresh(address.local);
          sourceEvent('refresh completed');
          sourceStage('source_generation_after_refresh');
          current();
          sourceStage('source_native_after_refresh');
          n.check();
          sourceStage('source_observation');
          const state = ledger.peek(),
            entry = state.sourceControl?.entries[address.local],
            observation = entry?.observation;
          if (!entry) {
            sourceReject('entry_missing');
            throw new Error('network.unauthorized');
          }
          if (entry.stopped) {
            sourceReject('entry_stopped');
            throw new Error('network.unauthorized');
          }
          if (!observation) {
            sourceReject('observation_missing');
            throw new Error('network.unauthorized');
          }
          if (
            (observation.source.state !== 'active' &&
              sourceReject('source_inactive')) ||
            (observation.source.source_binding !== entry.head &&
              sourceReject('head_mismatch')) ||
            (now() >= Date.parse(observation.valid_until ?? '') &&
              sourceReject('observation_expired')) ||
            (state.bindings[address.local]?.hostSessionId !== hostSessionId &&
              sourceReject('session_mismatch'))
          )
            throw new Error('network.unauthorized');
          return {
            sourceBinding: observation.source.source_binding,
            producerEpoch: state.durable?.producerEpoch ?? '',
            address,
            senderKind: 'agent',
            sendAllowed: true,
            expiresAt: Date.parse(observation.valid_until ?? ''),
          };
        },
        // The HTTPS server resolves and authorizes the actual registration.
        // The client accepts only the ordinary-session address form here.
        resolveTarget: (target) =>
          target.node !== '0' && target.local !== '0'
            ? { version: 1, ...target, type: 'session' }
            : null,
        releaseResult: async () => {},
        rateLimit: { messages: 60, localMessages: 30, windowMs: 60000 },
        now,
      });
      unregister = ctx.tools.register({
        name: 'auriga_send_message',
        description:
          'Send text to an online Auriga session that explicitly enabled receiving. A submitted result is not a host delivery receipt.',
        parameters: {
          type: 'object',
          properties: {
            target: { type: 'string' },
            body: {
              type: 'object',
              properties: {
                subtype: { type: 'string', enum: ['text'] },
                text: { type: 'string' },
              },
              required: ['subtype', 'text'],
              additionalProperties: false,
            },
            thread: { type: 'string' },
          },
          required: ['target', 'body'],
          additionalProperties: false,
        },
        output: {
          schema: {
            type: 'object',
            properties: {
              status: { type: 'string', enum: ['request_submitted', 'error'] },
              code: { type: 'string' },
            },
            required: ['status'],
            additionalProperties: false,
          },
          render: (
            /** @type {unknown} */ _input,
            /** @type {unknown} */ value,
          ) => [{ type: 'text', text: JSON.stringify(value) }],
        },
        execute(/** @type {unknown} */ input, /** @type {any} */ execution) {
          return observeTool(async () => {
            let outboxStarted = false;
            try {
              sourceStage('execution_generation');
              current();
              sourceStage('execution_native_registration');
              const n = registrationSession(ctx, execution);
              const request = await tool.prepare(input, execution);
              sourceStage('execution_fence');
              current();
              n.check();
              execution.signal?.throwIfAborted();
              if (request.kind !== 'message' || request.to.type !== 'session')
                throw new Error('network.protocol_error');
              const b = Object.values(ledger.peek().bindings).find(
                (v) => v.hostSessionId === n.id,
              );
              if (!b) throw new Error('network.local_registration_required');
              const semantic = (
                /** @type {import('../../generated/network/SubmitRequest.js').SubmitRequest} */ value,
              ) =>
                canonicalize({
                  kind: value.kind,
                  to: value.to,
                  body: value.body,
                  thread: value.thread_id ?? null,
                  intent: value.intent,
                  durability: value.durability,
                });
              const pending = Object.values(
                ledger.peek().durable?.submissions ?? {},
              ).filter(
                (value) =>
                  !value.result &&
                  value.bindingId === b.bindingId &&
                  semantic(value.request) === semantic(request),
              );
              if (pending.length > 1) throw new Error('network.conflict');
              sourceStage('outbox');
              outboxStarted = true;
              const result = await outbox.send(
                pending[0]?.request ?? { ...request, dispatch_mode: 'atomic' },
                b.bindingId,
                execution.signal,
                () => {
                  current();
                  n.check();
                  const latest = ledger.peek();
                  if (
                    latest.bindings[b.local]?.bindingId !== b.bindingId ||
                    latest.sourceControl?.entries[b.local]?.stopped
                  )
                    throw new Error('network.unauthorized');
                },
              );
              current();
              n.check();
              return mapSendReceipt(result);
            } catch (error) {
              sourceEvent(
                'tool attempt failed',
                { outbox: outboxStarted ? 'started' : 'not_started' },
                error,
              );
              return {
                status: 'error',
                code:
                  /** @type {any} */ (error)?.code ?? 'network.outcome_unknown',
              };
            }
          });
        },
      });
      off = transport.onMessage((bytes) => {
        const frame = decodeJson(bytes);
        if (frame.type === 'head')
          track(client.verify(frame, null).then(() => pull()));
      });
      await inbox.recover();
      current();
      track(outbox.recover());
      timer = setInterval(pull, 1000);
      timer.unref?.();
      reason = 'ready';
      pull();
    },
    async snapshot() {
      current();
      if (!inbox) throw new Error('messaging unavailable');
      return {
        sessions: inbox.snapshot().map((value) => ({
          ...value,
          address: formatAddress({
            realm: ledger.identity.realm,
            node: ledger.identity.node,
            local: value.local,
          }),
        })),
        reason,
        failures: inbox.failures(),
      };
    },
    /** Only the authenticated local UI/control boundary exposes this method.
     * @param {{local:string,enabled:boolean}} input */
    async setReceive(input) {
      current();
      if (
        !inbox ||
        !input ||
        Object.keys(input).sort().join(',') !== 'enabled,local' ||
        typeof input.enabled !== 'boolean' ||
        typeof input.local !== 'string'
      )
        throw new Error('invalid receive setting');
      const result = await inbox.setReceive(input.local, input.enabled);
      current();
      if (input.enabled) pull();
      return result;
    },
    close() {
      closed = true;
      unregister?.();
      off?.();
      clearInterval(timer);
    },
    async idle() {
      while (work.size) await Promise.allSettled([...work]);
    },
    get status() {
      return {
        available: !closed && !!unregister,
        reason,
        failures: inbox?.failures() ?? [],
      };
    },
  };
}
