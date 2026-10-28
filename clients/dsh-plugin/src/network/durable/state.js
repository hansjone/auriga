import { canonicalize } from '../trust/jcs.js';
import { isUint64String } from '../host/address.js';
import { isTerminalEvidence } from './recovery.js';

/** @returns {never} */
const invalid = () => {
  throw new Error('durable messaging ledger unavailable');
};
/** @param {unknown} value */
const map = (value) =>
  !!value && typeof value === 'object' && !Array.isArray(value);
/** @param {import('../host/ledger.js').LedgerState} state */
export function checkDurable(state) {
  const d = state.durable;
  if (d === undefined) return;
  if (
    !map(d) ||
    !isUint64String(d.producerEpoch) ||
    d.producerEpoch === '0' ||
    !isUint64String(d.resumeCursor) ||
    !map(d.submissions) ||
    !map(d.receive) ||
    !map(d.inbound) ||
    (d.terminal !== undefined && !map(d.terminal))
  )
    invalid();
  for (const [seq, item] of Object.entries(d.terminal ?? {}))
    if (seq !== item?.mail_seq || !isTerminalEvidence(state, item)) invalid();
  for (const [key, saved] of Object.entries(d.submissions)) {
    if (
      !saved?.request?.source ||
      !saved.request.idempotency ||
      saved.request.idempotency.producer_epoch !== d.producerEpoch ||
      typeof saved.bindingId !== 'string' ||
      !saved.bindingId ||
      !Number.isSafeInteger(saved.expiresAt) ||
      !(saved.result === null || typeof saved.result === 'string') ||
      key !==
        canonicalize([
          saved.request.source.source_binding,
          saved.request.idempotency.producer_epoch,
          saved.request.idempotency.key,
        ])
    )
      invalid();
  }
  for (const [local, receive] of Object.entries(d.receive)) {
    if (
      !state.bindings[local] ||
      typeof receive?.enabled !== 'boolean' ||
      !isUint64String(receive.version) ||
      receive.version === '0' ||
      !(
        receive.pending === null ||
        (map(receive.pending) &&
          typeof receive.pending.enabled === 'boolean' &&
          receive.pending.registration?.local === local &&
          receive.pending.expected_version === receive.version)
      )
    )
      invalid();
  }
  for (const [id, r] of Object.entries(d.inbound)) {
    if (
      !r ||
      r.recipientId !== id ||
      !state.bindings[r.local] ||
      state.bindings[r.local].bindingId !== r.bindingId ||
      !isUint64String(r.stateVersion) ||
      r.stateVersion === '0' ||
      ![
        'received',
        'admit_requested',
        'admitted',
        'invoking',
        'accepted',
        'committed',
      ].includes(r.stage) ||
      r.delivery?.type !== 'delivery' ||
      !r.attemptId ||
      !r.hostSessionId
    )
      invalid();
  }
}
/** Original request and result retention is independent of connection lifetime.
 * @param {import('../host/ledger.js').LedgerState} before @param {import('../host/ledger.js').LedgerState} draft */
export function fenceDurable(before, draft) {
  if (before.durable) {
    const a = before.durable,
      b = draft.durable;
    if (
      !b ||
      a.producerEpoch !== b.producerEpoch ||
      BigInt(b.resumeCursor) < BigInt(a.resumeCursor)
    )
      invalid();
    for (const [seq, item] of Object.entries(a.terminal ?? {}))
      if (
        !b.terminal?.[seq] ||
        canonicalize(item) !== canonicalize(b.terminal[seq])
      )
        invalid();
    for (const [key, value] of Object.entries(a.submissions)) {
      const next = b.submissions[key];
      if (
        !next ||
        canonicalize(value.request) !== canonicalize(next.request) ||
        canonicalize(value.sourceSnapshot) !==
          canonicalize(next.sourceSnapshot) ||
        value.bindingId !== next.bindingId ||
        value.expiresAt !== next.expiresAt ||
        (value.result !== null && value.result !== next.result)
      )
        invalid();
    }
    for (const [id, value] of Object.entries(a.inbound)) {
      const next = b.inbound[id];
      if (
        !next ||
        value.bindingId !== next.bindingId ||
        value.hostSessionId !== next.hostSessionId ||
        canonicalize(value.delivery) !== canonicalize(next.delivery) ||
        (value.receipt &&
          canonicalize(value.receipt) !== canonicalize(next.receipt)) ||
        (value.stage === 'committed' && next.stage !== 'committed')
      )
        invalid();
    }
  }
  checkDurable(draft);
}
