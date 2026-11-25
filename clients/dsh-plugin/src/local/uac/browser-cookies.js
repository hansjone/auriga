import { createDecipheriv, createHash } from 'node:crypto';
import { execFile } from 'node:child_process';
import {
  copyFileSync,
  existsSync,
  mkdtempSync,
  readdirSync,
  readFileSync,
  rmSync,
} from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { promisify } from 'node:util';

const execFileAsync = promisify(execFile);

/** Derive a cookie domain from a configured domain or UAC base URL.
 * Preserves explicit hostnames (e.g. `aip.corp.example`) so RFC 6265 parent-domain
 * cookie matching works when `cookies.domain` differs from `provider.baseUrl`.
 * @param {string|undefined} configured @param {string} baseUrl */
export function resolveCookieDomain(configured, baseUrl) {
  const raw = (configured ?? '').trim().toLowerCase();
  if (raw) return raw;
  try {
    const host = new URL(baseUrl).hostname.toLowerCase();
    const parts = host.split('.').filter(Boolean);
    if (parts.length <= 2) return `.${host}`;
    // Keep last 3 labels for multi-part ccTLDs like .com.cn / .co.uk
    const multiPart =
      parts[parts.length - 1].length === 2 &&
      ['com', 'net', 'org', 'gov', 'edu', 'co', 'ac'].includes(
        parts[parts.length - 2],
      );
    return '.' + parts.slice(multiPart ? -3 : -2).join('.');
  } catch {
    return '';
  }
}

/** RFC 6265-aware domain match supporting both suffix configs (`.corp.example`)
 * and explicit portal hosts (`aip.corp.example` matching parent `.corp.example` cookies).
 * @param {string} hostKey @param {string} configuredDomain */
export function domainMatches(hostKey, configuredDomain) {
  if (!hostKey || !configuredDomain) return false;
  const host = hostKey.trim().toLowerCase();
  const target = configuredDomain.trim().toLowerCase();
  const bareTarget = target.startsWith('.') ? target.slice(1) : target;
  if (!bareTarget) return false;
  const dotTarget = `.${bareTarget}`;
  if (host === bareTarget || host === dotTarget) return true;
  if (target.startsWith('.') && host.endsWith(dotTarget)) return true;
  // A parent-domain cookie (e.g. `.corp.example`) applies to `aip.corp.example`.
  if (host.startsWith('.')) {
    const parentLabels = host.slice(1).split('.').filter(Boolean);
    if (parentLabels.length >= 2 && bareTarget.endsWith(host)) return true;
  }
  return false;
}

/** Check whether an encrypted Chromium cookie buffer uses App-Bound Encryption (v20).
 * @param {Buffer|Uint8Array|null|undefined} encrypted */
export function isChromiumV20(encrypted) {
  if (!encrypted || encrypted.length < 3) return false;
  const buf = Buffer.isBuffer(encrypted) ? encrypted : Buffer.from(encrypted);
  return buf.subarray(0, 3).toString('ascii') === 'v20';
}

/** Decrypt a Chromium v10/v11 AES-256-GCM cookie buffer.
 * Supports Chromium 130+ (DB v24+) 32-byte SHA256(host_key) plaintext prefix stripping.
 * Returns null for App-Bound (v20) or unsupported formats.
 * @param {Buffer|Uint8Array} encrypted @param {Buffer} masterKey @param {string} [hostKey] */
export function decryptChromiumValue(encrypted, masterKey, hostKey) {
  if (!encrypted || !masterKey || masterKey.length !== 32) return null;
  const buf = Buffer.isBuffer(encrypted) ? encrypted : Buffer.from(encrypted);
  if (buf.length <= 3 + 12 + 16) return null;
  const prefix = buf.subarray(0, 3).toString('ascii');
  if (prefix !== 'v10' && prefix !== 'v11') return null;
  try {
    const nonce = buf.subarray(3, 15);
    const ciphertext = buf.subarray(15, buf.length - 16);
    const tag = buf.subarray(buf.length - 16);
    const decipher = createDecipheriv('aes-256-gcm', masterKey, nonce);
    decipher.setAuthTag(tag);
    let plain = Buffer.concat([decipher.update(ciphertext), decipher.final()]);
    if (typeof hostKey === 'string' && hostKey && plain.length >= 32) {
      const expectedHash = createHash('sha256')
        .update(hostKey, 'utf8')
        .digest();
      if (plain.subarray(0, 32).equals(expectedHash)) {
        plain = plain.subarray(32);
      }
    }
    return plain.toString('utf8');
  } catch {
    return null;
  }
}

