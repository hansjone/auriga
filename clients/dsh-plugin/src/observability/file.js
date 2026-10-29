import { mkdir, open, readFile, rename, unlink, lstat } from 'node:fs/promises';
import { join } from 'node:path';

/** Eight fixed slots, two 2 MiB files per slot. A lifetime lock prevents two
 * processes rotating the same slot. No filesystem work runs on the request path.
 * @param {string} directory @param {{maxBytes?:number}} [options] */
export function createFileSink(directory, { maxBytes = 2 * 1024 * 1024 } = {}) {
  let closed = false,
    pending = 0,
    dropped = 0;
  /** @type {import('node:fs/promises').FileHandle | undefined} */ let handle;
  /** @type {import('node:fs/promises').FileHandle | undefined} */ let lock;
  let lockPath = '',
    file = '',
    size = 0;
  const ready = (async () => {
    await mkdir(directory, { recursive: true, mode: 0o700 });
    if (!(await lstat(directory)).isDirectory()) return;
    for (let slot = 0; slot < 8; slot++) {
      const name = join(directory, `auriga-dsh-${slot}`);
      const candidate = `${name}.lock`;
      try {
        lock = await open(candidate, 'wx', 0o600);
      } catch (error) {
        if (/** @type {NodeJS.ErrnoException} */ (error).code !== 'EEXIST')
          throw error;
        // Serialize reclamation, then re-read under that gate. Otherwise two
        // stale readers could unlink the replacement lease of a live writer.
        /** @type {import('node:fs/promises').FileHandle | undefined} */ let reaper;
        try {
          reaper = await open(`${candidate}.reap`, 'wx', 0o600);
          const pid = Number(await readFile(candidate, 'utf8'));
          if (!Number.isSafeInteger(pid) || pid <= 0) continue;
          try {
            process.kill(pid, 0);
            continue;
          } catch (cause) {
            if (/** @type {NodeJS.ErrnoException} */ (cause).code !== 'ESRCH')
              continue;
          }
          await unlink(candidate);
          lock = await open(candidate, 'wx', 0o600);
        } catch {
          continue;
        } finally {
          if (reaper) {
            await reaper.close().catch(() => {});
            await unlink(`${candidate}.reap`).catch(() => {});
          }
        }
      }
      lockPath = candidate;
      await lock.writeFile(String(process.pid));
      file = `${name}.jsonl`;
      try {
        if (!(await lstat(file)).isFile())
          throw new Error('not a regular log file');
      } catch (error) {
        if (/** @type {NodeJS.ErrnoException} */ (error).code !== 'ENOENT')
          throw error;
      }
      handle = await open(file, 'a', 0o600);
      size = (await handle.stat()).size;
      return;
    }
  })();
  let tail = ready.catch(() => {});
  return {
    /** @param {string} line */
    write(line) {
      if (closed || pending >= 64 || Buffer.byteLength(line) > 65536) {
        dropped++;
        return;
      }
      pending++;
      tail = tail
        .then(async () => {
          if (!handle) {
            dropped++;
            return;
          }
          const bytes = Buffer.byteLength(line);
          if (size + bytes > maxBytes) {
            await handle.close();
            handle = undefined;
            await unlink(`${file}.1`).catch((error) => {
              if (error.code !== 'ENOENT') throw error;
            });
            await rename(file, `${file}.1`);
            handle = await open(file, 'a', 0o600);
            size = 0;
          }
          await handle.writeFile(line);
          size += bytes;
        })
        .catch(() => {
          dropped++;
        })
        .finally(() => {
          pending--;
        });
    },
    get dropped() {
      return dropped;
    },
    async close() {
      closed = true;
      await tail;
      await handle?.close().catch(() => {});
      handle = undefined;
      await lock?.close().catch(() => {});
      lock = undefined;
      if (lockPath) await unlink(lockPath).catch(() => {});
    },
  };
}
