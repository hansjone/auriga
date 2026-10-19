import {
  createHash,
  createHmac,
  randomBytes,
  timingSafeEqual,
} from 'node:crypto';
import { argon2id } from '@noble/hashes/argon2.js';

/** Match Spring Argon2PasswordEncoder(16, 32, 1, 65536, 3). */
export const ARGON2_DEFAULTS = Object.freeze({
  t: 3,
  m: 65536,
  p: 1,
  dkLen: 32,
  saltLen: 16,
});

/** Accepted range for stored / configured Argon2id parameters. */
export const ARGON2_LIMITS = Object.freeze({
  mMin: 19_456,
  mMax: 262_144,
  tMin: 2,
  tMax: 6,
  pMin: 1,
  pMax: 4,
  saltMin: 16,
  dkLenMin: 16,
  dkLenMax: 64,
});

/** Placeholder used when username is wrong so timing still includes Argon2. */
export const DUMMY_PASSWORD_HASH =
  '$argon2id$v=19$m=19456,t=2,p=1$' +
  'AAAAAAAAAAAAAAAAAAAAAA$' +
  'AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA';

const BASE32 = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567';
const PHC_RE = /^\$argon2id\$v=19\$m=(\d+),t=(\d+),p=(\d+)\$([^$]+)\$([^$]+)$/;

/** @param {number} [bytes] */
export function randomToken(bytes = 32) {
  return randomBytes(bytes).toString('base64url');
}

/** @param {string} raw */
export function sha256Hex(raw) {
  return createHash('sha256').update(raw, 'utf8').digest('hex');
}

/** @param {string} a @param {string} b */
export function same(a, b) {
  if (typeof a !== 'string' || typeof b !== 'string') return false;
  const left = Buffer.from(a, 'utf8');
  const right = Buffer.from(b, 'utf8');
  if (left.length !== right.length) return false;
  return timingSafeEqual(left, right);
}

/**
 * @param {{ m: number, t: number, p: number, saltLen: number, dkLen?: number }} params
 */
export function assertArgon2Params(params) {
  const { mMin, mMax, tMin, tMax, pMin, pMax, saltMin, dkLenMin, dkLenMax } =
    ARGON2_LIMITS;
  if (
    params.m < mMin ||
    params.m > mMax ||
    params.t < tMin ||
    params.t > tMax ||
    params.p < pMin ||
    params.p > pMax ||
    params.saltLen < saltMin ||
    (params.dkLen != null &&
      (params.dkLen < dkLenMin || params.dkLen > dkLenMax))
  )
    throw Object.assign(new Error('argon2 parameters out of range'), {
      code: 'common.validation_failed',
    });
}

/**
 * Parse and validate an Argon2id PHC string; throws if weak / malformed.
 * @param {string} encoded
 */
export function parseAndValidatePasswordHash(encoded) {
  const match = PHC_RE.exec(encoded);
  if (!match)
    throw Object.assign(new Error('invalid argon2 hash'), {
      code: 'common.validation_failed',
    });
  const m = Number(match[1]);
  const t = Number(match[2]);
  const p = Number(match[3]);
  const salt = Buffer.from(match[4], 'base64url');
  const hash = Buffer.from(match[5], 'base64url');
  assertArgon2Params({ m, t, p, saltLen: salt.length, dkLen: hash.length });
  return { m, t, p, salt, hash };
}

/**
 * @param {string} password
 * @param {{ t?: number, m?: number, p?: number, dkLen?: number, saltLen?: number }} [opts]
 */
export function hashPassword(password, opts = {}) {
  const t = opts.t ?? ARGON2_DEFAULTS.t;
  const m = opts.m ?? ARGON2_DEFAULTS.m;
  const p = opts.p ?? ARGON2_DEFAULTS.p;
  const dkLen = opts.dkLen ?? ARGON2_DEFAULTS.dkLen;
  const saltLen = opts.saltLen ?? ARGON2_DEFAULTS.saltLen;
  assertArgon2Params({ m, t, p, saltLen, dkLen });
  const salt = randomBytes(saltLen);
  const hash = argon2id(new TextEncoder().encode(password), salt, {
    t,
    m,
    p,
    dkLen,
  });
  return (
    `$argon2id$v=19$m=${m},t=${t},p=${p}$` +
    `${Buffer.from(salt).toString('base64url')}$` +
    `${Buffer.from(hash).toString('base64url')}`
  );
}

/** @param {string} password @param {string} encoded */
export function verifyPassword(password, encoded) {
  if (password.length > 256) return false;
  let parsed;
  try {
    parsed = parseAndValidatePasswordHash(encoded);
  } catch {
    return false;
  }
  const actual = Buffer.from(
    argon2id(new TextEncoder().encode(password), parsed.salt, {
      t: parsed.t,
      m: parsed.m,
      p: parsed.p,
      dkLen: parsed.hash.length,
    }),
  );
  if (actual.length !== parsed.hash.length) return false;
  return timingSafeEqual(actual, parsed.hash);
}

/** @returns {string} Base32 TOTP secret */
export function generateTotpSecret() {
  const raw = randomBytes(20);
  let buffer = 0;
  let bits = 0;
  let out = '';
  for (const b of raw) {
    buffer = (buffer << 8) | b;
    bits += 8;
    while (bits >= 5) {
      bits -= 5;
      out += BASE32[(buffer >> bits) & 31];
    }
  }
  return out;
}

/** @param {string} secret */
export function decodeBase32(secret) {
  const out = [];
  let buffer = 0;
  let bits = 0;
  for (const c of secret.toUpperCase()) {
    const v = BASE32.indexOf(c);
    if (v < 0) throw new Error('Invalid Base32');
    buffer = (buffer << 5) | v;
    bits += 5;
    if (bits >= 8) {
      bits -= 8;
      out.push((buffer >> bits) & 255);
    }
  }
  return Buffer.from(out);
}

/** @param {string} secret @param {number} counter */
export function totpCode(secret, counter) {
  const mac = createHmac('sha1', decodeBase32(secret));
  const buf = Buffer.alloc(8);
  buf.writeBigUInt64BE(BigInt(counter));
  const hash = mac.update(buf).digest();
  const offset = hash[hash.length - 1] & 15;
  const binary = hash.readUInt32BE(offset) & 0x7fffffff;
  return String(binary % 1_000_000).padStart(6, '0');
}

/**
 * @param {string} secret
 * @param {string} supplied
 * @param {Date|number} now
 * @param {number} lastCounter
 * @returns {number} accepted counter, or -1
 */
export function verifyTotp(secret, supplied, now, lastCounter) {
  if (!/^[0-9]{6}$/.test(supplied)) return -1;
  const epoch =
    typeof now === 'number' ? now : Math.floor(now.getTime() / 1000);
  const current = Math.floor(epoch / 30);
  for (let c = current - 1; c <= current + 1; c++) {
    if (c > lastCounter && same(totpCode(secret, c), supplied)) return c;
  }
  return -1;
}

/** @param {string} verifier */
export function pkceChallenge(verifier) {
  return createHash('sha256').update(verifier, 'ascii').digest('base64url');
}
