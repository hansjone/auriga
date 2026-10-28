/* global structuredClone */
import { randomUUID } from 'node:crypto';
import { createKeyedMutex } from '../host/keyed-mutex.js';
import { registrationSession } from '../host/native/host-port.js';
import { verifyFetchPage } from '../session/page.js';
import { digest } from '../trust/verifier.js';
import { isUint64String } from '../host/address.js';
import { canonicalize } from '../trust/jcs.js';
import { cursorEvidencePrefix, isTerminalEvidence } from './recovery.js';

/** Explicit, same-tenant ordinary-session intake. HostPort is the public native
 * adapter; it proves provider acceptance before any host_accepted receipt.
 * @param {{ctx:any,ledger:import('../host/ledger.js').Ledger,host:import('../host/adapter.js').HostPort,client:ReturnType<typeof import('./client.js').createMailboxClient>,ready:{epoch:string,channelNonce:string,bootId:string},trust:Awaited<ReturnType<typeof import('../trust/store.js').openTrustStore>>,assertCurrent:()=>void,now?:()=>number}} options */
export function createDurableInbox({
  ctx,
  ledger,
  host,
  client,
  ready,
  trust,
  assertCurrent,
  now = Date.now,
}) {
  const locks = createKeyedMutex();
  /** @type {Map<string,{scope:string,id:string,code:string}>} */
  const failures = new Map();
  const current = () => {
    assertCurrent();
    ledger.assertAvailable();
  };
  /** Keep failures local to their item; a retired connection or unavailable
   * ledger must still stop all work. Diagnostics never include remote text.
   * @param {string} scope @param {string} id @param {()=>Promise<unknown>} run */
  async function isolate(scope, id, run) {
    const key = scope + ':' + id;
    try {
      await run();
      failures.delete(key);
    } catch (error) {
      current();
      const message = error instanceof Error ? error.message : '';
      const code = [
        'receive_none',
        'network.local_registration_required',
        'native binding changed',
        'host durable acceptance unavailable',
        'invalid receive result',
      ].includes(message)
        ? message
        : 'network.outcome_unknown';
      failures.delete(key);
      failures.set(key, { scope, id, code });
      const oldest = failures.keys().next().value;
      if (failures.size > 128 && oldest !== undefined) failures.delete(oldest);
    }
  }
  /** @param {string} local @param {boolean} [receiving] */
  function native(local, receiving = false) {
    current();
    const state = ledger.peek(),
      b = state.bindings[local],
      r = state.locals[local];
    if (
      !b ||
      b.kind !== 'session' ||
      b.state !== 'bound' ||
      b.lifecycle !== 'open' ||
      r?.state !== 'registered' ||
      r.addressType !== 'session'
    )
      throw new Error('network.local_registration_required');
    if (
      receiving &&
      (b.receiveMode === 'receive_none' ||
        !state.durable?.receive[local]?.enabled ||
        state.durable.receive[local].pending?.enabled === false)
    )
      throw new Error('receive_none');
    const agent = ctx.agents.get(b.hostSessionId);
    if (!agent) throw new Error('network.local_registration_required');
    const token = registrationSession(ctx, { agent });
    if (token.id !== b.hostSessionId || token.cwd !== b.cwd)
      throw new Error('native binding changed');
    return { b, r, token };
  }
  /** @param {string} local @param {boolean} enabled @param {AbortSignal} [signal] */
  async function setReceive(local, enabled, signal) {
    return locks.run('receive:' + local, async () => {
      const n = native(local);
      const check = () => {
        current();
        signal?.throwIfAborted();
        n.token.check();
        if (ledger.peek().bindings[local]?.bindingId !== n.b.bindingId)
          throw new Error('binding changed');
      };
      const before = ledger.peek().durable?.receive[local];
      if (before?.pending && before.pending.enabled !== enabled)
        throw new Error('network.conflict: reconcile previous receive setting');
      if (!before?.pending) {
        const registration = {
          registration_id: n.r.registrationId,
          allocation_op_id: n.r.opId,
          allocation_fingerprint: 'sha256:' + n.r.fingerprint,
          registration_revision: n.r.registrationRevision,
          local,
          address_type: 'session',
        };
        await ledger.update((draft) => {
          if (!draft.durable) throw new Error('durable unavailable');
          draft.durable.receive[local] = {
            enabled: before?.enabled ?? false,
            version: before?.version ?? '1',
            pending: {
              registration,
              enabled,
              expected_version: before?.version ?? '1',
              request_id: randomUUID(),
            },
          };
          // Disabling fences local use even when the remote acknowledgement is lost.
          if (!enabled) draft.bindings[local].receiveMode = 'receive_none';
        }, check);
      }
      const pending = ledger.peek().durable?.receive[local].pending;
      const result = await client.request(
        client.frame('receive_set', { ...pending, boot_id: ready.bootId }),
        'receive_set_result',
        signal,
      );
      check();
      if (
        result.registration_id !== n.r.registrationId ||
        result.enabled !== enabled ||
        !isUint64String(result.state_version) ||
        BigInt(result.state_version) !== BigInt(pending.expected_version) + 1n
      )
        throw new Error('invalid receive result');
      await ledger.update((draft) => {
        if (!draft.durable) throw new Error('durable unavailable');
        draft.durable.receive[local] = {
          enabled,
          version: result.state_version,
          pending: null,
        };
        draft.bindings[local].receiveMode = enabled
          ? 'thread_messages'
          : 'receive_none';
      }, check);
      failures.delete('receive:' + local);
      return { local, enabled, version: result.state_version };
    });
  }
  /** @param {string} id @param {(value:import('./types.js').Intake)=>void} change */
  async function change(id, change) {
    await ledger.update((draft) => {
      if (!draft.durable) throw new Error('durable unavailable');
      change(draft.durable.inbound[id]);
    }, current);
  }
  /** Called only with fully authenticated delivery, including source digest.
   * @param {any} item */
  async function deliver(item) {
    const m = item.message;
    if (
      item.type !== 'delivery' ||
      m?.kind !== 'message' ||
      m.message_class !== 'unicast' ||
      m.to?.type !== 'session' ||
      m.to.realm !== ledger.identity.realm ||
      m.to.node !== ledger.identity.node ||
      typeof m.body?.text !== 'string' ||
      m.body.subtype !== 'text' ||
      !Array.isArray(item.recipients) ||
      item.recipients.length !== 1
    )
      throw new Error('unsupported ordinary session delivery');
    const wire = item.recipients[0],
      id = wire.recipient_id;
    if (
      typeof id !== 'string' ||
      wire.local !== m.to.local ||
      wire.type !== 'session' ||
      !isUint64String(wire.state_version) ||
      wire.state_version === '0'
    )
      throw new Error('invalid recipient version/target');
    return locks.run('recipient:' + id, async () => {
      const accepted = ledger.peek().durable?.inbound[id];
      if (accepted?.stage === 'accepted' || accepted?.stage === 'committed') {
        if (
          accepted.delivery.delivery_id !== item.delivery_id ||
          accepted.delivery.source_context.message_digest !==
            item.source_context.message_digest
        )
          throw new Error('recipient conflict');
        // Reconcile an already durable acceptance even after receiving is
        // disabled or the native session closes. This path never invokes it.
        return commitReceipt(id, accepted.delivery);
      }
      const n = native(wire.local);
      const checkIdentity = () => {
        current();
        n.token.check();
        if (
          ledger.peek().bindings[wire.local]?.bindingId !== n.b.bindingId ||
          now() >= Date.parse(m.expires_at)
        )
          throw new Error('recipient fenced or expired');
      };
      checkIdentity();
      const previous = ledger.peek().durable?.inbound[id];
      if (
        previous &&
        (previous.delivery.delivery_id !== item.delivery_id ||
          previous.delivery.source_context.message_digest !==
            item.source_context.message_digest ||
          previous.bindingId !== n.b.bindingId)
      )
        throw new Error('recipient conflict');
      if (!previous)
        await ledger.update((draft) => {
          if (!draft.durable) throw new Error('durable unavailable');
          draft.durable.inbound[id] = {
            delivery: structuredClone(item),
            recipientId: id,
            local: wire.local,
            bindingId: n.b.bindingId,
            hostSessionId: n.b.hostSessionId,
            attemptId: randomUUID(),
            epoch: ready.epoch,
            stateVersion: wire.state_version,
            stage: 'received',
            admission: null,
            receipt: null,
          };
        }, checkIdentity);
      // Retain verified mail before enforcing the receive switch. A disabled
      // session cannot invoke HostPort, but its item remains available to retry.
      const check = () => {
        checkIdentity();
        native(wire.local, true);
      };
      check();
      let r = ledger.peek().durable?.inbound[id];
      if (!r || r.stage === 'committed') return;
      if (r.stateVersion !== wire.state_version && r.stage !== 'accepted')
        throw new Error('recipient state version changed');
      if (r.stage === 'received') {
        // A reconnect before admission uses a new attempt for the new C. The
        // original durable recipient/host_request_id never changes.
        if (r.epoch !== ready.epoch)
          await change(id, (v) => {
            v.attemptId = randomUUID();
            v.epoch = ready.epoch;
          });
        r = ledger.peek().durable?.inbound[id];
        const attempt = await client.request(
          client.frame('attempt_begin', {
            boot_id: ready.bootId,
            delivery_id: item.delivery_id,
            recipient_id: id,
            attempt_id: r?.attemptId,
            purpose: 'process',
          }),
          'attempt_result',
        );
        check();
        if (
          attempt.recipient_id !== id ||
          attempt.attempt_id !== r?.attemptId ||
          attempt.result !== 'entered'
        )
          throw new Error('attempt unavailable');
        await change(id, (v) => {
          v.stage = 'admit_requested';
        });
      }
      r = ledger.peek().durable?.inbound[id];
      if (r?.stage === 'admit_requested') {
        const admit = client.frame('execution_admit', {
          recipient_id: id,
          target: m.to,
          connection_epoch: ready.epoch,
          boot_id: ready.bootId,
          host_request_id: id,
          attempt_id: r.attemptId,
        });
        delete admit.epoch;
        const result = await client.request(admit, 'admit_result');
        check();
        if (
          result.recipient_id !== id ||
          !isUint64String(result.state_version) ||
          result.state_version !== wire.state_version ||
          !['admitted', 'already_admitted'].includes(result.result)
        )
          throw new Error('invalid admission');
        await change(id, (v) => {
          v.admission = result;
          v.stage = 'admitted';
        });
      }
      r = ledger.peek().durable?.inbound[id];
      if (r?.stage === 'admitted' || r?.stage === 'invoking') {
        const exists = await host.hasPromptRequest(n.b.hostSessionId, id);
        check();
        if (!exists) {
          if (
            r.stage === 'invoking' ||
            r.admission.result !== 'admitted' ||
            r.epoch !== ready.epoch ||
            r.admission.admitted_epoch !== ready.epoch
          )
            throw new Error(
              'network.outcome_unknown: admission cannot grant a second invocation',
            );
          await change(id, (v) => {
            v.stage = 'invoking';
          });
          check();
          await host.prompt(n.b.hostSessionId, {
            requestId: id,
            text: `[Auriga external message ${m.message_id}]\n${m.body.text}`,
            mode: 'queue',
          });
          check();
        }
        if (
          (await host.hasDurableProvider?.(n.b.hostSessionId)) !== true ||
          (await host.flush(n.b.hostSessionId)) !== true
        )
          throw new Error('host durable acceptance unavailable');
        check();
        // Evidence is durable before the receipt can leave the plugin.
        await change(id, (v) => {
          v.stage = 'accepted';
        });
      }
      r = ledger.peek().durable?.inbound[id];
      if (r?.stage !== 'accepted') return;
      await commitReceipt(id, item);
    });
  }
  /** @param {string} id @param {any} item */
  async function commitReceipt(id, item) {
    current();
    let r = ledger.peek().durable?.inbound[id];
    if (!r || r.stage === 'committed') return;
    if (r.stage !== 'accepted') throw new Error('durable acceptance required');
    const m = item.message;
    if (!r.receipt) {
      const receipt = client.frame('receipt', {
        operation_id: randomUUID(),
        delivery_id: item.delivery_id,
        mail_seq: item.mail_seq,
        results: [
          {
            recipient_id: id,
            target: m.to,
            message_digest: item.source_context.message_digest,
            host_request_id: id,
            status: 'host_accepted',
            expected_state_version: r.stateVersion,
            accepted_by: 'session',
            acceptance_mode: 'normal',
            evidence_digest: digest({
              recipient_id: id,
              binding_id: r.bindingId,
              host_request_id: id,
              message_digest: item.source_context.message_digest,
              stage: 'accepted',
            }),
          },
        ],
      });
      await change(id, (v) => {
        v.receipt = receipt;
      });
    }
    r = ledger.peek().durable?.inbound[id];
    const receipt = { ...r?.receipt, epoch: ready.epoch };
    const committed = await client.request(receipt, 'receipt_committed');
    current();
    const result = committed.results?.find(
      (/** @type {any} */ v) => v.recipient_id === id,
    );
    if (
      committed.operation_id !== receipt.operation_id ||
      committed.delivery_id !== item.delivery_id ||
      committed.mail_seq !== item.mail_seq ||
      result?.transport_status !== 'host_accepted' ||
      !isUint64String(committed.committed_cursor) ||
      !isUint64String(result.state_version) ||
      BigInt(result.state_version) <= BigInt(r?.stateVersion ?? '0')
    )
      throw new Error('invalid receipt commit');
    await ledger.update((draft) => {
      if (!draft.durable) throw new Error('durable unavailable');
      draft.durable.inbound[id].stage = 'committed';
      const prefix = cursorEvidencePrefix(draft, committed.committed_cursor);
      if (prefix !== null) draft.durable.resumeCursor = prefix;
    }, current);
  }
  /** Call only after verifying the complete FETCH header, manifest and item.
   * Repeated signatures/epochs may differ, but the terminal fact cannot change.
   * @param {any} item */
  async function retainTerminal(item) {
    if (!isTerminalEvidence(ledger.peek(), item))
      throw new Error('invalid terminal evidence');
    const fact = (/** @type {any} */ value) =>
      canonicalize({
        delivery: value.delivery_id,
        seq: value.mail_seq,
        target: value.target_node,
        recipients: value.recipients,
      });
    await ledger.update((draft) => {
      if (!draft.durable) throw new Error('durable unavailable');
      const entries = (draft.durable.terminal ??= {});
      const old = entries[item.mail_seq];
      if (old && fact(old) !== fact(item)) throw new Error('terminal conflict');
      if (!old) entries[item.mail_seq] = structuredClone(item);
    }, current);
  }
  /** Only a server-confirmed prefix with complete retained evidence can move.
   * @param {string} cursor @param {boolean} [complete] */
  async function advanceCursor(cursor, complete = true) {
    if (!isUint64String(cursor)) throw new Error('invalid committed cursor');
    if (BigInt(cursor) <= BigInt(ledger.peek().durable?.resumeCursor ?? '0'))
      return;
    const prefix = cursorEvidencePrefix(ledger.peek(), cursor);
    if (prefix !== null && prefix !== ledger.peek().durable?.resumeCursor)
      await ledger.update((draft) => {
        if (!draft.durable || cursorEvidencePrefix(draft, cursor) !== prefix)
          throw new Error('mailbox cursor evidence changed');
        draft.durable.resumeCursor = prefix;
      }, current);
    if (complete && prefix !== cursor)
      throw new Error('mailbox cursor evidence missing');
  }
  async function pull() {
    return locks.run('fetch', async () => {
      current();
      const resume = ledger.peek().durable?.resumeCursor ?? '0';
      let committedCursor = resume;
      let continuation = null;
      const seen = new Set();
      for (let pageNumber = 0; pageNumber < 128; pageNumber++) {
        const frame = client.frame('fetch', {
          resume_cursor: resume,
          continuation,
          max_items: 32,
          max_bytes: 65536,
          held_ids: [],
          paused: [],
          state_barrier: [],
        });
        const response = await client.request(frame, 'fetch_result');
        current();
        const page = await verifyFetchPage({
          tenant: ledger.identity.tenant,
          nodeIncarnation: ledger.identity.nodeIncarnation,
          epoch: ready.epoch,
          bootId: ready.bootId,
          items: response.items,
          header: response.page,
          trust,
          context: {
            ...ledger.identity,
            epoch: ready.epoch,
            channelNonce: ready.channelNonce,
            requestId: frame.request_id,
          },
          now,
        });
        current();
        if (page.rejections.length)
          throw new Error(
            'network.signature_invalid: FETCH contains rejected items',
          );
        for (const item of page.items) {
          if (item.type === 'terminal') await retainTerminal(item);
          else
            await isolate(
              'recipient',
              item.recipients?.[0]?.recipient_id ?? item.delivery_id,
              () => deliver(item),
            );
        }
        if (!isUint64String(response.page.committed_cursor))
          throw new Error('invalid committed cursor');
        if (BigInt(response.page.committed_cursor) > BigInt(committedCursor))
          committedCursor = response.page.committed_cursor;
        const next = response.page.next_continuation;
        if (
          next !== null &&
          (typeof next !== 'string' || !next || seen.has(next))
        )
          throw new Error('invalid FETCH continuation');
        // Retain progress even when this bounded pull cannot reach the last
        // page. A later pull starts from the durable prefix with no token;
        // this pull keeps its original resume for every continuation request.
        await advanceCursor(committedCursor, next === null);
        if (next === null) return;
        seen.add(next);
        continuation = next;
      }
      throw new Error('network.overloaded: FETCH page bound reached');
    });
  }
  return {
    setReceive,
    pull,
    deliver,
    failures: () => [...failures.values()],
    snapshot() {
      current();
      return Object.values(ledger.peek().bindings).flatMap((b) => {
        try {
          native(b.local);
        } catch {
          return [];
        }
        const receive = ledger.peek().durable?.receive[b.local];
        return [
          {
            local: b.local,
            sessionId: b.hostSessionId,
            enabled:
              receive?.enabled === true && b.receiveMode !== 'receive_none',
            pending: !!receive?.pending,
            pendingEnabled: receive?.pending?.enabled ?? null,
          },
        ];
      });
    },
    async recover() {
      for (const [local, value] of Object.entries(
        ledger.peek().durable?.receive ?? {},
      ))
        if (value.pending)
          await isolate('receive', local, () =>
            setReceive(local, value.pending.enabled),
          );
      for (const r of Object.values(ledger.peek().durable?.inbound ?? {}))
        if (r.stage === 'accepted')
          await isolate('recipient', r.recipientId, () => deliver(r.delivery));
    },
  };
}
