/* global structuredClone */
import { canonicalize, parseStrictJson } from '../trust/jcs.js';
import { isUint64String } from '../host/address.js';
import { createKeyedMutex } from '../host/keyed-mutex.js';

/** @param {import('../host/ledger.js').Ledger} ledger @param {string} epoch */
export async function saveProducerEpoch(ledger, epoch) {
  if (!isUint64String(epoch) || epoch === '0')
    throw new Error('invalid producer epoch');
  if (ledger.peek().durable) {
    if (ledger.peek().durable?.producerEpoch !== epoch)
      throw new Error('producer epoch changed');
    return;
  }
  await ledger.update((draft) => {
    draft.durable = {
      producerEpoch: epoch,
      submissions: {},
      receive: {},
      inbound: {},
      resumeCursor: '0',
    };
  });
}

/** Same immutable request is the recovery query. No automatic replacement key.
 * @param {{ledger:import('../host/ledger.js').Ledger,submit:(request:import('../../generated/network/SubmitRequest.js').SubmitRequest,signal:AbortSignal)=>Promise<Uint8Array>,assertCurrent:()=>void,signal?:AbortSignal,now?:()=>number}} options */
export function createDurableOutbox({
  ledger,
  submit,
  assertCurrent,
  signal,
  now = Date.now,
}) {
  const locks = createKeyedMutex();
  const current = () => {
    signal?.throwIfAborted();
    assertCurrent();
    ledger.assertAvailable();
  };
  /** @param {import('../../generated/network/SubmitRequest.js').SubmitRequest} request @param {string} bindingId @param {AbortSignal} [execution] @param {()=>void} [beforeSend] */
  async function send(request, bindingId, execution, beforeSend = () => {}) {
    // Snapshot before the first await: a caller cannot mutate a saved request.
    const original = structuredClone(request);
    const key = canonicalize([
      original.source.source_binding,
      original.idempotency.producer_epoch,
      original.idempotency.key,
    ]);
    return locks.run(key, async () => {
      current();
      execution?.throwIfAborted();
      beforeSend();
      const state = ledger.peek().durable;
      if (!state || original.idempotency.producer_epoch !== state.producerEpoch)
        throw new Error('producer epoch unavailable');
      const existing = state.submissions[key];
      if (
        existing &&
        (canonicalize(existing.request) !== canonicalize(original) ||
          existing.bindingId !== bindingId)
      )
        throw new Error('network.conflict');
      if (existing?.result)
        return parseStrictJson(Buffer.from(existing.result));
      const binding = Object.values(ledger.peek().bindings).find(
        (value) => value.bindingId === bindingId,
      );
      const saved = existing ?? {
        request: original,
        bindingId,
        sourceSnapshot: binding
          ? {
              binding: structuredClone(binding),
              registration: structuredClone(
                ledger.peek().locals[binding.local],
              ),
              bindingRevision:
                ledger.peek().sourceControl?.entries[binding.local]
                  ?.bindingRevision ?? null,
            }
          : null,
        expiresAt: now() + (original.ttl ?? 86400) * 1000,
        result: null,
      };
      if (!existing) {
        if (
          Object.values(state.submissions).filter((v) => v.result === null)
            .length >= 128
        )
          throw new Error('network.overloaded');
        await ledger.update((draft) => {
          if (!draft.durable) throw new Error('producer epoch unavailable');
          draft.durable.submissions[key] = saved;
        }, current);
      }
      current();
      execution?.throwIfAborted();
      beforeSend();
      if (!saved || now() >= saved.expiresAt)
        throw new Error('network.outcome_unknown: original submission expired');
      const abort = new AbortController();
      const timer = setTimeout(
        () => abort.abort(),
        Math.min(15000, saved.expiresAt - now()),
      );
      const stop = () => abort.abort();
      signal?.addEventListener('abort', stop, { once: true });
      execution?.addEventListener('abort', stop, { once: true });
      try {
        current();
        execution?.throwIfAborted();
        const bytes = await submit(
          structuredClone(saved.request),
          abort.signal,
        );
        current();
        execution?.throwIfAborted();
        abort.signal.throwIfAborted();
        const result = parseStrictJson(bytes);
        if (
          !['platform_stored', 'request_submitted', 'contact_pending'].includes(
            result?.status,
          )
        )
          throw new Error('invalid SendResult');
        await ledger.update((draft) => {
          if (!draft.durable) throw new Error('producer epoch unavailable');
          draft.durable.submissions[key].result =
            Buffer.from(bytes).toString('utf8');
        }, current);
        return result;
      } finally {
        clearTimeout(timer);
        signal?.removeEventListener('abort', stop);
        execution?.removeEventListener('abort', stop);
      }
    });
  }
  return {
    send,
    async recover() {
      for (const saved of Object.values(
        ledger.peek().durable?.submissions ?? {},
      )) {
        current();
        if (!saved.result && now() < saved.expiresAt)
          await send(saved.request, saved.bindingId).catch(() => current());
      }
    },
  };
}
