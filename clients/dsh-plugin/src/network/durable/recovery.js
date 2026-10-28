import { isUint64String } from '../host/address.js';

const terminalStatuses = new Set([
  'host_accepted',
  'expired',
  'canceled',
  'revoked',
  'skipped',
  'unresolved',
]);

/** Structural/identity check for a retained terminal witness. Ingestion must
 * first verify its complete original envelope and FETCH page manifest. Stored
 * evidence remains useful after its original signature validity window ends.
 * @param {import('../host/ledger.js').LedgerState} state @param {any} item */
export function isTerminalEvidence(state, item) {
  const a = item?.server_auth;
  return !!(
    item?.type === 'terminal' &&
    isUint64String(item.mail_seq) &&
    item.mail_seq !== '0' &&
    isUint64String(item.epoch) &&
    item.epoch !== '0' &&
    typeof item.delivery_id === 'string' &&
    /^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$/.test(item.delivery_id) &&
    item.target_node?.realm === state.identity.realm &&
    item.target_node?.node === state.identity.node &&
    a?.purpose === 'protocol_response' &&
    a.audience?.realm === state.identity.realm &&
    a.audience?.node === state.identity.node &&
    a.audience?.incarnation === state.identity.nodeIncarnation &&
    a.audience?.epoch === item.epoch &&
    typeof a.signature === 'string' &&
    Array.isArray(item.recipients) &&
    item.recipients.length > 0 &&
    item.recipients.length <= 256 &&
    new Set(item.recipients.map((/** @type {any} */ r) => r?.recipient_id))
      .size === item.recipients.length &&
    item.recipients.every(
      (/** @type {any} */ r) =>
        typeof r?.recipient_id === 'string' &&
        /^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$/.test(r.recipient_id) &&
        terminalStatuses.has(r.transport_status) &&
        isUint64String(r.state_version) &&
        r.state_version !== '0',
    )
  );
}

/** Correlate a verified server cursor with a continuous prefix of retained
 * native acceptances or fully verified server terminal witnesses. A terminal
 * is not a local HostPort acceptance and never authorizes a host invocation.
 * @param {import('../host/ledger.js').LedgerState} state @param {string} cursor */
export function cursorEvidencePrefix(state, cursor) {
  const durable = state.durable;
  if (!durable || !isUint64String(cursor)) return null;
  const from = BigInt(durable.resumeCursor),
    to = BigInt(cursor);
  if (to <= from) return durable.resumeCursor;
  // Index retained entries once. Work stops at the first hole and is bounded
  // by local evidence, even if the server cursor is near uint64's maximum.
  const inbound = new Map();
  for (const r of Object.values(durable.inbound)) {
    const seq = r.delivery.mail_seq;
    if (inbound.has(seq)) inbound.set(seq, null);
    else inbound.set(seq, r);
  }
  let verified = from;
  while (verified < to) {
    const end = verified + 128n < to ? verified + 128n : to;
    for (let seq = verified + 1n; seq <= end; seq++) {
      const terminal = durable.terminal?.[String(seq)];
      if (terminal) {
        if (
          terminal.mail_seq !== String(seq) ||
          !isTerminalEvidence(state, terminal)
        )
          return String(seq - 1n);
        continue;
      }
      const r = inbound.get(String(seq));
      if (!r) return String(seq - 1n);
      const proof = r.receipt?.results?.[0];
      if (
        !['accepted', 'committed'].includes(r.stage) ||
        !r.receipt ||
        r.receipt.results.length !== 1 ||
        r.receipt.delivery_id !== r.delivery.delivery_id ||
        r.receipt.mail_seq !== String(seq) ||
        proof.recipient_id !== r.recipientId ||
        proof.host_request_id !== r.recipientId ||
        proof.message_digest !== r.delivery.source_context.message_digest ||
        proof.status !== 'host_accepted'
      )
        return String(seq - 1n);
    }
    verified = end;
  }
  return String(verified);
}

/** @param {import('../host/ledger.js').LedgerState} state @param {string} cursor */
export function hasCursorEvidence(state, cursor) {
  return (
    !!state.durable &&
    isUint64String(cursor) &&
    BigInt(cursor) > BigInt(state.durable.resumeCursor) &&
    cursorEvidencePrefix(state, cursor) === cursor
  );
}
