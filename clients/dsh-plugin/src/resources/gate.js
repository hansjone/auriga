import { verifyResourceArchive } from './verify.js';
/** @typedef {import('./verify.js').HighWater} HighWater */
const uuid = '[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}';
const scope = new RegExp(`^${uuid}/${uuid}$`);
const versionId = new RegExp(`^${uuid}$`);
/** @returns {never} */
function unavailable() {
  throw Object.assign(new Error('resource.storage_unavailable'), {
    code: 'resource.storage_unavailable',
  });
}
/** Serializes admission and durable high-water updates. Use a dedicated protected
 * directory and the existing single-writer file ledger adapter in production.
 * The gate is cryptographic admission, not installation/ACL/revocation approval.
 * @param {import('../network/host/store.js').LedgerStoreHandle} store */
export async function openResourceGate(store) {
  /** @type {{schemaVersion: 1, rows: Record<string, HighWater>}} */
  let state;
  try {
    const loaded = /** @type {any} */ (await store.load());
    if (loaded === undefined) state = { schemaVersion: 1, rows: {} };
    else {
      if (
        !loaded ||
        loaded.schemaVersion !== 1 ||
        Object.keys(loaded).sort().join() !== 'rows,schemaVersion' ||
        !loaded.rows ||
        typeof loaded.rows !== 'object' ||
        Array.isArray(loaded.rows)
      )
        unavailable();
      for (const [key, value] of Object.entries(loaded.rows)) {
        const row = /** @type {any} */ (value);
        if (
          !scope.test(key) ||
          !row ||
          Object.keys(row).sort().join() !==
            'digest,releaseSequence,versionId' ||
          !Number.isSafeInteger(row.releaseSequence) ||
          row.releaseSequence <= 0 ||
          typeof row.versionId !== 'string' ||
          !versionId.test(row.versionId) ||
          typeof row.digest !== 'string' ||
          !/^sha256:[0-9a-f]{64}$/.test(row.digest)
        )
          unavailable();
      }
      state = loaded;
    }
  } catch {
    await store.close();
    return unavailable();
  }
  let poisoned = false,
    closed = false;
  let pending = Promise.resolve();
  return {
    /** Returns only AFTER the high water has been persisted. A failed/uncertain
     * commit poisons this handle; reopening reloads the last durable snapshot.
     * @param {Uint8Array} wire @param {Uint8Array} archive
     * @param {Omit<Parameters<typeof verifyResourceArchive>[2], 'highWater'>} options */
    admit(wire, archive, options) {
      if (
        !(wire instanceof Uint8Array) ||
        wire.byteLength > 65536 ||
        !(archive instanceof Uint8Array) ||
        archive.byteLength > 67108864
      )
        return Promise.reject(
          Object.assign(new Error('resource.signature_invalid'), {
            code: 'resource.signature_invalid',
          }),
        );
      // Snapshot bytes and reference before waiting for another admission.
      const bytes = Uint8Array.from(wire),
        content = Uint8Array.from(archive);
      const locked = {
        ...options,
        expected: {
          ...options.expected,
          statement: { ...options.expected.statement },
        },
        releaseKeys: new Map(options.releaseKeys),
      };
      const result = pending.then(async () => {
        if (closed || poisoned) unavailable();
        const key = locked.expected.tenantId + '/' + locked.expected.packageId;
        if (!scope.test(key)) unavailable();
        const envelope = verifyResourceArchive(bytes, content, {
          ...locked,
          highWater: state.rows[key],
        });
        const next = {
          ...state,
          rows: {
            ...state.rows,
            [key]: {
              releaseSequence: envelope.releaseSequence,
              versionId: envelope.versionId,
              digest: envelope.statement.digest,
            },
          },
        };
        try {
          await store.commit(next);
        } catch {
          poisoned = true;
          unavailable();
        }
        state = next;
        return envelope;
      });
      pending = result.then(
        () => {},
        () => {},
      );
      return result;
    },
    async close() {
      closed = true;
      await pending;
      await store.close();
    },
  };
}
