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
  /** @type {Set<()=>Promise<void>>} */
  const resourceIdentityListeners = new Set();
  /** @type {Set<(identity:{tenantId:string,ownerId:string,hostId:string}|undefined)=>Promise<void>>} */
  const resourceIdentityGates = new Set();
  /** @param {any} value */
  const resourceIdentity = (value) =>
    ['tid', 'sub', 'did']
      .map((key) => value?.credential?.claims?.[key] ?? '')
      .join('/');
  return {
    /** Fence resource views before publishing a different durable identity. IDs only; no credentials.
     * @param {(identity:{tenantId:string,ownerId:string,hostId:string}|undefined)=>Promise<void>} gate */
    subscribeResourceIdentityGate(gate) {
      resourceIdentityGates.add(gate);
      return () => {
        resourceIdentityGates.delete(gate);
      };
    },
    /** Observe durable ownership changes so private resource code stops before logout completes.
     * @param {()=>Promise<void>} listener */
    subscribeResourceIdentity(listener) {
      resourceIdentityListeners.add(listener);
      return () => {
        resourceIdentityListeners.delete(listener);
      };
    },
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
        const changed = resourceIdentity(state) !== resourceIdentity(next);
        const signingOut = changed && !next.credential;
        const c = next.credential?.claims,
          identity = c
            ? { tenantId: c.tid, ownerId: c.sub, hostId: c.did }
            : undefined;
        if (signingOut && resourceIdentityGates.size)
          next.resourceIsolationPending = {
            previous: resourceIdentity(state),
            recordedAt: new Date().toISOString(),
          };
        if (changed && !signingOut) {
          if (next.resourceIsolationPending && !resourceIdentityGates.size)
            throw new Error('resource isolation pending');
          await Promise.all(
            [...resourceIdentityGates].map((gate) => gate(identity)),
          );
          delete next.resourceIsolationPending;
        }
        if (closed) throw new Error('connect state closed');
        await atomicWriteJson(path, sealSecret(key, JSON.stringify(next)));
        if (closed) throw new Error('connect state closed');
        state = next;
        if (signingOut) {
          // Isolation is best effort after the durable credential tombstone. Do not let
          // a 30-second resource-lock wait turn logout into an HTTP timeout.
          // Keep the marker until the next successful pre-publication identity gate.
          void Promise.allSettled(
            [...resourceIdentityGates].map((gate) =>
              Promise.resolve().then(() => gate(undefined)),
            ),
          );
        }
        if (changed)
          await Promise.allSettled(
            [...resourceIdentityListeners].map((listener) =>
              Promise.resolve().then(listener),
            ),
          );
      });
      queue = work.catch(() => {});
      return work;
    },
    close() {
      closed = true;
      resourceIdentityListeners.clear();
      resourceIdentityGates.clear();
      state = null;
      key.fill(0);
      // Fence immediately, then let callers drain an already-started atomic write.
      return queue;
    },
  };
}
