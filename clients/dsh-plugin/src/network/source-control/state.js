import { randomUUID } from 'node:crypto';
import { canonicalize } from '../trust/jcs.js';
import {
  assertRegistration,
  sameRegistration,
  assertFact,
  id,
  u64,
  invalid,
} from './frames.js';

/** @returns {import('./types.js').SourceState} */
export function emptySources() {
  return {
    version: 1,
    entries: {},
    operations: {},
    revocations: {},
    witnesses: {},
  };
}
/** @param {import('../host/ledger.js').LedgerState} draft @param {string} local @param {string} reason */
export function stopSource(draft, local, reason) {
  const entry = draft.sourceControl?.entries[local];
  if (!entry) return;
  if (!entry.stopped) {
    if (BigInt(entry.bindingRevision) === 18446744073709551615n) invalid();
    entry.bindingRevision = (BigInt(entry.bindingRevision) + 1n).toString();
    entry.stopped = true;
    entry.stopReason = reason;
    entry.observation = null;
  }
  if (entry.head) enqueueRevoke(draft, local, entry.head);
}
/** @param {import('../host/ledger.js').LedgerState} draft @param {string} local @param {string} binding */
export function enqueueRevoke(draft, local, binding) {
  const state = draft.sourceControl;
  if (!state || Object.values(state.revocations).some((r) => r.local === local))
    return;
  const op = randomUUID();
  state.revocations[op] = {
    id: op,
    local,
    sourceBinding: binding,
    receipt: null,
  };
}
/** Source mapping mutations share the actual binding's durable transaction.
 * @param {import('../host/ledger.js').LedgerState} before @param {import('../host/ledger.js').LedgerState} draft */
export function fenceSourceChanges(before, draft) {
  if (before.version === 3 && draft.version !== 3) invalid();
  for (const [local, original] of Object.entries(
    before.sourceControl?.entries ?? {},
  )) {
    const next = draft.sourceControl?.entries[local];
    if (
      !next ||
      canonicalize(next.mapping) !== canonicalize(original.mapping) ||
      !sameRegistrationRecord(next, original) ||
      (original.stopped && !next.stopped) ||
      BigInt(next.bindingRevision) < BigInt(original.bindingRevision)
    )
      invalid();
    const b = draft.bindings[local];
    if (
      !b ||
      b.state !== 'bound' ||
      b.lifecycle !== 'open' ||
      ['bindingId', 'hostSessionId', 'cwd', 'kind'].some(
        (k) =>
          /** @type {any} */ (b)[k] !==
          /** @type {any} */ (original.mapping)[k],
      )
    )
      stopSource(draft, local, 'mapping_changed');
  }
  for (const [key, original] of Object.entries(
    before.sourceControl?.operations ?? {},
  )) {
    const next = draft.sourceControl?.operations[key];
    if (
      !next ||
      canonicalize({ ...next, receipt: null }) !==
        canonicalize({ ...original, receipt: null }) ||
      (original.receipt &&
        canonicalize(next.receipt) !== canonicalize(original.receipt))
    )
      invalid();
  }
  for (const [key, original] of Object.entries(
    before.sourceControl?.revocations ?? {},
  )) {
    const next = draft.sourceControl?.revocations[key];
    if (
      !next ||
      next.id !== original.id ||
      next.local !== original.local ||
      next.sourceBinding !== original.sourceBinding ||
      (original.receipt &&
        canonicalize(next.receipt) !== canonicalize(original.receipt))
    )
      invalid();
  }
  for (const [key, original] of Object.entries(
    before.sourceControl?.witnesses ?? {},
  ))
    if (
      !draft.sourceControl?.witnesses[key] ||
      canonicalize(draft.sourceControl.witnesses[key]) !==
        canonicalize(original)
    )
      invalid();
}
/** @param {import('./types.js').SourceEntry} a @param {import('./types.js').SourceEntry} b */
const sameRegistrationRecord = (a, b) =>
  canonicalize(a.registration) === canonicalize(b.registration);
/** Validate recovery evidence instead of silently recreating missing source maps.
 * @param {import('../host/ledger.js').LedgerState} ledger */
export function checkSources(ledger) {
  const s = ledger.sourceControl;
  if (ledger.version === 3 && !s) invalid();
  if (s === undefined) return;
  if (
    s?.version !== 1 ||
    !['entries', 'operations', 'revocations', 'witnesses'].every((k) => {
      const map = /** @type {any} */ (s)[k];
      return map && typeof map === 'object' && !Array.isArray(map);
    })
  )
    invalid();
  for (const [local, e] of Object.entries(s.entries)) {
    assertRegistration(e?.registration);
    const r = ledger.locals[local];
    if (
      e.local !== local ||
      e.registration.local !== local ||
      !id(e.mapping?.bindingId) ||
      !u64(e.bindingRevision) ||
      typeof e.stopped !== 'boolean' ||
      (e.policyRevision !== undefined && !u64(e.policyRevision, false)) ||
      !e.facts ||
      !r ||
      r.state !== 'registered' ||
      !sameRegistration(e.registration, {
        registration_id: r.registrationId,
        allocation_op_id: r.opId,
        allocation_fingerprint: 'sha256:' + r.fingerprint,
        registration_revision: r.registrationRevision,
        local,
        address_type: r.addressType,
      }) ||
      (e.operationId && s.operations[e.operationId]?.local !== local) ||
      (e.head && !e.facts[e.head])
    )
      invalid();
    for (const [binding, fact] of Object.entries(e.facts)) {
      const op = Object.values(s.operations).find(
        (o) =>
          o.local === local && o.receipt?.source?.source_binding === binding,
      );
      if (!op || binding !== fact.source_binding) invalid();
      assertFact(fact, op.body, op);
    }
  }
  for (const [key, op] of Object.entries(s.operations)) {
    assertRegistration(op?.body?.registration);
    if (
      key !== op.id ||
      !id(key) ||
      !s.entries[op.local] ||
      !id(op.bindingId) ||
      !u64(op.bindingRevision) ||
      op.body.scope !== 'durable_unicast_send' ||
      !u64(op.body.expected_revision, !!op.body.predecessor_binding) ||
      (!op.body.predecessor_binding && op.body.expected_revision !== '0')
    )
      invalid();
    if (
      !sameRegistration(op.body.registration, s.entries[op.local].registration)
    )
      invalid();
    if (op.receipt?.source) {
      assertFact(op.receipt.source, op.body, op);
      if (
        op.receipt.operation_id !== key ||
        op.receipt.result !== 'registered' ||
        op.receipt.source.state !== 'active' ||
        op.receipt.source.revision !== '1' ||
        !s.entries[op.local].facts[op.receipt.source.source_binding]
      )
        invalid();
    } else if (
      op.receipt &&
      (!['not_accepted', 'not_committed'].includes(op.receipt.result) ||
        op.receipt.operation_id !== key ||
        op.receipt.source !== null)
    )
      invalid();
  }
  for (const [key, r] of Object.entries(s.revocations))
    if (
      key !== r.id ||
      !id(key) ||
      !id(r.sourceBinding) ||
      !s.entries[r.local]?.stopped ||
      !s.entries[r.local].facts[r.sourceBinding]
    )
      invalid();
}
