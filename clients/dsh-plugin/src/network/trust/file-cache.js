import { createFileLedgerStore } from '../host/store.js';

/** Reuse the existing single-writer atomic/fsync snapshot store for public keys.
 * Supply a dedicated private directory, separate from the delivery ledger.
 * No keyset from the network is trusted by this storage adapter.
 * @param {{dir: string}} options
 * @returns {Promise<import('../host/store.js').LedgerStoreHandle>}
 */
export function openFileTrustCache({ dir }) {
  return createFileLedgerStore({ dir }).open();
}
