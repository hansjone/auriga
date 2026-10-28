/** Correlate a verified READY cursor with every retained local acceptance.
 * A signed counter alone never repairs missing delivery/receipt evidence.
 * @param {import('../host/ledger.js').LedgerState} state @param {string} cursor */
export function acceptedCursor(state, cursor) {
  const durable = state.durable;
  if (!durable || !/^(0|[1-9][0-9]{0,19})$/.test(cursor)) return false;
  const from = BigInt(durable.resumeCursor),
    to = BigInt(cursor);
  if (to <= from || to - from > 128n) return false;
  for (let seq = from + 1n; seq <= to; seq++) {
    const entries = Object.values(durable.inbound).filter(
      (value) => value.delivery.mail_seq === String(seq),
    );
    if (entries.length !== 1) return false;
    const r = entries[0],
      proof = r.receipt?.results?.[0];
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
      return false;
  }
  return true;
}
