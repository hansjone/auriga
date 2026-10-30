/* global structuredClone */
import { readFile } from 'node:fs/promises';
import { isAbsolute, join } from 'node:path';
import { atomicWriteJson } from './utils/atomic-write.js';
import {
  loadOrCreateSecretKey,
  openSecret,
  sealSecret,
} from './utils/secret-box.js';

/** Atomic copy-on-write: failed storage never publishes credentials/bindings/acceptance.
 * @param {string} directory */
export async function createVault(directory) {
  if (!isAbsolute(directory))
    throw new Error('absolute private state directory required');
  const path = join(directory, 'connect.json');
  let raw;
  try {
    raw = await readFile(path, 'utf8');
  } catch (e) {
    if (/** @type {NodeJS.ErrnoException} */ (e).code !== 'ENOENT')
      throw new Error('connect state unavailable');
  }
  const key = await loadOrCreateSecretKey(directory, {
    hasEncryptedData: raw !== undefined,
  });
  let state =
    raw === undefined
      ? {
          version: 1,
          credential: null,
          revocations: [],
          endpoint: null,
          threads: {},
          deliveries: {},
        }
      : JSON.parse(openSecret(key, JSON.parse(raw)) ?? 'null');
  if (!state || state.version !== 1 || !state.threads || !state.deliveries)
    throw new Error('connect state unavailable');
  let queue = Promise.resolve(),
    closed = false;
  return {
    snapshot() {
      if (closed) throw new Error('connect state closed');
      return structuredClone(state);
    },
    /** @param {(state:any)=>void|Promise<void>} change */
    update(change) {
      const work = queue.then(async () => {
        if (closed) throw new Error('connect state closed');
        const next = structuredClone(state);
        await change(next);
        await atomicWriteJson(path, sealSecret(key, JSON.stringify(next)));
        if (closed) throw new Error('connect state closed');
        state = next;
      });
      queue = work.catch(() => {});
      return work;
    },
    close() {
      closed = true;
      state = null;
      key.fill(0);
    },
  };
}