/** Decrypt Windows DPAPI blob using current user scope via PowerShell.
 * @param {Buffer} blob */
async function unprotectWindowsDpapi(blob) {
  if (process.platform !== 'win32') return null;
  const b64 = blob.toString('base64');
  const script = [
    'Add-Type -AssemblyName System.Security',
    '$raw = [Convert]::FromBase64String($env:AURIGA_DPAPI_B64)',
    '$out = [System.Security.Cryptography.ProtectedData]::Unprotect($raw, $null, [System.Security.Cryptography.DataProtectionScope]::CurrentUser)',
    '[Console]::Out.Write([Convert]::ToBase64String($out))',
  ].join('; ');
  try {
    const { stdout } = await execFileAsync(
      'powershell.exe',
      ['-NoProfile', '-NonInteractive', '-Command', script],
      {
        timeout: 5000,
        windowsHide: true,
        maxBuffer: 65536,
        env: { ...process.env, AURIGA_DPAPI_B64: b64 },
      },
    );
    const decoded = Buffer.from(stdout.trim(), 'base64');
    return decoded.length === 32 ? decoded : null;
  } catch {
    return null;
  }
}

/** Copy a possibly-locked file on Windows using shared read/write/delete mode.
 * Paths are passed via environment variables to avoid shell/quote interpolation.
 * @param {string} source @param {string} dest */
async function copySharedFile(source, dest) {
  try {
    copyFileSync(source, dest);
    return true;
  } catch {
    if (process.platform !== 'win32') return false;
  }
  try {
    const script = [
      '$s = [System.IO.File]::Open($env:AURIGA_SRC, [System.IO.FileMode]::Open, [System.IO.FileAccess]::Read, [System.IO.FileShare]::ReadWrite -bor [System.IO.FileShare]::Delete)',
      'try { $d = [System.IO.File]::Create($env:AURIGA_DST); try { $s.CopyTo($d) } finally { $d.Dispose() } } finally { $s.Dispose() }',
    ].join('; ');
    await execFileAsync(
      'powershell.exe',
      ['-NoProfile', '-NonInteractive', '-Command', script],
      {
        timeout: 5000,
        windowsHide: true,
        env: { ...process.env, AURIGA_SRC: source, AURIGA_DST: dest },
      },
    );
    return existsSync(dest);
  } catch {
    return false;
  }
}

/** Copy a SQLite database together with optional -wal and -shm sidecar files.
 * @param {string} source @param {string} dest */
async function copySqliteWithWal(source, dest) {
  const ok = await copySharedFile(source, dest);
  if (!ok) return false;
  for (const suffix of ['-wal', '-shm']) {
    const sidecar = source + suffix;
    if (existsSync(sidecar)) {
      if (!(await copySharedFile(sidecar, dest + suffix))) return false;
    }
  }
  return true;
}

/** Load node:sqlite lazily so older runtimes degrade cleanly. */
async function loadSqlite() {
  try {
    const mod = await import('node:sqlite');
    return mod.DatabaseSync ?? null;
  } catch {
    return null;
  }
}

/** @param {string} userDataDir */
function chromiumProfileCookieFiles(userDataDir) {
  if (!existsSync(userDataDir)) return [];
  const candidates = [];
  let entries = [];
  try {
    entries = readdirSync(userDataDir, { withFileTypes: true });
  } catch {
    return [];
  }
  const profileNames = entries
    .filter(
      (e) =>
        e.isDirectory() &&
        (e.name === 'Default' || e.name.startsWith('Profile ')),
    )
    .map((e) => e.name)
    .sort((a, b) =>
      a === 'Default' ? -1 : b === 'Default' ? 1 : a.localeCompare(b),
    );
  for (const name of profileNames) {
    for (const rel of [
      join(name, 'Network', 'Cookies'),
      join(name, 'Cookies'),
    ]) {
      const full = join(userDataDir, rel);
      if (existsSync(full)) {
        candidates.push(full);
        break;
      }
    }
  }
  return candidates;
}

