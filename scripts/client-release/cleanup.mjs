/**
 * 受限目录安全清理与错误保留工具（客户端依赖准备与现场派生共用）。
 * - 强制路径约束（within），拒绝删除边界外路径；
 * - 绝不跟随符号链接或 Windows NTFS Junction 删除外部目标；
 * - 容忍有界的 Windows 瞬态删除失败（ENOTEMPTY/EPERM/EBUSY 等）与长路径/悬空 Junction；
 * - 二次清理失败时保留原始异常，同时报告清理阶段、路径与清理错误。
 */
import {
  chmodSync,
  lstatSync,
  readdirSync,
  rmdirSync,
  rmSync,
  unlinkSync,
} from 'node:fs';
import {
  isAbsolute,
  join,
  relative,
  resolve,
  toNamespacedPath,
} from 'node:path';

const RETRYABLE_RM_CODES = new Set([
  'ENOTEMPTY',
  'EPERM',
  'EBUSY',
  'EMFILE',
  'ENFILE',
  'EACCES',
]);

const DEFAULT_MAX_RETRIES = 5;
const DEFAULT_RETRY_DELAY_MS = 150;
const SLEEP_BUFFER = new Int32Array(new SharedArrayBuffer(4));

function defaultSleep(ms) {
  if (ms > 0) Atomics.wait(SLEEP_BUFFER, 0, 0, ms);
}

function normalizeForCompare(path) {
  const resolved = resolve(path);
  return process.platform === 'win32' ? resolved.toLowerCase() : resolved;
}

/** 校验 target 是否在 within 边界内；默认要求严格子路径（allowEqual=false）。 */
export function assertWithinBoundary(
  target,
  within,
  { allowEqual = false } = {},
) {
  if (!within) throw new Error('安全清理必须提供 within 边界目录');
  const targetAbs = resolve(target);
  const withinAbs = resolve(within);
  const rel = relative(
    normalizeForCompare(withinAbs),
    normalizeForCompare(targetAbs),
  );
  const inside =
    (allowEqual && rel === '') ||
    (rel !== '' && !rel.startsWith('..') && !isAbsolute(rel));
  if (!inside)
    throw new Error(
      `拒绝清理边界外路径：target=${targetAbs} within=${withinAbs}`,
    );
  return { targetAbs, withinAbs };
}

function fsPath(path) {
  return process.platform === 'win32' ? toNamespacedPath(path) : path;
}

function removeLinkOrReparsePoint(path) {
  const p = fsPath(path);
  try {
    unlinkSync(p);
    return;
  } catch (error) {
    if (error?.code === 'ENOENT') return;
    if (process.platform !== 'win32') throw error;
  }
  try {
    rmdirSync(p);
  } catch (error) {
    if (error?.code === 'ENOENT') return;
    throw error;
  }
}

function removeFile(path) {
  const p = fsPath(path);
  try {
    unlinkSync(p);
  } catch (error) {
    if (error?.code === 'ENOENT') return;
    if (error?.code === 'EPERM' || error?.code === 'EACCES') {
      try {
        chmodSync(p, 0o600);
      } catch {
        // 尽力去掉只读位后重试。
      }
      try {
        unlinkSync(p);
        return;
      } catch (retryError) {
        if (retryError?.code === 'ENOENT') return;
        throw retryError;
      }
    }
    throw error;
  }
}

/**
 * 自底向上显式拆除目录树：
 * - 不跟随任何 symlink / Windows Junction，直接移除链接/reparse point 本身；
 * - 若 readdir 列出条目但 lstat 报 ENOENT（Windows 悬空/长路径 reparse point），
 *   尝试按 reparse point 移除自身，绝不递归进入。
 */
function dismantleTreeSafely(dirAbs, withinAbs) {
  assertWithinBoundary(dirAbs, withinAbs, { allowEqual: true });
  const dirFs = fsPath(dirAbs);
  let dirStat;
  try {
    dirStat = lstatSync(dirFs);
  } catch (error) {
    if (error?.code === 'ENOENT') return;
    throw error;
  }
  if (dirStat.isSymbolicLink()) {
    removeLinkOrReparsePoint(dirAbs);
    return;
  }
  if (!dirStat.isDirectory()) {
    removeFile(dirAbs);
    return;
  }
  let entries;
  try {
    entries = readdirSync(dirFs, { withFileTypes: true });
  } catch (error) {
    if (error?.code === 'ENOENT') return;
    throw error;
  }
  for (const entry of entries) {
    const childAbs = join(dirAbs, entry.name);
    assertWithinBoundary(childAbs, withinAbs, { allowEqual: false });
    if (entry.isSymbolicLink()) {
      removeLinkOrReparsePoint(childAbs);
      continue;
    }
    let childStat;
    try {
      childStat = lstatSync(fsPath(childAbs));
    } catch (error) {
      if (error?.code === 'ENOENT') {
        // Windows 悬空/异常 reparse point：readdir 可见但 lstat 报 ENOENT。
        removeLinkOrReparsePoint(childAbs);
        continue;
      }
      throw error;
    }
    if (childStat.isSymbolicLink()) {
      removeLinkOrReparsePoint(childAbs);
    } else if (childStat.isDirectory()) {
      dismantleTreeSafely(childAbs, withinAbs);
    } else {
      removeFile(childAbs);
    }
  }
  try {
    rmdirSync(dirFs);
  } catch (error) {
    if (error?.code === 'ENOENT') return;
    if (error?.code === 'EPERM' || error?.code === 'EACCES') {
      try {
        chmodSync(dirFs, 0o700);
      } catch {
        // 尽力放宽目录权限后重试。
      }
      try {
        rmdirSync(dirFs);
        return;
      } catch (retryError) {
        if (retryError?.code === 'ENOENT') return;
        throw retryError;
      }
    }
    throw error;
  }
}

