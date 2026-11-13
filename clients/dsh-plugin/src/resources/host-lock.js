import { commitMoves } from './file-commit.js';
import { uptime } from 'node:os';
import { createHash, randomUUID } from 'node:crypto';
import { execFileSync } from 'node:child_process';
import {
  closeSync,
  fsyncSync,
  lstatSync,
  mkdirSync,
  openSync,
  readFileSync,
  realpathSync,
  rmdirSync,
  unlinkSync,
  writeFileSync,
  statfsSync,
} from 'node:fs';
import { isAbsolute, join, parse, resolve } from 'node:path';
import { parseStrictJson } from './json.js';
import { requireSchema } from './schema.js';

const processStartedAt = new Date(
  Date.now() - process.uptime() * 1000,
).toISOString();
let cachedBootId = '';
const checkedDrives = new Set();

// Both values are host boot scoped, never Node's process-relative performance.now().
export function hostTime() {
  const bootId =
    cachedBootId ||
    (process.platform === 'linux'
      ? readFileSync('/proc/sys/kernel/random/boot_id', 'utf8').trim()
      : process.platform === 'win32'
        ? execFileSync(
            'powershell.exe',
            [
              '-NoProfile',
              '-NonInteractive',
              '-Command',
              "(Get-CimInstance Win32_OperatingSystem).LastBootUpTime.ToUniversalTime().ToString('o')",
            ],
            { encoding: 'utf8', windowsHide: true, timeout: 10000 },
          ).trim()
        : '');
  if (!bootId) throw new Error('unsupported host boot clock');
  cachedBootId = bootId;
  return { bootId, monotonicMillis: Math.floor(uptime() * 1000) };
}

/** Same encoding as E's trustedClockTime: UUID Linux boot identity, Windows SHA-256 prefix.
 * The raw host-lock identity remains untouched.
 * @param {string} raw */
export function clockBootId(raw) {
  if (/^[a-f0-9]{8}(?:-[a-f0-9]{4}){3}-[a-f0-9]{12}$/i.test(raw)) return raw;
  if (!/^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\.\d{3,7}Z$/.test(raw))
    throw new Error('unverifiable boot identity');
  const h = createHash('sha256').update(raw).digest('hex').slice(0, 32);
  return (
    h.slice(0, 8) +
    '-' +
    h.slice(8, 12) +
    '-' +
    h.slice(12, 16) +
    '-' +
    h.slice(16, 20) +
    '-' +
    h.slice(20)
  );
}

/** @param {string} path */
export function assertLocalPath(path) {
  if (!isAbsolute(path) || path.startsWith('\\\\') || path.startsWith('//'))
    throw new Error('local absolute path required');
  let current = parse(resolve(path)).root;
  if (process.platform === 'win32' && !checkedDrives.has(current)) {
    if (!/^[A-Za-z]:\\$/.test(current))
      throw new Error('local NTFS volume required');
    const result = execFileSync(
      'powershell.exe',
      [
        '-NoProfile',
        '-NonInteractive',
        '-Command',
        `$taskVolume=Get-CimInstance Win32_LogicalDisk -Filter "DeviceID='${current.slice(0, 2)}'"; Write-Output ($taskVolume.FileSystem+' '+$taskVolume.DriveType)`,
      ],
      { encoding: 'utf8', windowsHide: true, timeout: 10000 },
    ).trim();
    if (result !== 'NTFS 3')
      throw new Error('fixed local NTFS volume required');
    checkedDrives.add(current);
  }
  for (const part of resolve(path).slice(current.length).split(/[\\/]/)) {
    if (!part) continue;
    current = join(current, part);
    let info;
    try {
      info = lstatSync(current);
    } catch (error) {
      if (/** @type {any} */ (error).code === 'ENOENT') continue;
      throw error;
    }
    if (info.isSymbolicLink()) throw new Error('guardian path contains a link');
    if (
      process.platform === 'linux' &&
      ![
        0xef53, 0x58465342, 0x9123683e, 0x01021994, 0x794c7630, 0x2fc12fc1,
      ].includes(Number(statfsSync(current).type))
    )
      throw new Error('unsupported or non-local guardian filesystem');
    const canonical = realpathSync.native(current);
    const equal =
      process.platform === 'win32'
        ? canonical.toLowerCase() === current.toLowerCase()
        : canonical === current;
    if (!equal) throw new Error('guardian path alias');
  }
}

/** @param {string} file @param {any} value */
export function durableJson(file, value) {
  durableRecords([[file, value]]);
}
/** @param {[string,any][]} records */
export function durableRecords(records) {
  /** @type {[string,string][]} */ const moves = [];
  for (const [file, value] of records) {
    assertLocalPath(file);
    const temporary = file + '.' + randomUUID() + '.tmp';
    const fd = openSync(temporary, 'wx', 0o600);
    try {
      writeFileSync(fd, JSON.stringify(value) + '\n');
      fsyncSync(fd);
    } finally {
      closeSync(fd);
    }
    moves.push([temporary, file]);
  }
  commitMoves(moves);
}

/** @param {string} dataHome @param {'C'} [role] @param {typeof hostTime} [time] */
export function acquireHostLock(dataHome, role = 'C', time = hostTime) {
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
    const ownerFile = join(lock, 'owner.json');
    assertLocalPath(ownerFile);
    if (lstatSync(ownerFile).size > 65536)
      throw new Error('invalid host lock owner');
    const actual = requireSchema(
      'UnifiedHostLockOwner',
      parseStrictJson(readFileSync(ownerFile), true),
    );
    const now = time();
    if (
      actual.lockId !== owner.lockId ||
      JSON.stringify(actual) !== JSON.stringify(owner) ||
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
