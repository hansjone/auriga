// @ts-nocheck
import { createHash } from 'node:crypto';
import { lstat } from 'node:fs/promises';
import { join } from 'node:path';
import { createFileLedgerStore } from '../network/host/store.js';
import { canonical, fail } from './protocol.js';

const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
const copy = (value) =>
  value === undefined ? undefined : JSON.parse(JSON.stringify(value));
const object = (value) =>
  !!value && typeof value === 'object' && !Array.isArray(value);
const digest = (value) =>
  'sha256:' + createHash('sha256').update(canonical(value)).digest('hex');

export function sharedMessagePaths(poolId, kind, contextId) {
  if (
    !uuid.test(poolId) ||
    !['preferences', 'inboxes'].includes(kind) ||
    (kind === 'inboxes' ? !uuid.test(contextId) : contextId !== undefined)
  )
    fail('common.validation_failed');
  const suffix = `message-${kind}` + (contextId ? '/' + contextId : '');
  const root = `/var/lib/auriga/pools/${poolId}/host`;
  // /host maps members. Node's pre-existing hard quota is members/.host;
  // a sibling of .host does not inherit that project quota.
  return { dir: root + '/.host/' + suffix, legacyDir: root + '/' + suffix };
}

export function createSharedMessageStore(poolId, kind, contextId) {
  return createMigratingMessageStore(
    sharedMessagePaths(poolId, kind, contextId),
  );
}

/** Import by an atomic NEW write inside the quota root, never rename legacy
 * inodes (which may retain an unbounded project-id). Preserve the old ledger
 * and pin its exact digest. A later divergent old writer fails closed instead
 * of rolling the new receipt/cursor state back. Pool upgrades are serialized. */
export function createMigratingMessageStore({
  dir,
  legacyDir,
  store,
  legacyStore,
}) {
  const target = store ?? createFileLedgerStore({ dir });
  const legacy = legacyStore ?? createFileLedgerStore({ dir: legacyDir });
  return {
    async open() {
      let old,
        next,
        loaded,
        source = null,
        closed = false,
        failed = false;
      let tail = Promise.resolve();
      try {
        let present = false;
        try {
          const stat = await lstat(join(legacyDir, 'ledger.json'));
          if (!stat.isFile() || stat.isSymbolicLink())
            fail('common.unavailable');
          present = true;
        } catch (error) {
          if (error.code !== 'ENOENT') throw error;
        }
        // Holding the original lock prevents a running legacy process from
        // writing while this process consumes the migrated quota ledger.
        if (present) old = await legacy.open();
        const previous = await old?.load();
        if (present && !object(previous)) fail('common.unavailable');
        next = await target.open();
        const saved = await next.load();
        if (saved !== undefined) {
          if (
            !saved ||
            Object.keys(saved).sort().join(',') !==
              'ledger,legacyDigest,version' ||
            saved.version !== 1 ||
            !object(saved.ledger) ||
            !(
              saved.legacyDigest === null ||
              /^sha256:[a-f0-9]{64}$/.test(saved.legacyDigest)
            )
          )
            fail('common.unavailable');
          source = saved.legacyDigest;
          if (present && source !== digest(previous))
            fail('common.unavailable');
          loaded = saved.ledger;
        } else if (present) {
          source = digest(previous);
          await next.commit({
            version: 1,
            legacyDigest: source,
            ledger: previous,
          });
          loaded = previous;
        }
      } catch (error) {
        await Promise.allSettled([old?.close(), next?.close()]);
        throw error;
      }
      return {
        async load() {
          if (closed || failed) fail('common.unavailable');
          return copy(loaded);
        },
        commit(value) {
          const work = tail.then(async () => {
            if (closed || failed || !object(value)) fail('common.unavailable');
            const snapshot = copy(value);
            try {
              await next.commit({
                version: 1,
                legacyDigest: source,
                ledger: snapshot,
              });
            } catch (error) {
              failed = true;
              throw error;
            }
            loaded = snapshot;
          });
          tail = work.catch(() => {});
          return work;
        },
        async close() {
          if (closed) return;
          closed = true;
          await tail;
          const result = await Promise.allSettled([old?.close(), next.close()]);
          const rejected = result.find((entry) => entry.status === 'rejected');
          if (rejected) throw rejected.reason;
        },
      };
    },
  };
}
