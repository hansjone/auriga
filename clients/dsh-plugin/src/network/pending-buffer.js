/* global structuredClone */
/** Private bounded metadata, never an execution/delivery authority. Expiry and
 * exhausted reconciliation mean outcome unknown, not a successful remote write.
 * @typedef {{value:any,owner:string,expiresAt:number,attempts:number}} Row
 * @param {{handle:import('./host/store.js').LedgerStoreHandle,now:()=>number,limit:number,perOwner:number,ttlMs:number,
 * onCommitFailure:()=>void,onDrop?:(reason:string)=>void,
 * migrate?:(value:any)=>Row|undefined}} options */
export async function openPendingBuffer(options) {
  const { handle, now, limit, perOwner, ttlMs } = options;
  /** @type {Record<string,Row>} */
  let records = {};
  let tail = Promise.resolve();
  let poisoned = false;
  /** @param {string} reason */
  function dropped(reason) {
    options.onDrop?.(reason);
  }
  /** @param {Record<string,Row>} draft */
  function trim(draft) {
    let changed = false;
    const owners = new Map();
    for (const [key, row] of Object.entries(draft)) {
      const count = owners.get(row?.owner) ?? 0;
      if (
        !row ||
        !row.owner ||
        !Number.isFinite(row.expiresAt) ||
        row.expiresAt <= now() ||
        !Number.isSafeInteger(row.attempts) ||
        row.attempts < 0 ||
        count >= perOwner
      ) {
        delete draft[key];
        changed = true;
        dropped('expired_or_owner_limit');
        continue;
      }
      owners.set(row.owner, count + 1);
    }
    for (const key of Object.keys(draft).slice(limit)) {
      delete draft[key];
      changed = true;
      dropped('global_limit');
    }
    return changed;
  }
  /** Only a failed actual commit poisons this instance.
   * @param {Record<string,Row>} draft */
  async function commit(draft) {
    try {
      await handle.commit({ version: 1, records: draft });
    } catch (error) {
      poisoned = true;
      options.onCommitFailure();
      throw error;
    }
    records = draft;
  }
  const saved = await handle.load();
  if (saved !== undefined) {
    const snapshot = /** @type {any} */ (saved);
    if (
      snapshot.version === 1 &&
      snapshot.records &&
      typeof snapshot.records === 'object'
    )
      records = snapshot.records;
    else {
      for (const [key, value] of Object.entries(snapshot)) {
        const row = options.migrate?.(value);
        if (row) records[key] = row;
        else dropped('legacy_unknown');
      }
    }
    trim(records);
    await commit(records);
  }
  /** @template T @param {(draft:Record<string,Row>)=>{changed:boolean,result:T}} change */
  function update(change) {
    const work = tail.then(async () => {
      if (poisoned) throw new Error('pending metadata store unavailable');
      const draft = structuredClone(records);
      const trimmed = trim(draft);
      const { changed, result } = change(draft);
      if (trimmed || changed) await commit(draft);
      return result;
    });
    tail = work.then(
      () => {},
      () => {},
    );
    return work;
  }
  return {
    get size() {
      return Object.keys(records).length;
    },
    get settled() {
      return tail;
    },
    /** @param {string} key */
    get(key) {
      const row = records[key];
      return row && row.expiresAt > now() ? row.value : undefined;
    },
    entries() {
      return Object.entries(records)
        .filter(([, row]) => row.expiresAt > now())
        .map(([key, row]) => /** @type {[string,any]} */ ([key, row.value]));
    },
    /** Capacity/conflict rejection has no commit-failure side effects.
     * @param {string} key @param {any} value @param {string} owner @param {number} [expiresAt] */
    put(key, value, owner, expiresAt = now() + ttlMs) {
      const copy = structuredClone(value);
      return update((draft) => {
        const old = draft[key];
        if (
          old &&
          (old.owner !== owner ||
            (old.value.request?.digest &&
              old.value.request.digest !== copy.request?.digest))
        )
          return { changed: false, result: false };
        if (
          !old &&
          (Object.keys(draft).length >= limit ||
            Object.values(draft).filter((row) => row.owner === owner).length >=
              perOwner)
        )
          return { changed: false, result: false };
        draft[key] = {
          value: copy,
          owner,
          expiresAt: Math.min(
            old?.expiresAt ?? Infinity,
            expiresAt,
            now() + ttlMs,
          ),
          attempts: old?.attempts ?? 0,
        };
        return { changed: true, result: true };
      });
    },
    /** @param {string[]} keys */
    remove(keys) {
      return update((draft) => {
        let changed = false;
        for (const key of keys) {
          if (draft[key]) {
            delete draft[key];
            changed = true;
          }
        }
        return { changed, result: undefined };
      });
    },
    prune() {
      return update(() => ({ changed: false, result: undefined }));
    },
    /** Persist the attempt before I/O, so process restarts cannot mint retries.
     * @param {string} key @param {number} maximum */
    claim(key, maximum) {
      return update((draft) => {
        const row = draft[key];
        if (!row) return { changed: false, result: false };
        if (row.attempts >= maximum) {
          delete draft[key];
          dropped('retry_limit_unknown');
          return { changed: true, result: false };
        }
        row.attempts++;
        return { changed: true, result: true };
      });
    },
  };
}