/** Read matching cookies per Chromium profile without merging across profiles.
 * @param {any} DatabaseSync
 * @param {string} userDataDir
 * @param {string} configuredDomain
 * @param {string[]} names
 * @param {number} nowMs
 * @returns {Promise<{maps:Map<string,string>[],v20Detected:boolean,unreadable?:boolean}>} */
async function readChromiumUserData(
  DatabaseSync,
  userDataDir,
  configuredDomain,
  names,
  nowMs,
) {
  const localStatePath = join(userDataDir, 'Local State');
  if (!existsSync(localStatePath)) return { maps: [], v20Detected: false };
  let masterKey = null;
  try {
    const state = JSON.parse(readFileSync(localStatePath, 'utf8'));
    const encB64 = state?.os_crypt?.encrypted_key;
    if (typeof encB64 === 'string' && encB64) {
      const raw = Buffer.from(encB64, 'base64');
      if (raw.subarray(0, 5).toString('ascii') === 'DPAPI') {
        masterKey = await unprotectWindowsDpapi(raw.subarray(5));
      }
    }
  } catch {
    return { maps: [], v20Detected: false, unreadable: true };
  }

  // Chromium timestamps are microseconds since 1601-01-01.
  const nowChromeMicros = BigInt(nowMs) * 1000n + 11644473600000000n;
  const cookieFiles = chromiumProfileCookieFiles(userDataDir);
  /** @type {Map<string,string>[]} */ const maps = [];
  let v20Detected = false;
  let unreadable = false;
  const placeholders = names.map(() => '?').join(',');
  const sql = `SELECT host_key, name, value, encrypted_value, expires_utc, last_access_utc FROM cookies WHERE name IN (${placeholders}) ORDER BY last_access_utc DESC`;

  for (const file of cookieFiles) {
    const dir = mkdtempSync(join(tmpdir(), 'auriga-sso-'));
    const tempDb = join(dir, 'cookies.db');
    let db;
    try {
      const copied = await copySqliteWithWal(file, tempDb);
      if (!copied) {
        unreadable = true;
        continue;
      }
      db = new DatabaseSync(tempDb, { readOnly: true });
      const rows = db.prepare(sql).all(...names);
      /** @type {Map<string,string>} */ const found = new Map();
      for (const row of rows) {
        const hostKey = String(row.host_key ?? '');
        if (!domainMatches(hostKey, configuredDomain)) continue;
        const exp = BigInt(row.expires_utc ?? 0);
        if (exp !== 0n && exp < nowChromeMicros) continue;
        const name = String(row.name ?? '');
        if (!name || found.has(name)) continue;
        let val = typeof row.value === 'string' && row.value ? row.value : null;
        if (!val && row.encrypted_value) {
          if (isChromiumV20(row.encrypted_value)) {
            v20Detected = true;
          } else if (masterKey) {
            val = decryptChromiumValue(row.encrypted_value, masterKey, hostKey);
          }
        }
        if (val) found.set(name, val);
      }
      if (found.size) maps.push(found);
    } catch {
      unreadable = true;
    } finally {
      try {
        db?.close();
      } catch {
        /* ignore */
      }
      try {
        rmSync(dir, { recursive: true, force: true });
      } catch {
        /* ignore */
      }
    }
  }
  return { maps, v20Detected, unreadable };
}

/** Read matching cookies per Firefox profile (plaintext SQLite).
 * @param {any} DatabaseSync
 * @param {string} profilesRoot
 * @param {string} configuredDomain
 * @param {string[]} names
 * @param {number} nowMs
 * @returns {Promise<{maps:Map<string,string>[],unreadable?:boolean}>} */
