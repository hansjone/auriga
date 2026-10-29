/* global structuredClone */
import { randomUUID } from 'node:crypto';
import { createNativeSourceResolver } from '../host/native/source-context.js';
import { registrationSession } from '../host/native/host-port.js';
import {
  assertFact,
  assertRegistration,
  sameRegistration,
  invalid,
} from './frames.js';
import { emptySources, stopSource, enqueueRevoke } from './state.js';
import { timeValue } from '../trust/store.js';
import {
  observeSourceWork,
  sourceEvent,
  sourceReject,
  sourceRpc,
  sourceStage,
  sourceWaiting,
  sourceTaskFailed,
} from '../../observability/source.js';

// Local pending work is capped conservatively; terminal history is never erased.
const MAX_PENDING = 128;
const REQUEST_MS = 30000;
/** @typedef {import('../host/ledger.js').Ledger} Ledger */
/** @typedef {import('./types.js').SourceEntry} Entry */
/** @typedef {import('./types.js').RegisterOperation} Operation */

/** Internal, per-node source lifecycle. No SEND or model-selectable entry point.
 * @param {{ledger:Ledger,getContext:()=>any,ready:{epoch:string,bootId:string},request:ReturnType<typeof import('./connection.js').connectionSourceClient>,assertCurrent:()=>void,now?:()=>number,onStorageFailure?:()=>void}} options */
