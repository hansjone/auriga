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
  /** @param {Promise<unknown>} promise */
  function track(promise) {
    work.add(promise);
    void promise.finally(() => work.delete(promise)).catch(() => {});
    return promise;
  }
  /** @param {string} local */
  function entry(local) {
    const e = state()?.entries[local];
    if (!e) throw new Error('source not registered');
    return e;
  }
  /** @param {Entry} e */
  function native(e) {
    current();
    if (e.stopped || stopping.has(e.local)) throw new Error('source stopped');
    const ctx = getContext(),
      b = ledger.peek().bindings[e.local];
    if (
      !ctx ||
      !b ||
      b.state !== 'bound' ||
      b.lifecycle !== 'open' ||
      b.bindingId === b.hostSessionId ||
      !sameRegistration(e.registration, registration(e.local)) ||
      ['bindingId', 'hostSessionId', 'cwd', 'kind'].some(
        (k) => /** @type {any} */ (b)[k] !== /** @type {any} */ (e.mapping)[k],
      )
    )
      throw new Error('source mapping changed');
    let token = natives.get(e.local);
    if (!token) {
      // Only already-live agents are candidates. Never resume cold history.
      token = registrationSession(ctx, {
        agent: ctx.agents.get(b.hostSessionId),
      });
      natives.set(e.local, token);
    }
    token.check();
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
    try {
      return await resolveWitness(e, signal);
    } catch (error) {
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
    const check = () => {
      current();
      signal.throwIfAborted();
      const clock = now();
      if (
        !Number.isSafeInteger(clock) ||
        clock < 0 ||
        clock < observedAt ||
        clock >= validUntil
      )
        throw new Error('source witness clock or deadline');
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
    check();
    const resolver = await createNativeSourceResolver({
      ctx: n.ctx,
      lookupBinding: async (id) => {
        check();
        const b = ledger.peek().bindings[e.local];
        return b?.hostSessionId === id ? b : null;
      },
    });
    check();
    const resolved = await resolver({ agent: n.agent });
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
    if (w) await saveWitness(frame, w.check);
    current();
    signal.throwIfAborted();
    w?.check();
    if (w && now() >= timeValue(w.value.valid_until))
      throw new Error('source witness expired');
    const result =
      /** @type {import('../../generated/network/SourceControl.js').SourceRegisterResult} */ (
        await request(frame, signal)
      );
    current();
    signal.throwIfAborted();
    if (result.result === 'registered') assertFact(result.source, op.body, op);
    // Even a native-close race must retain the authentic original receipt so
    // the newly learned binding can be revoked. It never enables local use.
    let live = false;
    if (!entry(op.local).stopped) {
      try {
        w?.check();
        native(entry(op.local));
        live = true;
      } catch {
        await stop(op.local, 'native_changed');
        current();
        signal.throwIfAborted();
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
    current();
    signal.throwIfAborted();
    pump();
    if (live && !entry(op.local).stopped && result.source)
      await get(op.local, signal);
  }
  /** @param {import('../../generated/network/SourceControl.js').SourceRegister | import('../../generated/network/SourceControl.js').SourceGet} frame @param {()=>void} check */
  async function saveWitness(frame, check) {
    const witnessId = frame.native_witness?.witness_id;
    if (!witnessId) invalid();
    await update((draft) => {
      const s = draft.sourceControl;
      if (!s || s.witnesses[witnessId]) invalid();
      s.witnesses[witnessId] = { request: structuredClone(frame) };
    }, check);
    check();
  }
  /** @param {string} local @param {AbortSignal} signal */
  async function get(local, signal) {
    const e = entry(local),
      head = e.head;
    if (!head) return;
    const op = Object.values(state()?.operations ?? {}).find(
      (o) => o.receipt?.source?.source_binding === head,
    );
    if (!op) invalid();
    const w = await witness(e, signal);
    /** @type {import('../../generated/network/SourceControl.js').SourceGet} */
    const frame = {
      ...context('source_get'),
      type: 'source_get',
      source_binding: head,
      registration: structuredClone(e.registration),
      native_witness: w.value,
    };
    await saveWitness(frame, w.check);
    w.check();
    if (now() >= timeValue(w.value.valid_until))
      throw new Error('source witness expired');
    const result =
      /** @type {import('../../generated/network/SourceControl.js').SourceGetResult} */ (
        await request(frame, signal)
      );
    current();
    signal.throwIfAborted();
    try {
      w.check();
    } catch (error) {
      await stop(local, 'native_changed');
      throw error;
    }
    assertFact(result.source, op.body, op);
    const previous = entry(local).facts[head];
    if (
      result.source.source_binding !== head ||
      BigInt(result.source.revision) < BigInt(previous.revision) ||
      result.source.not_before !== previous.not_before ||
      result.source.expires_at !== previous.expires_at ||
      (previous.state !== 'active' && result.source.state === 'active') ||
      (entry(local).observation &&
        BigInt(result.policy_revision) <
          BigInt(entry(local).observation?.policy_revision ?? '0'))
    )
      invalid();
    await update((draft) => {
      const latest = draft.sourceControl?.entries[local];
      if (!latest || latest.head !== head) invalid();
      latest.facts[head] = result.source;
      latest.observation = result;
      if (['revoked', 'stopped'].includes(result.source.state))
        stopSource(draft, local, 'remote_stopped');
    }, w.check);
    current();
    signal.throwIfAborted();
    if (result.source.state === 'active') retryAt.set(local, now() + 15000);
    if (result.source.state === 'expired' && !entry(local).stopped)
      queue.add(local);
  }
  /** @param {string} local @param {AbortSignal} signal */
  async function drive(local, signal) {
    current();
    signal.throwIfAborted();
    const e = entry(local),
      old = e.operationId ? state()?.operations[e.operationId] : undefined;
    if (old && !old.receipt) {
      await register(old, signal, false);
      return;
    }
    if (e.stopped || stopping.has(local)) return;
    try {
      native(e);
    } catch {
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
    current();
    signal.throwIfAborted();
    /** @type {import('../../generated/network/SourceControl.js').SourceRevoke} */
    const frame = {
      ...context('source_revoke'),
      type: 'source_revoke',
      operation_id: r.id,
      source_binding: r.sourceBinding,
    };
    const result =
      /** @type {import('../../generated/network/SourceControl.js').SourceRevokeResult} */ (
        await request(frame, signal)
      );
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
    const task = Promise.race([action(abort.signal), timeout])
      .catch(() => {})
      .finally(() => {
        clearTimeout(deadline);
        abort.abort();
        attempts.delete(abort);
        done();
        pump();
      });
    track(task);
  }
  function pump() {
    if (closed) return;
    try {
      current();
    } catch {
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
        } catch {
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
          await establish(b.local).catch(() => {
            current();
          });
      await sweep();
      timer = setInterval(() => {
        void sweep().catch(() => {});
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
      while (work.size) await Promise.allSettled([...work]);
    },
    close() {
      closed = true;
      clearInterval(timer);
      for (const a of attempts) a.abort();
    },
  };
}