async function readFirefoxProfiles(
  DatabaseSync,
  profilesRoot,
  configuredDomain,
  names,
  nowMs,
) {
  if (!existsSync(profilesRoot)) return { maps: [] };
  let entries = [];
  try {
    entries = readdirSync(profilesRoot, { withFileTypes: true });
  } catch {
    return { maps: [], unreadable: true };
  }
  const nowSec = Math.floor(nowMs / 1000);
  /** @type {Map<string,string>[]} */ const maps = [];
  let unreadable = false;
  const placeholders = names.map(() => '?').join(',');
  const sql = `SELECT host, name, value, expiry, lastAccessed FROM moz_cookies WHERE name IN (${placeholders}) ORDER BY lastAccessed DESC`;

  for (const entry of entries) {
    if (!entry.isDirectory()) continue;
    const cookiePath = join(profilesRoot, entry.name, 'cookies.sqlite');
    if (!existsSync(cookiePath)) continue;
    const dir = mkdtempSync(join(tmpdir(), 'auriga-sso-ff-'));
    const tempDb = join(dir, 'cookies.sqlite');
    let db;
    try {
      const copied = await copySqliteWithWal(cookiePath, tempDb);
      if (!copied) {
        unreadable = true;
        continue;
      }
      db = new DatabaseSync(tempDb, { readOnly: true });
      const rows = db.prepare(sql).all(...names);
      /** @type {Map<string,string>} */ const found = new Map();
      for (const row of rows) {
        if (!domainMatches(String(row.host ?? ''), configuredDomain)) continue;
        const exp = Number(row.expiry ?? 0);
        if (exp > 0 && exp < nowSec) continue;
        const name = String(row.name ?? '');
        const val = typeof row.value === 'string' ? row.value : '';
        if (name && val && !found.has(name)) found.set(name, val);
      }
      if (found.size) maps.push(found);
    } catch {
      unreadable = true;
    } finally {
      try {
        db?.close();
      } catch {
        /* ignore */
      }
      try {
        rmSync(dir, { recursive: true, force: true });
      } catch {
        /* ignore */
      }
    }
  }
  return { maps, unreadable };
}

/**
 * Read local browser SSO cookie maps per profile, preserving profile isolation
 * and reporting whether App-Bound Encryption (v20) cookies were encountered.
 * Note: Chromium v20 (App-Bound Encryption) is OS-enforced and cannot be
 * decrypted by an unprivileged user process; callers offer QR with a safe reason.
 * @param {{domain:string,names:string[],now?:()=>number}} options
 * @returns {Promise<{maps:Map<string,string>[],v20Detected:boolean,unreadable?:boolean}>}
 */
export async function readLocalBrowserCookieSets({
  domain,
  names,
  now = Date.now,
}) {
  if (!domain || !Array.isArray(names) || !names.length)
    return { maps: [], v20Detected: false };
  const uniqueNames = [
    ...new Set(names.filter((n) => typeof n === 'string' && n)),
  ];
  if (!uniqueNames.length) return { maps: [], v20Detected: false };
  const DatabaseSync = await loadSqlite();
  if (!DatabaseSync) return { maps: [], v20Detected: false, unreadable: true };

  const nowMs = now();
  /** @type {Map<string,string>[]} */ const maps = [];
  let v20Detected = false;
  let unreadable = false;
  const includeFirefox = async (/** @type {string} */ root) => {
    const result = await readFirefoxProfiles(
      DatabaseSync,
      root,
      domain,
      uniqueNames,
      nowMs,
    );
    maps.push(...result.maps);
    unreadable ||= !!result.unreadable;
  };

  if (process.platform === 'win32') {
    const localAppData = process.env.LOCALAPPDATA ?? '';
    const appData = process.env.APPDATA ?? '';
    const chromiumRoots = [
      localAppData && join(localAppData, 'Microsoft', 'Edge', 'User Data'),
      localAppData && join(localAppData, 'Google', 'Chrome', 'User Data'),
    ].filter(Boolean);
    for (const root of chromiumRoots) {
      const res = await readChromiumUserData(
        DatabaseSync,
        root,
        domain,
        uniqueNames,
        nowMs,
      );
      maps.push(...res.maps);
      if (res.v20Detected) v20Detected = true;
      unreadable ||= !!res.unreadable;
    }
    if (appData) {
      await includeFirefox(join(appData, 'Mozilla', 'Firefox', 'Profiles'));
    }
  } else if (process.platform === 'linux') {
    const home = process.env.HOME ?? '';
    if (home) {
      await includeFirefox(join(home, '.mozilla', 'firefox'));
    }
  }
  return { maps, v20Detected, unreadable };
}

/**
 * Convenience wrapper returning a merged Map of local browser SSO cookies.
 * @param {{domain:string,names:string[],now?:()=>number}} options
 * @returns {Promise<Map<string,string>>}
 */
export async function readLocalBrowserCookies(options) {
  const { maps } = await readLocalBrowserCookieSets(options);
  /** @type {Map<string,string>} */ const merged = new Map();
  for (const map of maps)
    for (const [k, v] of map) if (!merged.has(k) && v) merged.set(k, v);
  return merged;
}
