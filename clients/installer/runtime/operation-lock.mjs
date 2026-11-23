import { randomUUID, createHash } from 'node:crypto';
import { performance } from 'node:perf_hooks';
import {
  existsSync,
  mkdirSync,
  readFileSync,
  readdirSync,
  renameSync,
  rmdirSync,
  unlinkSync,
  writeFileSync,
} from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { assertLocalPath } from './host-lock.mjs';
import { processIdentity, sameProcess } from './process-tree.mjs';

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

function retiredOwner(path, name, pid) {
  if (!processAlive(pid)) return true;
  if (process.platform !== 'win32') return false;
  try {
    const stored = JSON.parse(readFileSync(join(path, name), 'utf8'));
    if (stored?.pid !== pid || !Number.isFinite(Date.parse(stored.createdAt)))
      return false;
    const actual = processIdentity(pid);
    return Boolean(actual && !sameProcess(actual, stored));
  } catch {
    return false;
  }
}

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
export function acquireOperationLock(path, waitMs = 0, ops = {}) {
  const renameDir = ops.renameSync ?? renameSync;
  const readDir = ops.readdirSync ?? readdirSync;
  assertLocalPath(path);
  mkdirSync(dirname(path), { recursive: true });
  const token = randomUUID();
  const owner = `${process.pid}-${token}`;
  const claim = `${path}.${token}.claim`;
  mkdirSync(claim);
  try {
    const identity =
      process.platform === 'win32' ? processIdentity(process.pid) : null;
    writeFileSync(
      join(claim, owner),
      JSON.stringify(identity ?? { pid: process.pid }),
      { flag: 'wx', mode: 0o600 },
    );
    const start = performance.now();
    // On Windows (e.g. LTSC / endpoint filter drivers scanning the newly written
    // owner file inside claim), the initial directory rename can transiently fail
    // with EPERM/EACCES even when the target lock directory does not exist yet.
    let missingTargetRetries = 20;
    for (;;) {
      try {
        renameDir(claim, path);
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
        entries = readDir(path);
      } catch (error) {
        if (error.code !== 'ENOENT') throw error;
        if (performance.now() - start < waitMs) continue;
        if (missingTargetRetries > 0) {
          missingTargetRetries--;
          Atomics.wait(sleep, 0, 0, 15);
          continue;
        }
        throw error;
      }
      if (entries.length === 0) {
        removeEmpty(path);
        if (!existsSync(path)) continue;
      } else if (entries.length === 1) {
        const previous = ownerName.exec(entries[0]);
        if (previous && retiredOwner(path, entries[0], Number(previous[1]))) {
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
