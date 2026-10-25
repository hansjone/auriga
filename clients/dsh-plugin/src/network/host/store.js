import * as nodeFs from 'node:fs/promises';
import { randomBytes } from 'node:crypto';
import { join } from 'node:path';
import { CODES, HostSeamError } from './errors.js';

const MAIN = 'ledger.json';
const TMP = 'ledger.json.tmp';
const LOCK = 'ledger.lock';
/** Paths locked by this process: same-process double open is a conflict too. */
const heldInProcess = new Set();

/**
 * @typedef {{
 *   load(): Promise<unknown>,
 *   commit(state: unknown): Promise<void>,
 *   close(): Promise<void>,
 *   abandon?(): void,
 * }} LedgerStoreHandle
 *
 * @typedef {'write_tmp'|'sync_tmp'|'rename'|'sync_dir'} CommitStep
 */

/** @param {unknown} error */
function errno(error) {
  return /** @type {{ code?: unknown }} */ (error)?.code;
}

/** @param {number} pid */
function defaultIsAlive(pid) {
  try {
    process.kill(pid, 0);
    return true;
  } catch (error) {
    return errno(error) === 'EPERM';
  }
}

/**
 * File-backed ledger: single-writer lock file, whole-snapshot atomic replace
 * (write tmp → fsync → rename → fsync dir). A crash leaves either the previous
 * or the new snapshot, never a mix. A stale lock is reclaimed only when its
 * recorded pid is not alive on this machine (PID reuse would fail closed).
 *
 * @param {{
 *   dir: string,
 *   fs?: typeof nodeFs,
 *   isAlive?: (pid: number) => boolean,
 *   beforeStep?: (step: CommitStep) => Promise<void>|void,
 *   platform?: string,
 * }} options
 */
export function createFileLedgerStore({
  dir,
  fs = nodeFs,
  isAlive = defaultIsAlive,
  beforeStep = () => {},
  platform = process.platform,
}) {
  const mainPath = join(dir, MAIN);
  const tmpPath = join(dir, TMP);
  const lockPath = join(dir, LOCK);

  async function acquire(/** @type {string} */ token) {
    for (let round = 0; round < 2; round++) {
      try {
        const handle = await fs.open(lockPath, 'wx');
        try {
          await handle.writeFile(
            JSON.stringify({ pid: process.pid, token }),
            'utf8',
          );
          await handle.sync();
        } finally {
          await handle.close();
        }
        return;
      } catch (error) {
        if (errno(error) !== 'EEXIST') throw error;
      }
      /** @type {{ pid?: unknown }} */
      let holder = {};
      try {
        holder = JSON.parse(await fs.readFile(lockPath, 'utf8'));
      } catch {
        // Unreadable lock: a crash during lock creation. Treat as stale only
        // when nobody in this process holds the path.
      }
      const pid = typeof holder.pid === 'number' ? holder.pid : null;
      // For our own pid, the in-process registry is authoritative (a
      // leftover from an earlier process that had the same pid).
      const stale =
        !heldInProcess.has(lockPath) &&
        (pid === null || pid === process.pid || !isAlive(pid));
      if (!stale || round > 0)
        throw new HostSeamError(CODES.ledgerUnavailable, 'ledger locked');
      await fs.rm(lockPath, { force: true });
    }
  }

  return {
    /** @returns {Promise<LedgerStoreHandle>} */
    async open() {
      await fs.mkdir(dir, { recursive: true });
      if (heldInProcess.has(lockPath))
        throw new HostSeamError(CODES.ledgerUnavailable, 'ledger locked');
      const token = randomBytes(16).toString('hex');
      await acquire(token);
      heldInProcess.add(lockPath);
      let closed = false;
      await fs.rm(tmpPath, { force: true });

      async function syncDir() {
        await beforeStep('sync_dir');
        // Windows cannot open a directory for fsync; NTFS rename is
        // metadata-journaled. POSIX needs the directory entry synced.
        if (platform === 'win32') return;
        const handle = await fs.open(dir, 'r');
        try {
          await handle.sync();
        } finally {
          await handle.close();
        }
      }

      return {
        async load() {
          if (closed) throw new HostSeamError(CODES.ledgerUnavailable);
          let raw;
          try {
            raw = await fs.readFile(mainPath, 'utf8');
          } catch (error) {
            if (errno(error) === 'ENOENT') return undefined;
            throw new HostSeamError(
              CODES.ledgerUnavailable,
              'ledger unreadable',
            );
          }
          try {
            return JSON.parse(raw);
          } catch {
            throw new HostSeamError(CODES.ledgerUnavailable, 'ledger corrupt');
          }
        },
        /** @param {unknown} state */
        async commit(state) {
          if (closed) throw new HostSeamError(CODES.ledgerUnavailable);
          const data = JSON.stringify(state);
          await beforeStep('write_tmp');
          const handle = await fs.open(tmpPath, 'w');
          try {
            await handle.writeFile(data, 'utf8');
            await beforeStep('sync_tmp');
            await handle.sync();
          } finally {
            await handle.close();
          }
          await beforeStep('rename');
          await fs.rename(tmpPath, mainPath);
          await syncDir();
        },
        async close() {
          if (closed) return;
          closed = true;
          heldInProcess.delete(lockPath);
          try {
            const holder = JSON.parse(await fs.readFile(lockPath, 'utf8'));
            if (holder?.token === token) await fs.rm(lockPath, { force: true });
          } catch {
            // Lock already gone; nothing to release.
          }
        },
        /** Simulated process death for tests: drop the handle, keep the lock
         * file on disk exactly as a killed process would. */
        abandon() {
          closed = true;
          heldInProcess.delete(lockPath);
        },
      };
    },
  };
}

/**
 * Memory-backed store for fixtures. Only committed snapshots survive
 * `crash()`; the lock models one live writer.
 * @param {{ beforeCommit?: (state: unknown) => Promise<void>|void }} [options]
 */
export function createMemoryLedgerStore({ beforeCommit = () => {} } = {}) {
  /** @type {string|undefined} */
  let persisted;
  let locked = false;
  let generation = 0;
  return {
    async open() {
      if (locked)
        throw new HostSeamError(CODES.ledgerUnavailable, 'ledger locked');
      locked = true;
      const mine = ++generation;
      const live = () => locked && generation === mine;
      return {
        async load() {
          if (!live()) throw new HostSeamError(CODES.ledgerUnavailable);
          return persisted === undefined ? undefined : JSON.parse(persisted);
        },
        /** @param {unknown} state */
        async commit(state) {
          if (!live()) throw new HostSeamError(CODES.ledgerUnavailable);
          await beforeCommit(state);
          if (!live()) throw new HostSeamError(CODES.ledgerUnavailable);
          persisted = JSON.stringify(state);
        },
        async close() {
          if (live()) locked = false;
        },
        abandon() {
          if (live()) locked = false;
        },
      };
    },
    /** Process death: the live handle stops working, disk state stays. */
    crash() {
      locked = false;
      generation++;
    },
    snapshot() {
      return persisted === undefined ? undefined : JSON.parse(persisted);
    },
    /** Replace disk contents (rollback / clone experiments). @param {unknown} state */
    restore(state) {
      persisted = state === undefined ? undefined : JSON.stringify(state);
    },
  };
}
