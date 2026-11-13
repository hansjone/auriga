import { createHash, randomUUID } from 'node:crypto';
import { uptime } from 'node:os';
import { execFileSync } from 'node:child_process';
import {
  closeSync,
  fsyncSync,
  lstatSync,
  mkdirSync,
  openSync,
  readFileSync,
  realpathSync,
  renameSync,
  rmdirSync,
  unlinkSync,
  writeFileSync,
} from 'node:fs';
import { dirname, isAbsolute, join, parse, resolve } from 'node:path';

const processStartedAt = new Date(
  Date.now() - process.uptime() * 1000,
).toISOString();

let bootId;
// C/E host-lock sources: raw UTC boot identity and OS uptime, never process uptime.
export function hostTime() {
  if (!bootId && process.platform === 'linux')
    bootId = readFileSync('/proc/sys/kernel/random/boot_id', 'utf8').trim();
  if (!bootId && process.platform === 'win32') {
    const result = execFileSync(
      'powershell.exe',
      [
        '-NoProfile',
        '-NonInteractive',
        '-Command',
        "$ErrorActionPreference='Stop'; (Get-CimInstance Win32_OperatingSystem).LastBootUpTime.ToUniversalTime().ToString('o')",
      ],
      { encoding: 'utf8', windowsHide: true, timeout: 10000 },
    ).trim();
    if (
      !/^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\.\d{7}Z$/.test(result) ||
      !Number.isFinite(Date.parse(result))
    )
      throw new Error('unable to read boot identity');
    bootId = result;
  }
  if (!bootId) throw new Error('unsupported guardian platform');
  const monotonicMillis = Math.floor(uptime() * 1000);
  if (!Number.isSafeInteger(monotonicMillis) || monotonicMillis < 0)
    throw new Error('invalid OS uptime');
  return { bootId, monotonicMillis };
}

// UnifiedTrustedClock still requires UUIDs. Keep this encoding separate from the
// raw host-lock identity agreed with C; never change the frozen schema to fit it.
export function trustedClockTime() {
  const sample = hostTime();
  if (process.platform !== 'win32') return sample;
  const hex = createHash('sha256')
    .update(sample.bootId)
    .digest('hex')
    .slice(0, 32);
  return {
    ...sample,
    bootId: `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`,
  };
}

export function assertLocalPath(path) {
  if (!isAbsolute(path) || path.startsWith('\\\\') || path.startsWith('//'))
    throw new Error('local absolute path required');
  let current = parse(resolve(path)).root;
  for (const part of resolve(path).slice(current.length).split(/[\\/]/)) {
    if (!part) continue;
    current = join(current, part);
    let info;
    try {
      info = lstatSync(current);
    } catch (error) {
      if (error.code === 'ENOENT') continue;
      throw error;
    }
    if (info.isSymbolicLink()) throw new Error('guardian path contains a link');
    const canonical = realpathSync.native(current);
    const equal =
      process.platform === 'win32'
        ? canonical.toLowerCase() === current.toLowerCase()
        : canonical === current;
    if (!equal) throw new Error('guardian path alias');
  }
}

export function durableWrite(file, content) {
  assertLocalPath(file);
  const temporary = `${file}.${randomUUID()}.tmp`;
  const fd = openSync(temporary, 'wx', 0o600);
  try {
    writeFileSync(fd, content);
    fsyncSync(fd);
  } finally {
    closeSync(fd);
  }
  renameSync(temporary, file);
  if (process.platform !== 'win32') {
    const parent = openSync(dirname(file), 'r');
    try {
      fsyncSync(parent);
    } finally {
      closeSync(parent);
    }
  }
}

export const durableJson = (file, value) =>
  durableWrite(file, JSON.stringify(value) + '\n');

export function acquireHostLock(dataHome, role = 'E', time = hostTime) {
  assertLocalPath(dataHome);
  const parent = join(dataHome, 'auriga', 'guardian');
  mkdirSync(parent, { recursive: true });
  assertLocalPath(parent);
  const lock = join(parent, 'host.lock.d');
  const stamp = time();
  mkdirSync(lock); // EEXIST is always a losing contender, including incomplete owner records.
  const owner = {
    schemaVersion: 1,
    lockId: randomUUID(),
    role,
    pid: process.pid,
    processStartedAt,
    createdAt: new Date().toISOString(),
    ...stamp,
    leaseDurationMillis: 30000,
  };
  try {
    durableJson(join(lock, 'owner.json'), owner);
  } catch (error) {
    throw new Error(
      'lock owner persistence failed; retained for guardian recovery',
      { cause: error },
    );
  }
  const check = () => {
    assertLocalPath(lock);
    const actual = JSON.parse(readFileSync(join(lock, 'owner.json'), 'utf8'));
    const now = time();
    if (
      actual.lockId !== owner.lockId ||
      now.bootId !== owner.bootId ||
      now.monotonicMillis < owner.monotonicMillis ||
      now.monotonicMillis - owner.monotonicMillis > 30000
    )
      throw new Error('host lock ownership lost or expired');
  };
  return {
    owner,
    check,
    release() {
      check();
      unlinkSync(join(lock, 'owner.json'));
      rmdirSync(lock);
    },
  };
}

// Recovery policy is exposed separately: callers must establish host quiescence before rename.
export function evaluateHostLock(input) {
  const result = (decision, reason) => ({ decision, reason });
  if (input.operation === 'reclaim' && input.callerRole !== 'E')
    return result('reject', 'guardian_only');
  if (!input.owner) return result('wait', 'unverifiable_owner');
  if (input.owner.lockId !== input.observedLockId)
    return result('reject', 'owner_changed');
  if (input.operation === 'release')
    return input.callerRole === input.owner.role &&
      input.callerPid === input.owner.pid
      ? result('allow', 'owner_release')
      : result('reject', 'not_owner');
  if (!input.newStartsBlocked || !input.otherWritersStopped)
    return result('wait', 'quiesce_host_first');
  if (input.holderStatus === 'dead') return result('allow', 'owner_dead');
  if (input.holderStatus !== 'alive')
    return result('wait', 'unverifiable_owner');
  if (
    input.bootId !== input.owner.bootId ||
    input.monotonicNowMillis - input.owner.monotonicMillis <=
      input.owner.leaseDurationMillis
  )
    return result('wait', 'owner_alive');
  return input.writerStopped
    ? result('allow', 'lease_expired')
    : result('wait', 'stop_writer_first');
}
