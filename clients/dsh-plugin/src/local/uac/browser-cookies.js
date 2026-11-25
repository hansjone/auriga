import { createDecipheriv } from 'node:crypto';
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

/** Derive a cookie domain suffix from a configured domain or UAC base URL.
 * @param {string|undefined} configured @param {string} baseUrl */
export function resolveCookieDomain(configured, baseUrl) {
  const raw = (configured ?? '').trim().toLowerCase();
  if (raw) return raw.startsWith('.') ? raw : `.${raw}`;
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

/** @param {string} hostKey @param {string} domainSuffix */
export function domainMatches(hostKey, domainSuffix) {
  if (!hostKey || !domainSuffix) return false;
  const host = hostKey.trim().toLowerCase();
  const suffix = domainSuffix.startsWith('.')
    ? domainSuffix.toLowerCase()
    : `.${domainSuffix.toLowerCase()}`;
  const bare = suffix.slice(1);
  return host === bare || host === suffix || host.endsWith(suffix);
}

/** Decrypt a Chromium v10/v11 AES-256-GCM cookie buffer.
 * Returns null for App-Bound (v20) or unsupported formats.
 * @param {Buffer|Uint8Array} encrypted @param {Buffer} masterKey */
export function decryptChromiumValue(encrypted, masterKey) {
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
    const plain = Buffer.concat([
      decipher.update(ciphertext),
      decipher.final(),
    ]);
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
    `$raw = [Convert]::FromBase64String('${b64}')`,
    '$out = [System.Security.Cryptography.ProtectedData]::Unprotect($raw, $null, [System.Security.Cryptography.DataProtectionScope]::CurrentUser)',
    '[Console]::Out.Write([Convert]::ToBase64String($out))',
  ].join('; ');
  try {
    const { stdout } = await execFileAsync(
      'powershell.exe',
      ['-NoProfile', '-NonInteractive', '-Command', script],
      { timeout: 5000, windowsHide: true, maxBuffer: 65536 },
    );
    const decoded = Buffer.from(stdout.trim(), 'base64');
    return decoded.length === 32 ? decoded : null;
  } catch {
    return null;
  }
}

/** Copy a possibly-locked file on Windows using shared read/write/delete mode.
 * @param {string} source @param {string} dest */
