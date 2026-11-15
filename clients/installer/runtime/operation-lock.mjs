import { randomUUID, createHash } from 'node:crypto';
import { performance } from 'node:perf_hooks';
import {
  existsSync,
  mkdirSync,
  readdirSync,
  renameSync,
  rmdirSync,
  unlinkSync,
  writeFileSync,
} from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { assertLocalPath } from './host-lock.mjs';

export function processAlive(pid) {
  if (!Number.isSafeInteger(pid) || pid < 1) return true;
  try {
    process.kill(pid, 0);
    return true;
  } catch (error) {
    return error.code !== 'ESRCH';
  }
}

const ownerName = /^(\d+)-[0-9a-f-]{36}$/;
const sleep = new Int32Array(new SharedArrayBuffer(4));

function removeEmpty(path) {
  try {
    rmdirSync(path);
  } catch (error) {
    if (
      !['ENOENT', 'ENOTEMPTY', 'EEXIST', 'EPERM', 'EACCES'].includes(error.code)
    )
      throw error;
  }
}

/** Publish a nonempty lock directory atomically. Only the exact retired owner
 * file may be removed, and a newly published nonempty directory cannot be removed. */
export function acquireOperationLock(path, waitMs = 0) {
  assertLocalPath(path);
  mkdirSync(dirname(path), { recursive: true });
  const token = randomUUID();
  const owner = `${process.pid}-${token}`;
  const claim = `${path}.${token}.claim`;
  mkdirSync(claim);
  try {
    writeFileSync(join(claim, owner), '', { flag: 'wx', mode: 0o600 });
    const start = performance.now();
    for (;;) {
      try {
        renameSync(claim, path);
        return () => {
          unlinkSync(join(path, owner));
          removeEmpty(path);
        };
      } catch (error) {
        if (!['EEXIST', 'ENOTEMPTY', 'EPERM', 'EACCES'].includes(error.code))
          throw error;
      }
      assertLocalPath(path);
      let entries;
      try {
        entries = readdirSync(path);
      } catch (error) {
        if (error.code !== 'ENOENT') throw error;
        if (performance.now() - start < waitMs) continue;
        throw error;
      }
      if (entries.length === 0) {
        removeEmpty(path);
        if (!existsSync(path)) continue;
      } else if (entries.length === 1) {
        const previous = ownerName.exec(entries[0]);
        if (previous && !processAlive(Number(previous[1]))) {
          try {
            unlinkSync(join(path, entries[0]));
          } catch (error) {
            if (error.code !== 'ENOENT') throw error;
          }
          removeEmpty(path);
          continue;
        }
      }
      if (performance.now() - start >= waitMs)
        throw Object.assign(
          new Error('Auriga DSH 已在运行或正在更改，请先退出或稍后重试。'),
          {
            code: 'desktop.in_use',
          },
        );
      Atomics.wait(sleep, 0, 0, 15);
    }
  } finally {
    if (existsSync(claim)) {
      try {
        unlinkSync(join(claim, owner));
        removeEmpty(claim);
      } catch {
        /* A failed unpublished claim cannot block acquisitions. */
      }
    }
  }
}

export function pathOperationLock(path, scope = 'version', waitMs = 0) {
  path = resolve(path);
  const key = process.platform === 'win32' ? path.toLowerCase() : path;
  const hash = createHash('sha256').update(key).digest('hex').slice(0, 24);
  // All launchers for a Windows user inherit this location. A writable existing
  // install can reside under a parent directory where that user cannot write.
  const local = process.platform === 'win32' && process.env.LOCALAPPDATA;
  const directory = local
    ? join(local, 'AurigaDesktop', 'locks')
    : dirname(path);
  return acquireOperationLock(
    join(directory, `.auriga-desktop-${scope}-${hash}.lock`),
    waitMs,
  );
}
