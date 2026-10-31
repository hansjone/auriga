import { SessionError } from '../session/errors.js';
import { digest } from '../trust/verifier.js';
import { timeValue } from '../trust/store.js';
import { clockToleranceMs, platformStarted } from '../clock.js';

/** @param {unknown} v @returns {v is string} */
export const id = (v) =>
  typeof v === 'string' && /^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$/.test(v);
/** @param {unknown} v @param {boolean} [positive] @returns {v is string} */
export const u64 = (v, positive = true) =>
  typeof v === 'string' &&
  /^(0|[1-9][0-9]{0,19})$/.test(v) &&
  BigInt(v) <= 18446744073709551615n &&
  (!positive || v !== '0');
/** @returns {never} */
export function invalid() {
  throw new SessionError('network.protocol_error', 'source control mismatch');
}
/** @param {any} r */
export function assertRegistration(r) {
  if (
    !r ||
    !id(r.registration_id) ||
    !id(r.allocation_op_id) ||
    !/^sha256:[a-f0-9]{64}$/.test(r.allocation_fingerprint ?? '') ||
    !u64(r.registration_revision) ||
    !u64(r.local) ||
    r.address_type !== 'session'
  )
    invalid();
}
/** Compare the known tuple, retaining unknown signed extensions on disk.
 * @param {any} a @param {any} b */
export function sameRegistration(a, b) {
  return [
    'registration_id',
    'allocation_op_id',
    'allocation_fingerprint',
    'registration_revision',
    'local',
    'address_type',
  ].every((k) => a?.[k] === b?.[k]);
}
/** @param {any} fact @param {import('./types.js').RegisterBody} body @param {{bindingId:string,bindingRevision:string}} mapping */
export function assertFact(fact, body, mapping) {
  assertRegistration(fact?.registration);
  if (
    !id(fact.source_binding) ||
    !sameRegistration(fact.registration, body.registration) ||
    fact.scope !== body.scope ||
    !u64(fact.revision) ||
    !['active', 'expired', 'revoked', 'stopped'].includes(fact.state) ||
    !Number.isFinite(timeValue(fact.not_before)) ||
    !(timeValue(fact.expires_at) > timeValue(fact.not_before)) ||
    fact.predecessor_binding !== body.predecessor_binding ||
    fact.native_binding?.binding_id !== mapping.bindingId ||
    fact.native_binding?.binding_revision !== mapping.bindingRevision
  )
    invalid();
}
/** Call after full signature verification, never treat ERROR as a mutation terminal.
 * @param {any} result @param {import('../../generated/network/SourceControl.js').SourceRegister | import('../../generated/network/SourceControl.js').SourceGet | import('../../generated/network/SourceControl.js').SourceRevoke} request
 * @param {number} now */
export function assertSourceResult(result, request, now) {
  if (
    result?.request_id !== request.request_id ||
    result.type !== request.type + '_result'
  )
    invalid();
  if (request.type === 'source_revoke') {
    if (
      result.operation_id !== request.operation_id ||
      result.source_binding !== request.source_binding ||
      !u64(result.revision) ||
      !['revoked', 'expired'].includes(result.state) ||
      result.lineage_stopped !== true
    )
      invalid();
    return;
  }
  if (request.type === 'source_register') {
    if (result.operation_id !== request.operation_id) invalid();
    if (result.result === 'registered') {
      if (
        result.failure_evidence !== null ||
        result.source?.revision !== '1' ||
        result.source?.state !== 'active'
      )
        invalid();
    } else {
      const evidence = result.failure_evidence;
      if (
        result.source !== null ||
        !/^sha256:[a-f0-9]{64}$/.test(evidence?.request_digest ?? '')
      )
        invalid();
      if (result.result === 'not_accepted') {
        const body = {
          registration: request.registration,
          scope: request.scope,
          expected_revision: request.expected_revision,
          predecessor_binding: request.predecessor_binding,
        };
        if (
          evidence.authority !== 'collab' ||
          evidence.reason !== 'rejected_before_pending' ||
          evidence.request_digest !== digest(body)
        )
          invalid();
      } else if (
        result.result !== 'not_committed' ||
        evidence.authority !== 'iam' ||
        ![
          'original_connection_invalid_uncommitted',
          'revision_mismatch',
          'predecessor_not_head',
          'lineage_stopped',
        ].includes(evidence.reason)
      )
        invalid();
      // IAM digest includes a private original connection, so remains opaque here.
    }
    return;
  }
  if (
    !u64(result.policy_revision) ||
    result.witness_id !== request.native_witness.witness_id ||
    !Number.isFinite(timeValue(result.observed_at)) ||
    timeValue(result.observed_at) > now + clockToleranceMs ||
    now - timeValue(result.observed_at) > clockToleranceMs ||
    !(timeValue(request.native_witness.valid_until) > now) ||
    !(timeValue(request.native_witness.observed_at) <= now)
  )
    invalid();
  if (result.source?.state === 'active') {
    const until = timeValue(result.valid_until);
    if (!(
      sourceObservationDeadline(result, request.native_witness) > now &&
      until <= timeValue(request.native_witness.valid_until) &&
      until <= timeValue(result.source.expires_at) &&
      until > timeValue(result.observed_at) &&
      until <= timeValue(result.observed_at) + 60000 &&
      timeValue(result.source.not_before) <= timeValue(result.observed_at) &&
      platformStarted(timeValue(result.source.not_before), now)
    ))
      invalid();
  } else if ('valid_until' in result) invalid();
}

/** A retained observation is only usable while its original local witness lives.
 * @param {any} result @param {any} witness */
export function sourceObservationDeadline(result, witness) {
  if (
    !witness ||
    witness.witness_id !== result?.witness_id ||
    result?.source?.state !== 'active'
  )
    return NaN;
  // Both durations use platform time. Anchoring at local request observation
  // is conservative: it predates the server observation, including network delay.
  // Never grant longer than the original, strictly local native witness.
  const observed = timeValue(result.observed_at);
  return Math.min(
    timeValue(witness.valid_until),
    timeValue(witness.observed_at) + timeValue(result.valid_until) - observed,
    timeValue(witness.observed_at) +
      timeValue(result.source.expires_at) -
      observed,
  );
}