async function copySharedFile(source, dest) {
  try {
    copyFileSync(source, dest);
    return true;
  } catch {
    if (process.platform !== 'win32') return false;
  }
  try {
    const srcEsc = source.replaceAll("'", "''");
    const dstEsc = dest.replaceAll("'", "''");
    const script = [
      `$s = [System.IO.File]::Open('${srcEsc}', [System.IO.FileMode]::Open, [System.IO.FileAccess]::Read, [System.IO.FileShare]::ReadWrite -bor [System.IO.FileShare]::Delete)`,
      `try { $d = [System.IO.File]::Create('${dstEsc}'); try { $s.CopyTo($d) } finally { $d.Dispose() } } finally { $s.Dispose() }`,
    ].join('; ');
    await execFileAsync(
      'powershell.exe',
      ['-NoProfile', '-NonInteractive', '-Command', script],
      { timeout: 5000, windowsHide: true },
    );
    return existsSync(dest);
  } catch {
    return false;
  }
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

/** Read matching cookies from one Chromium User Data directory.
 * @param {any} DatabaseSync
 * @param {string} userDataDir
 * @param {string} domainSuffix
 * @param {string[]} names
 * @param {number} nowMs */
async function readChromiumUserData(
  DatabaseSync,
  userDataDir,
  domainSuffix,
  names,
  nowMs,
) {
  const localStatePath = join(userDataDir, 'Local State');
  if (!existsSync(localStatePath)) return new Map();
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
    return new Map();
  }

  // Chromium timestamps are microseconds since 1601-01-01.
  const nowChromeMicros = BigInt(nowMs) * 1000n + 11644473600000000n;
  const cookieFiles = chromiumProfileCookieFiles(userDataDir);
  const found = new Map();
  const placeholders = names.map(() => '?').join(',');
  const sql = `SELECT host_key, name, value, encrypted_value, expires_utc, last_access_utc FROM cookies WHERE name IN (${placeholders}) ORDER BY last_access_utc DESC`;

  for (const file of cookieFiles) {
    const dir = mkdtempSync(join(tmpdir(), 'auriga-sso-'));
    const tempDb = join(dir, 'cookies.db');
    let db;
    try {
      const copied = await copySharedFile(file, tempDb);
      if (!copied) continue;
      db = new DatabaseSync(tempDb, { readOnly: true });
      const rows = db.prepare(sql).all(...names);
      for (const row of rows) {
        if (!domainMatches(String(row.host_key ?? ''), domainSuffix)) continue;
        const exp = BigInt(row.expires_utc ?? 0);
        if (exp !== 0n && exp < nowChromeMicros) continue;
        const name = String(row.name ?? '');
        if (!name || found.has(name)) continue;
        let val = typeof row.value === 'string' && row.value ? row.value : null;
        if (!val && row.encrypted_value && masterKey) {
          val = decryptChromiumValue(row.encrypted_value, masterKey);
        }
        if (val) found.set(name, val);
      }
      if (found.size >= names.length) break;
    } catch {
      /* Ignore unreadable or locked profile databases. */
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
  return found;
}

/** Read matching cookies from Firefox profiles (plaintext SQLite).
 * @param {any} DatabaseSync
 * @param {string} profilesRoot
 * @param {string} domainSuffix
 * @param {string[]} names
 * @param {number} nowMs */
async function readFirefoxProfiles(
  DatabaseSync,
  profilesRoot,
  domainSuffix,
  names,
  nowMs,
) {
  if (!existsSync(profilesRoot)) return new Map();
  let entries = [];
  try {
    entries = readdirSync(profilesRoot, { withFileTypes: true });
  } catch {
    return new Map();
  }
  const nowSec = Math.floor(nowMs / 1000);
  const found = new Map();
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
      const copied = await copySharedFile(cookiePath, tempDb);
      if (!copied) continue;
      db = new DatabaseSync(tempDb, { readOnly: true });
      const rows = db.prepare(sql).all(...names);
      for (const row of rows) {
        if (!domainMatches(String(row.host ?? ''), domainSuffix)) continue;
        const exp = Number(row.expiry ?? 0);
        if (exp > 0 && exp < nowSec) continue;
        const name = String(row.name ?? '');
        const val = typeof row.value === 'string' ? row.value : '';
        if (name && val && !found.has(name)) found.set(name, val);
      }
      if (found.size >= names.length) break;
    } catch {
      /* ignore */
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
  return found;
}

/**
 * Read local browser SSO cookies matching `domainSuffix` and `names`.
 * Platform differences are encapsulated here; callers only receive a Map of
 * cookie name -> value and never see browser paths or decryption details.
 * @param {{domain:string,names:string[],now?:()=>number}} options
 * @returns {Promise<Map<string,string>>}
 */
export async function readLocalBrowserCookies({
  domain,
  names,
  now = Date.now,
}) {
  if (!domain || !Array.isArray(names) || !names.length) return new Map();
  const uniqueNames = [
    ...new Set(names.filter((n) => typeof n === 'string' && n)),
  ];
  if (!uniqueNames.length) return new Map();
  const DatabaseSync = await loadSqlite();
  if (!DatabaseSync) return new Map();

  const nowMs = now();
  const merged = new Map();
  const absorb = (/** @type {Map<string,string>} */ map) => {
    for (const [k, v] of map) if (!merged.has(k) && v) merged.set(k, v);
  };

  if (process.platform === 'win32') {
    const localAppData = process.env.LOCALAPPDATA ?? '';
    const appData = process.env.APPDATA ?? '';
    // Prefer Edge first (frequently uses DPAPI v10 on enterprise Windows), then Chrome, then Firefox.
    const chromiumRoots = [
      localAppData && join(localAppData, 'Microsoft', 'Edge', 'User Data'),
      localAppData && join(localAppData, 'Google', 'Chrome', 'User Data'),
    ].filter(Boolean);
    for (const root of chromiumRoots) {
      absorb(
        await readChromiumUserData(
          DatabaseSync,
          root,
          domain,
          uniqueNames,
          nowMs,
        ),
      );
      if (merged.size >= uniqueNames.length) return merged;
    }
    if (appData) {
      absorb(
        await readFirefoxProfiles(
          DatabaseSync,
          join(appData, 'Mozilla', 'Firefox', 'Profiles'),
          domain,
          uniqueNames,
          nowMs,
        ),
      );
    }
  } else if (process.platform === 'linux') {
    const home = process.env.HOME ?? '';
    if (home) {
      absorb(
        await readFirefoxProfiles(
          DatabaseSync,
          join(home, '.mozilla', 'firefox'),
          domain,
          uniqueNames,
          nowMs,
        ),
      );
    }
  }
  return merged;
}