export function createSourceControl({
  ledger,
  getContext,
  ready,
  request,
  assertCurrent,
  now = Date.now,
  onStorageFailure,
}) {
  let closed = false;
  /** @type {Map<string, ReturnType<typeof registrationSession>>} */ const natives =
    new Map();
  /** @type {Set<string>} */ const stopping = new Set();
  /** @type {Set<string>} */ const queue = new Set();
  /** @type {Set<Promise<unknown>>} */ const work = new Set();
  /** @type {Set<AbortController>} */ const attempts = new Set();
  /** @type {Map<string, number>} */ const retryAt = new Map();
  /** @type {ReturnType<typeof setInterval>|undefined} */ let timer;
  let regular = false,
    revoking = false;
  const state = () => ledger.peek().sourceControl;
  const current = () => {
    if (closed) throw new Error('source generation retired');
    assertCurrent();
    ledger.assertAvailable();
  };
  /** @template T @param {(draft:import('../host/ledger.js').LedgerState)=>T} mutate @param {()=>void} [fence] */
  async function update(mutate, fence = current) {
    const previous = sourceStage('persist');
    sourceEvent('persist started');
    try {
      const result = await ledger.update(mutate, fence);
      sourceEvent('persist completed');
      sourceStage(previous);
      return result;
    } catch (error) {
      sourceEvent('persist failed', {}, error);
      try {
        ledger.assertAvailable();
      } catch {
        onStorageFailure?.();
      }
      throw error;
    }
  }
  /** @param {Promise<unknown>} promise */
  function track(promise) {
    work.add(promise);
    void promise
      .finally(() => work.delete(promise))
      .catch((error) => {
        sourceTaskFailed(promise, error);
      });
    return promise;
  }
  /** @param {string} local */
  function entry(local) {
    const e = state()?.entries[local];
    if (!e) {
      sourceReject('entry_missing');
      throw new Error('source not registered');
    }
    return e;
  }
  /** @param {Entry} e */
  function native(e) {
    const previous = sourceStage('native_guard');
    current();
    if ((e.stopped || stopping.has(e.local)) && sourceReject('native_stopped'))
      throw new Error('source stopped');
    const ctx = getContext(),
      b = ledger.peek().bindings[e.local];
    if (
      (!ctx && sourceReject('native_context_missing')) ||
      (!b && sourceReject('native_binding_missing')) ||
      (b.state !== 'bound' && sourceReject('native_binding_unbound')) ||
      (b.lifecycle !== 'open' && sourceReject('native_session_closed')) ||
      (b.bindingId === b.hostSessionId &&
        sourceReject('native_binding_not_independent')) ||
      (!sameRegistration(e.registration, registration(e.local)) &&
        sourceReject('native_registration_changed')) ||
      (['bindingId', 'hostSessionId', 'cwd', 'kind'].some(
        (k) => /** @type {any} */ (b)[k] !== /** @type {any} */ (e.mapping)[k],
      ) &&
        sourceReject('native_mapping_changed'))
    )
      throw new Error('source mapping changed');
    let token = natives.get(e.local);
    if (!token) {
      sourceStage('native_registration');
      // Only already-live agents are candidates. Never resume cold history.
      token = registrationSession(ctx, {
        agent: ctx.agents.get(b.hostSessionId),
      });
      natives.set(e.local, token);
    }
    sourceStage('native_token_check');
    token.check();
    sourceStage(previous);
    return { ctx, token, agent: ctx.agents.get(b.hostSessionId) };
  }
  /** @param {string} local @returns {import('../../generated/network/SourceControl.js').Registration} */
  function registration(local) {
    const r = ledger.peek().locals[local];
    if (!r || r.state !== 'registered' || r.addressType !== 'session')
      invalid();
    const result = {
      registration_id: r.registrationId ?? '',
      allocation_op_id: r.opId,
      allocation_fingerprint: 'sha256:' + r.fingerprint,
      registration_revision: r.registrationRevision ?? '',
      local,
      address_type: /** @type {const} */ ('session'),
    };
    assertRegistration(result);
    return result;
  }
  /** Stop synchronously fences local use; durable transaction precedes Revoke.
   * @param {string} local @param {string} [reason] */
  async function stop(local, reason = 'local_stop') {
    stopping.add(local);
    if (!state()?.entries[local]) return;
    await update((draft) => stopSource(draft, local, reason));
    pump();
  }
  /** @param {Entry} e @param {AbortSignal} signal */
  async function witness(e, signal) {
    sourceStage('witness');
    sourceEvent('witness started');
    try {
      const result = await resolveWitness(e, signal);
      sourceEvent('witness completed');
      return result;
    } catch (error) {
      sourceEvent('witness failed', {}, error);
      if (!closed && !signal.aborted) await stop(e.local, 'native_unavailable');
      throw error;
    }
  }
  /** @param {Entry} e @param {AbortSignal} signal */
  async function resolveWitness(e, signal) {
    const n = native(e),
      revision = e.bindingRevision;
    let observedAt = -Infinity,
      validUntil = Infinity;
    const checkNative = () => {
      current();
      n.token.check();
      const latest = entry(e.local);
      if (
        latest.bindingRevision !== revision ||
        latest.stopped ||
        stopping.has(e.local) ||
        getContext() !== n.ctx
      )
        throw new Error('source native fence');
      native(latest);
    };
    const checkFresh = () => {
      signal.throwIfAborted();
      const clock = now();
      if (
        !Number.isSafeInteger(clock) ||
        clock < 0 ||
        clock < observedAt ||
        clock >= validUntil
      )
        throw new Error('source witness clock or deadline');
    };
    const check = () => {
      checkNative();
      checkFresh();
    };
    check();
    sourceStage('native_resolver_create');
    const resolver = await createNativeSourceResolver({
      ctx: n.ctx,
      lookupBinding: async (id) => {
        check();
        const b = ledger.peek().bindings[e.local];
        return b?.hostSessionId === id ? b : null;
      },
    });
    check();
    sourceStage('native_source_resolve');
    sourceEvent('native source resolve started');
    const resolved = await resolver({ agent: n.agent });
    sourceStage('native_source_fence');
    check();
    if (
      resolved.local !== e.local ||
      resolved.bindingId !== e.mapping.bindingId
    )
      invalid();
    const clock = now();
    if (!Number.isSafeInteger(clock) || clock < 0)
      throw new Error('source clock unavailable');
    const observed = Math.floor(clock / 1000) * 1000;
    observedAt = observed;
    validUntil = observed + REQUEST_MS;
    return {
      check,
      checkNative,
      checkFresh,
      value: {
        witness_id: randomUUID(),
        binding_id: e.mapping.bindingId,
        binding_revision: revision,
        observed_at: iso(observed),
        valid_until: iso(observed + REQUEST_MS),
      },
    };
  }
  /** @param {number} value */
  function iso(value) {
    return new Date(value).toISOString().replace('.000Z', 'Z');
  }
  /** @param {string} type */
  function context(type) {
    return {
      app_version: '1.0',
      type,
      request_id: randomUUID(),
      epoch: ready.epoch,
      boot_id: ready.bootId,
    };
  }
  /** @param {Operation} op @param {AbortSignal} signal @param {boolean} issue */
  async function register(op, signal, issue) {
    sourceStage('register');
    const e = entry(op.local);
    const w = issue ? await witness(e, signal) : null;
    current();
    signal.throwIfAborted();
    /** @type {import('../../generated/network/SourceControl.js').SourceRegister} */
    const frame = {
      ...context('source_register'),
      type: 'source_register',
      operation_id: op.id,
      ...structuredClone(op.body),
      mode: issue ? 'issue' : 'reconcile',
      native_witness: w?.value ?? null,
    };
    if (w) await saveWitness(frame, w);
    current();
    signal.throwIfAborted();
    w?.check();
    if (w && now() >= timeValue(w.value.valid_until))
      throw new Error('source witness expired');
    sourceStage('register_request');
    sourceRpc(frame, 'started');
    const result =
      /** @type {import('../../generated/network/SourceControl.js').SourceRegisterResult} */ (
        await request(frame, signal)
      );
    sourceRpc(frame, 'response_received');
    sourceStage('register_reply_check');
    current();
    signal.throwIfAborted();
    if (result.result === 'registered') assertFact(result.source, op.body, op);
    // Even a native-close race must retain the authentic original receipt so
    // the newly learned binding can be revoked. It never enables local use.
    let live = false;
    if (!entry(op.local).stopped) {
      try {
        w?.checkNative();
        native(entry(op.local));
        live = true;
      } catch (error) {
        sourceEvent('register native fence failed', {}, error);
        await stop(op.local, 'native_changed');
        current();
        signal.throwIfAborted();
      }
    }
    if (live) {
      try {
        w?.checkFresh();
      } catch (error) {
        sourceEvent('register freshness failed', {}, error);
        // A historic Register receipt remains reconcilable. Freshness expiry
        // only disables immediate use; a later Get needs a new resolver call.
        live = false;
        await discardObservation(op.local);
      }
    }
    await update((draft) => {
      const s = draft.sourceControl;
      if (!s || s.operations[op.id].receipt) return;
      s.operations[op.id].receipt = result;
      const latest = s.entries[op.local];
      if (result.source) {
        latest.facts[result.source.source_binding] = structuredClone(
          result.source,
        );
        latest.head = result.source.source_binding;
        latest.observation = null;
        if (latest.stopped || stopping.has(op.local)) {
          stopSource(draft, op.local, 'local_stop');
          enqueueRevoke(draft, op.local, result.source.source_binding);
        }
      } else if (result.failure_evidence?.authority === 'iam') {
        if (
          ['lineage_stopped', 'predecessor_not_head'].includes(
            result.failure_evidence.reason,
          )
        )
          stopSource(draft, op.local, 'remote_stopped');
        else latest.observation = null;
      }
    });
    sourceEvent('register result retained', {
      result: ['registered', 'not_accepted', 'rejected'].includes(result.result)
        ? result.result
        : 'other',
    });
    current();
    signal.throwIfAborted();
    pump();
    if (live && !entry(op.local).stopped && result.source)
      await get(op.local, signal);
  }
  /** @param {string} local @param {string} [requestId] */
  async function discardObservation(local, requestId) {
    if (!entry(local).observation) return;
    await update((draft) => {
      const e = draft.sourceControl?.entries[local];
      if (
        e?.observation &&
        (!requestId || e.observation.request_id === requestId)
      ) {
        e.policyRevision = e.observation.policy_revision;
        e.observation = null;
      }
    });
  }
  /** @param {import('../../generated/network/SourceControl.js').SourceRegister | import('../../generated/network/SourceControl.js').SourceGet} frame @param {{check:()=>void,checkNative:()=>void,checkFresh:()=>void}} w */
  async function saveWitness(frame, w) {
    const witnessId = frame.native_witness?.witness_id;
    if (!witnessId) invalid();
    await update((draft) => {
      w.checkFresh();
      const s = draft.sourceControl;
      if (!s || s.witnesses[witnessId]) invalid();
      s.witnesses[witnessId] = { request: structuredClone(frame) };
    }, w.checkNative);
    w.check();
  }
  /** @param {string} local @param {AbortSignal} signal */
  async function get(local, signal) {
    sourceStage('get');
    const e = entry(local),
      head = e.head;
    if (!head) return;
    const op = Object.values(state()?.operations ?? {}).find(
      (o) => o.receipt?.source?.source_binding === head,
    );
    if (!op) invalid();
    // An observation is not a reusable allow lease while a new query is pending
    // or times out. Keep its policy revision for this response's monotonic check.
    const policyRevision =
      e.policyRevision ?? e.observation?.policy_revision ?? '0';
    await discardObservation(local);
    current();
    signal.throwIfAborted();
    const w = await witness(e, signal);
    /** @type {import('../../generated/network/SourceControl.js').SourceGet} */
    const frame = {
      ...context('source_get'),
      type: 'source_get',
      source_binding: head,
      registration: structuredClone(e.registration),
      native_witness: w.value,
    };
    try {
      await saveWitness(frame, w);
      w.check();
      if (now() >= timeValue(w.value.valid_until))
        throw new Error('source witness expired');
      sourceStage('get_request');
      sourceRpc(frame, 'started');
      const result =
        /** @type {import('../../generated/network/SourceControl.js').SourceGetResult} */ (
          await request(frame, signal)
        );
      sourceRpc(frame, 'response_received');
      sourceStage('get_reply_check');
      current();
      signal.throwIfAborted();
      w.check();
      assertFact(result.source, op.body, op);
      const previous = entry(local).facts[head];
      if (
        result.source.source_binding !== head ||
        BigInt(result.source.revision) < BigInt(previous.revision) ||
        result.source.not_before !== previous.not_before ||
        result.source.expires_at !== previous.expires_at ||
        (previous.state !== 'active' && result.source.state === 'active') ||
        BigInt(result.policy_revision) < BigInt(policyRevision)
      )
        invalid();
      await update((draft) => {
        w.checkFresh();
        const latest = draft.sourceControl?.entries[local];
        if (!latest || latest.head !== head) invalid();
        latest.facts[head] = result.source;
        latest.policyRevision = result.policy_revision;
        latest.observation = result;
        if (['revoked', 'stopped'].includes(result.source.state))
          stopSource(draft, local, 'remote_stopped');
      }, w.checkNative);
      sourceEvent('get observation retained');
      w.check();
      if (result.source.state === 'active') retryAt.set(local, now() + 15000);
      if (result.source.state === 'expired' && !entry(local).stopped)
        queue.add(local);
    } catch (error) {
      sourceEvent('get failed', {}, error);
      current();
      try {
        w.checkNative();
      } catch {
        await stop(local, 'native_changed');
        throw error;
      }
      // Pure freshness/request expiry (or a failed response) never stops a
      // healthy lineage. Only discard this attempt, not a newer observation.
      await discardObservation(local, frame.request_id);
      throw error;
    }
  }
  /** @param {string} local @param {AbortSignal} signal */
  async function drive(local, signal) {
    sourceStage('drive');
    current();
    signal.throwIfAborted();
    const e = entry(local),
      old = e.operationId ? state()?.operations[e.operationId] : undefined;
    if (old && !old.receipt) {
      await register(old, signal, false);
      return;
    }
    if (e.stopped || stopping.has(local)) {
      sourceReject('entry_stopped');
      sourceEvent('control skipped', { rpc: 'not_started' });
      return;
    }
    sourceStage('native_guard');
    try {
      native(e);
    } catch (error) {
      sourceEvent('native guard failed', { rpc: 'not_started' }, error);
      await stop(local, 'native_changed');
      return;
    }
    const fact = e.head ? e.facts[e.head] : null;
    if (fact && (!e.observation || fact.state === 'active')) {
      await get(local, signal);
      return;
    }
    // Only our current head's verified natural-expiry observation can renew.
    if (
      fact &&
      (fact.state !== 'expired' ||
        e.observation?.source.source_binding !== e.head)
    )
      return;
    const body = {
      registration: structuredClone(e.registration),
      scope: /** @type {const} */ ('durable_unicast_send'),
      expected_revision: fact?.revision ?? '0',
      predecessor_binding: fact?.source_binding ?? '',
    };
    const op = {
      id: randomUUID(),
      local,
      body,
      bindingId: e.mapping.bindingId,
      bindingRevision: e.bindingRevision,
      receipt: null,
    };
    const check = () => {
      current();
      signal.throwIfAborted();
      native(entry(local));
    };
    await update((draft) => {
      const s = draft.sourceControl;
      if (!s) invalid();
      if (
        Object.values(s.operations).filter((o) => !o.receipt).length >=
        MAX_PENDING
      )
        throw new Error('source backpressure');
      s.operations[op.id] = op;
      s.entries[local].operationId = op.id;
    }, check);
    check();
    await register(op, signal, true);
  }
  /** @param {import('./types.js').Revocation} r @param {AbortSignal} signal */
  async function revoke(r, signal) {
    sourceStage('revoke');
    current();
    signal.throwIfAborted();
    /** @type {import('../../generated/network/SourceControl.js').SourceRevoke} */
    const frame = {
      ...context('source_revoke'),
      type: 'source_revoke',
      operation_id: r.id,
      source_binding: r.sourceBinding,
    };
    sourceStage('revoke_request');
    sourceRpc(frame, 'started');
    const result =
      /** @type {import('../../generated/network/SourceControl.js').SourceRevokeResult} */ (
        await request(frame, signal)
      );
    sourceRpc(frame, 'response_received');
    sourceStage('revoke_reply_check');
    current();
    signal.throwIfAborted();
    await update((draft) => {
      const s = draft.sourceControl;
      if (!s) invalid();
      s.revocations[r.id].receipt = result;
      const e = s.entries[r.local],
        fact = e.facts[r.sourceBinding];
      if (BigInt(result.revision) >= BigInt(fact.revision)) {
        fact.revision = result.revision;
        fact.state = result.state;
      }
    });
  }
  /** Acceptance deadline includes native resolution and storage, not just wire time.
   * @param {(signal:AbortSignal)=>Promise<void>} action @param {()=>void} done */
  function launch(action, done) {
    const abort = new AbortController();
    attempts.add(abort);
    /** @type {ReturnType<typeof setTimeout>|undefined} */ let deadline;
    const timeout = new Promise((_, reject) => {
      deadline = setTimeout(() => {
        abort.abort();
        reject(new Error('source request timeout'));
      }, REQUEST_MS);
    });
    const task = observeSourceWork(() =>
      Promise.race([action(abort.signal), timeout])
        .catch((error) => {
          sourceEvent('control failed', {}, error);
        })
        .finally(() => {
          sourceEvent('control task settled');
          clearTimeout(deadline);
          abort.abort();
          attempts.delete(abort);
          done();
          pump();
        }),
    );
    track(task);
  }
  function pump() {
    if (closed) return;
    try {
      current();
    } catch (error) {
      sourceEvent('pump generation rejected', { stage: 'pump_current' }, error);
      return;
    }
    // Revocations have a reserved lane, including while regular RPCs are hung.
    const r = Object.values(state()?.revocations ?? {}).find(
      (r) => !r.receipt && (retryAt.get(r.id) ?? 0) <= now(),
    );
    if (!revoking && r) {
      revoking = true;
      retryAt.set(r.id, now() + 30000);
      launch(
        (signal) => revoke(r, signal),
        () => {
          revoking = false;
        },
      );
    }
    // Dispatch revocation first, while leaving the separate regular lane
    // available to other sessions even when a remote revoke is unresponsive.
    if (regular) return;
    const local = [...queue].find((key) => (retryAt.get(key) ?? 0) <= now());
    if (!local) return;
    queue.delete(local);
    regular = true;
    retryAt.set(local, now() + 1000);
    launch(
      (signal) => drive(local, signal),
      () => {
        regular = false;
      },
    );
  }
  /** Establish is invoked by a successful join, never by a model source choice.
   * @param {string} local */
  async function establish(local) {
    current();
    if (!state()?.entries[local]) {
      const b = ledger.peek().bindings[local];
      if (
        !b ||
        b.state !== 'bound' ||
        b.lifecycle !== 'open' ||
        b.bindingId === b.hostSessionId ||
        b.kind !== 'session'
      )
        invalid();
      const r = registration(local);
      await update((draft) => {
        const s = (draft.sourceControl ??= emptySources());
        draft.version = 3;
        if (s.entries[local]) return;
        if (
          Object.values(s.entries).filter(
            (e) =>
              !e.stopped ||
              (e.operationId && !s.operations[e.operationId]?.receipt) ||
              Object.values(s.revocations).some(
                (r) => r.local === e.local && !r.receipt,
              ),
          ).length >= MAX_PENDING
        )
          throw new Error('source backpressure');
        s.entries[local] = {
          local,
          mapping: {
            bindingId: b.bindingId,
            hostSessionId: b.hostSessionId,
            cwd: b.cwd,
            kind: b.kind,
          },
          bindingRevision: '1',
          registration: r,
          stopped: false,
          stopReason: null,
          operationId: null,
          head: null,
          facts: {},
          observation: null,
        };
      });
      current();
    }
    const e = entry(local),
      op = e.operationId && state()?.operations[e.operationId];
    if ((!e.stopped || (op && !op.receipt)) && queue.size < MAX_PENDING)
      queue.add(local);
    pump();
  }
  /** Native change observation is independent of network requests and lanes. */
  async function sweep() {
    current();
    for (const e of Object.values(state()?.entries ?? {})) {
      if (!e.stopped) {
        try {
          native(e);
        } catch (error) {
          sourceEvent(
            'sweep native guard failed',
            { stage: 'native_guard' },
            error,
          );
          await stop(e.local, 'native_changed');
        }
      }
      const op = e.operationId && state()?.operations[e.operationId];
      if ((!e.stopped || (op && !op.receipt)) && queue.size < MAX_PENDING)
        queue.add(e.local);
    }
    pump();
  }
  return {
    establish,
    stop,
    async stopAll() {
      await Promise.all(
        Object.keys(state()?.entries ?? {}).map((local) =>
          stop(local, 'host_unavailable'),
        ),
      );
    },
    async start() {
      current();
      for (const b of Object.values(ledger.peek().bindings))
        if (
          b.kind === 'session' &&
          b.state === 'bound' &&
          b.lifecycle === 'open' &&
          ledger.peek().locals[b.local]?.state === 'registered'
        )
          await establish(b.local).catch((error) => {
            sourceEvent('establish failed', { stage: 'establish' }, error);
            current();
          });
      await sweep();
      timer = setInterval(() => {
        void sweep().catch((error) => {
          sourceEvent('sweep failed', { stage: 'sweep' }, error);
        });
      }, 1000);
      timer.unref?.();
    },
    sweep,
    /** @param {string} local */
    async refresh(local) {
      queue.add(local);
      retryAt.delete(local);
      pump();
      await this.idle();
    },
    async idle() {
      while (work.size) {
        const pending = [...work];
        sourceWaiting(pending);
        await Promise.allSettled(pending);
      }
    },
    close() {
      closed = true;
      clearInterval(timer);
      for (const a of attempts) a.abort();
    },
  };
}