function pathStillPresent(path) {
  try {
    lstatSync(fsPath(path));
    return true;
  } catch (error) {
    return error?.code !== 'ENOENT';
  }
}

/**
 * 安全删除边界内目录，容忍有界重试与 Windows 悬空 Junction。
 */
export function removeDirSafely(target, options = {}) {
  const {
    within,
    allowEqual = false,
    phase = 'cleanup',
    maxRetries = DEFAULT_MAX_RETRIES,
    retryDelayMs = DEFAULT_RETRY_DELAY_MS,
    sleep = defaultSleep,
    rm = rmSync,
    dismantle = dismantleTreeSafely,
  } = options;
  const { targetAbs, withinAbs } = assertWithinBoundary(target, within, {
    allowEqual,
  });
  if (!pathStillPresent(targetAbs)) return;

  // 若目标本身是符号链接/Junction，仅移除链接本身，绝不跟随删除目标。
  try {
    if (lstatSync(fsPath(targetAbs)).isSymbolicLink()) {
      removeLinkOrReparsePoint(targetAbs);
      return;
    }
  } catch (error) {
    if (error?.code === 'ENOENT') return;
    throw error;
  }

  let lastError = null;
  for (let attempt = 0; attempt <= maxRetries; attempt += 1) {
    try {
      rm(fsPath(targetAbs), { recursive: true, force: true });
      if (!pathStillPresent(targetAbs)) return;
    } catch (error) {
      lastError = error;
      if (!RETRYABLE_RM_CODES.has(error?.code)) break;
      try {
        dismantle(targetAbs, withinAbs);
        if (!pathStillPresent(targetAbs)) return;
      } catch (dismantleError) {
        lastError = dismantleError;
        if (!RETRYABLE_RM_CODES.has(dismantleError?.code)) break;
      }
      if (attempt < maxRetries) sleep(retryDelayMs * (attempt + 1));
    }
  }
  const detail =
    lastError instanceof Error
      ? lastError.message
      : String(lastError ?? '目录仍存在');
  const wrapped = new Error(
    `清理目录失败 [${phase}] ${targetAbs}：${detail}`,
    lastError instanceof Error ? { cause: lastError } : undefined,
  );
  wrapped.phase = phase;
  wrapped.path = targetAbs;
  if (lastError && typeof lastError === 'object') {
    if ('code' in lastError) wrapped.code = lastError.code;
    if ('errno' in lastError) wrapped.errno = lastError.errno;
    if ('syscall' in lastError) wrapped.syscall = lastError.syscall;
  }
  throw wrapped;
}

/**
 * 在 catch 分支执行二次清理：清理成功则原样重抛 originalError；
 * 清理也失败时保留 originalError（附带 cleanupError、阶段与路径），避免掩盖主因。
 */
export function cleanupAfterError(
  originalError,
  cleanupFn,
  { phase = 'cleanup', path = '' } = {},
) {
  try {
    cleanupFn();
  } catch (cleanupError) {
    const origMsg =
      originalError instanceof Error
        ? originalError.message
        : String(originalError);
    const cleanMsg =
      cleanupError instanceof Error
        ? cleanupError.message
        : String(cleanupError);
    const location = path ? ` ${path}` : '';
    const combined = new Error(
      `${origMsg}（后续清理失败 [${phase}]${location}：${cleanMsg}）`,
      originalError instanceof Error ? { cause: originalError } : undefined,
    );
    combined.originalError = originalError;
    combined.cleanupError = cleanupError;
    combined.cleanupPhase = phase;
    combined.cleanupPath = path;
    if (
      originalError &&
      typeof originalError === 'object' &&
      'code' in originalError
    )
      combined.code = originalError.code;
    throw combined;
  }
  throw originalError;
}
