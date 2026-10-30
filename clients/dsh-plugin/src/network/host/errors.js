/**
 * Error codes for the transport-independent host seam. Codes that cross the
 * network use the proposed `network.*` family from the design; the rest stay
 * local and never leave the plugin.
 */
export const CODES = Object.freeze({
  targetUnavailable: 'network.target_unavailable',
  hostIncompatible: 'network.host_incompatible',
  outcomeUnknown: 'network.outcome_unknown',
  overloaded: 'network.overloaded',
  recoveryConflict: 'network.recovery_conflict',
  localAllocationConflict: 'network.local_allocation_conflict',
  protocolError: 'network.protocol_error',
  invalidAddress: 'network.invalid_address',
  unsupportedAddressVersion: 'network.unsupported_address_version',
  realmNotSupported: 'network.realm_not_supported',
  senderMismatch: 'network.sender_mismatch',
  targetMismatch: 'network.target_mismatch',
  attemptConsumed: 'host.attempt_consumed',
  admissionDenied: 'host.admission_denied',
  admissionUnavailable: 'host.admission_unavailable',
  sessionNotReady: 'host.session_not_ready',
  relationPending: 'host.relation_pending',
  flushPending: 'host.flush_pending',
  ledgerUnavailable: 'host.ledger_unavailable',
  invalidBody: 'host.invalid_body',
  localQueueBusy: 'host.local_queue_busy',
});

/** Error carrying a stable code and no host payloads, paths or secrets. */
export class HostSeamError extends Error {
  /** @param {string} code @param {string} [message] */
  constructor(code, message) {
    super(message ?? code);
    this.name = 'HostSeamError';
    this.code = code;
  }
}

/** @param {unknown} error @param {string} code */
export function hasCode(error, code) {
  return (
    error instanceof Error &&
    /** @type {{ code?: unknown }} */ (error).code === code
  );
}
