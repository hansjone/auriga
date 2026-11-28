import { randomUUID, createHash } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { performance } from 'node:perf_hooks';
import {
  existsSync,
  lstatSync,
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
import { systemTool } from './system-tools.mjs';

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

function inspectLockDirectory(lockDir) {
  const details = {
    lockDir,
    exists: false,
    kind: 'missing',
  };
  try {
    const st = lstatSync(lockDir);
    details.exists = true;
    details.kind = st.isSymbolicLink()
      ? 'symlink'
      : st.isDirectory()
        ? 'directory'
        : st.isFile()
          ? 'file'
          : 'other';
  } catch (error) {
    details.statCode = error.code ?? 'UNKNOWN';
  }
  if (process.platform === 'win32') {
    const user = [process.env.USERDOMAIN, process.env.USERNAME]
      .filter(Boolean)
      .join('\\');
    const profile = process.env.USERPROFILE
      ? resolve(process.env.USERPROFILE).toLowerCase()
      : null;
    const local = process.env.LOCALAPPDATA
      ? resolve(process.env.LOCALAPPDATA).toLowerCase()
      : null;
    details.user = user || 'unknown';
    if (profile && local) {
      details.localAppDataMatchesProfile =
        local === profile || local.startsWith(profile + '\\');
    }
    if (details.exists) {
      try {
        const acl = spawnSync(systemTool('icacls'), [lockDir], {
          encoding: 'utf8',
          windowsHide: true,
          timeout: 3000,
        });
        if (acl.status === 0 && acl.stdout) {
          details.acl = acl.stdout
            .split(/\r?\n/)
            .map((line) => line.trim())
            .filter((line) => line && !/Successfully processed/i.test(line))
            .slice(0, 8)
            .join(' | ');
        }
      } catch {
        /* best-effort diagnostics only */
      }
    }
  }
  return details;
}

function mkdirWithRetry(
  target,
  options,
  { mkdir = mkdirSync, delays = [50, 150, 300, 600] } = {},
) {
  for (let attempt = 0; ; attempt++) {
    try {
      mkdir(target, options);
      return;
    } catch (error) {
      if (
        !['EPERM', 'EBUSY', 'EACCES'].includes(error.code) ||
        attempt >= delays.length
      )
        throw error;
      Atomics.wait(sleep, 0, 0, delays[attempt]);
    }
  }
}

function lockAccessError(stage, lockDir, error, { diagnose = true } = {}) {
  const diag = diagnose ? inspectLockDirectory(lockDir) : { lockDir };
  const payload = {
    code: 'desktop.lock_access_denied',
    stage,
    errno: error.code ?? 'UNKNOWN',
    ...diag,
  };
  console.error(`[desktop.lock_access_denied] ${JSON.stringify(payload)}`);
  const hint =
    payload.localAppDataMatchesProfile === false
      ? `检测到提权账号（${payload.user}）与 LOCALAPPDATA 所属用户目录不一致，请使用当前登录账号权限重试。`
      : '可能是终端安全软件/杀软拦截了安装进程，或该目录权限受限。请将安装目录与该锁目录加入白名单后重试。';
  return Object.assign(
    new Error(
      `无法创建操作锁目录（${payload.errno}）：${lockDir}。${hint}[desktop.lock_access_denied]`,
    ),
    {
      code: 'desktop.lock_access_denied',
      errno: error.code,
      cause: error,
      diagnostics: payload,
    },
  );
}

/** Publish a nonempty lock directory atomically. Only the exact retired owner
 * file may be removed, and a newly published nonempty directory cannot be removed. */
export function acquireOperationLock(path, waitMs = 0, ops = {}) {
  const renameDir = ops.renameSync ?? renameSync;
  const readDir = ops.readdirSync ?? readdirSync;
  const makeDir = ops.mkdirSync ?? mkdirSync;
  const mkdirDelays = ops.mkdirRetryDelays ?? [50, 150, 300, 600];
  assertLocalPath(path);
  const lockDir = dirname(path);
  try {
    mkdirWithRetry(
      lockDir,
      { recursive: true },
      { mkdir: makeDir, delays: mkdirDelays },
    );
  } catch (error) {
    if (['EPERM', 'EACCES'].includes(error.code))
      throw lockAccessError('mkdir-parent', lockDir, error, {
        diagnose: ops.diagnose !== false,
      });
    throw error;
  }
  const token = randomUUID();
  const owner = `${process.pid}-${token}`;
  // Avoid leading dot on the temporary claim directory: Windows EDR/AV filters
  // often block unsigned/temp installers from creating dot-prefixed entries.
  const claim = join(lockDir, `auriga-lock-claim-${token}`);
  try {
    mkdirWithRetry(claim, undefined, { mkdir: makeDir, delays: mkdirDelays });
  } catch (error) {
    if (['EPERM', 'EACCES'].includes(error.code))
      throw lockAccessError('mkdir-claim', lockDir, error, {
        diagnose: ops.diagnose !== false,
      });
    throw error;
  }
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

function hasActiveLockOwner(lockPath) {
  if (!existsSync(lockPath)) return false;
  try {
    const entries = readdirSync(lockPath);
    if (entries.length === 0) return false;
    if (entries.length === 1) {
      const previous = ownerName.exec(entries[0]);
      if (previous && retiredOwner(lockPath, entries[0], Number(previous[1])))
        return false;
    }
    return true;
  } catch {
    return false;
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
  // Respect an active legacy dot-prefixed lock held by an older running binary,
  // while creating new locks without a leading dot to avoid Windows EDR blocks.
  const legacyLock = join(directory, `.auriga-desktop-${scope}-${hash}.lock`);
  const legacyDeadline = performance.now() + waitMs;
  while (hasActiveLockOwner(legacyLock)) {
    if (performance.now() >= legacyDeadline) {
      throw Object.assign(
        new Error('Auriga DSH 已在运行或正在更改，请先退出或稍后重试。'),
        { code: 'desktop.in_use' },
      );
    }
    Atomics.wait(sleep, 0, 0, 15);
  }
  return acquireOperationLock(
    join(directory, `auriga-desktop-${scope}-${hash}.lock`),
    waitMs,
  );
}
