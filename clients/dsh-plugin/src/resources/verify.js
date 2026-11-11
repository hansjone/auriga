import { createHash, verify } from 'node:crypto';
import { canonicalize, parseStrictJson } from '../network/trust/jcs.js';
import { TextDecoder } from 'node:util';
import { integerToken } from './json.js';

/** @typedef {import('../generated/resource-signatures.js').ResourceEnvelope} Envelope */
/** @typedef {{releaseSequence: number, versionId: string, digest: string}} HighWater */
/** @param {string} [code] @returns {never} */
function invalid(code = 'resource.signature_invalid') {
  throw Object.assign(new Error(code), { code });
}
/** @param {any} value @param {string[]} keys */
function shape(value, keys) {
  return (
    value &&
    typeof value === 'object' &&
    !Array.isArray(value) &&
    Object.keys(value).length === keys.length &&
    keys.every((key) => Object.hasOwn(value, key))
  );
}
/** Preserve strings/duplicate keys while accepting all JSON safe-integer spellings.
 * The shared strict parser subsequently validates the complete JSON grammar.
 * @param {Uint8Array} wire */
function parseEnvelope(wire) {
  if (
    !(wire instanceof Uint8Array) ||
    !wire.byteLength ||
    wire.byteLength > 65536
  )
    invalid();
  const source = new TextDecoder('utf-8', {
    fatal: true,
    ignoreBOM: true,
  }).decode(wire);
  let normalized = '',
    quoted = false,
    escaped = false;
  for (let i = 0; i < source.length; i++) {
    const c = source[i];
    if (quoted) {
      normalized += c;
      if (escaped) escaped = false;
      else if (c === '\\') escaped = true;
      else if (c === '"') quoted = false;
    } else if (c === '"') {
      quoted = true;
      normalized += c;
    } else if (c === '-' || /[0-9]/.test(c)) {
      const token =
        /^-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?/.exec(
          source.slice(i),
        );
      if (!token) invalid();
      normalized += JSON.stringify(integerToken(token[0]));
      i += token[0].length - 1;
    } else normalized += c;
  }
  return parseStrictJson(Buffer.from(normalized));
}
/** Exact UTC nanoseconds; Date.parse alone accepts impossible dates and loses precision.
 * @param {string} value @returns {bigint} */
function instant(value) {
  const match =
    typeof value === 'string' &&
    /^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2})(?:\.(\d{1,9}))?Z$/.exec(value);
  if (!match) return invalid();
  const ms = Date.parse(match[1] + 'Z');
  if (
    !Number.isFinite(ms) ||
    new Date(ms).toISOString().slice(0, 19) !== match[1]
  )
    return invalid();
  return BigInt(ms) * 1000000n + BigInt((match[2] ?? '').padEnd(9, '0'));
}
const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
/** @param {unknown} n */
const counter = (n) =>
  typeof n === 'number' && Number.isSafeInteger(n) && n > 0;
/** @param {unknown} v @param {number} max */
const text = (v, max) =>
  typeof v === 'string' && v.length > 0 && Array.from(v).length <= max;

/** Cryptographic admission only, never installs or executes an archive.
 * The caller supplies protected release roots, the locked workspace reference and a
 * durably loaded high-water row. Persist the returned row atomically BEFORE activation;
 * this function cannot replace ACL/revocation checks or host archive validation.
 * @param {Uint8Array} wire Original envelope JSON (not a parsed/reserialized object).
 * @param {Uint8Array} archive
 * @param {{expected: {tenantId: string, packageId: string, versionId: string, releaseSequence: number, statement: Envelope['statement'], sizeBytes: number}, releaseKeys: ReadonlyMap<string, import('node:crypto').KeyObject>, highWater?: HighWater, now: number}} options
 * @returns {Envelope}
 */
export function verifyResourceArchive(
  wire,
  archive,
  { expected, releaseKeys, highWater, now },
) {
  try {
    const e = parseEnvelope(wire);
    if (
      !shape(e, [
        'schemaVersion',
        'tenantId',
        'packageId',
        'versionId',
        'releaseSequence',
        'notAfter',
        'statement',
        'signature',
      ]) ||
      e.schemaVersion !== 1 ||
      ![e.tenantId, e.packageId, e.versionId].every(
        (id) => typeof id === 'string' && uuid.test(id),
      ) ||
      !counter(e.releaseSequence)
    )
      invalid();
    const s = e.statement;
    if (
      !shape(s, [
        'purpose',
        'name',
        'version',
        'os',
        'arch',
        'digest',
        'issuedAt',
      ]) ||
      s.purpose !== 'plugin' ||
      !text(s.name, 214) ||
      !/^(@[a-z0-9._-]+\/)?[a-z0-9._-]+$/.test(s.name) ||
      !text(s.version, 128) ||
      !['linux', 'windows', 'any'].includes(s.os) ||
      !['amd64', 'arm64', 'any'].includes(s.arch) ||
      !/^sha256:[0-9a-f]{64}$/.test(s.digest)
    )
      invalid();
    const sig = e.signature;
    if (
      !shape(sig, ['alg', 'kid', 'value']) ||
      sig.alg !== 'Ed25519' ||
      !text(sig.kid, 128) ||
      !/^[A-Za-z0-9._-]+$/.test(sig.kid) ||
      typeof sig.value !== 'string' ||
      !/^[A-Za-z0-9_-]{85}[AQgw]$/.test(sig.value)
    )
      invalid();
    const key = releaseKeys.get(sig.kid),
      signature = Buffer.from(sig.value, 'base64url');
    const unsigned = { ...e };
    delete unsigned.signature;
    if (
      !key ||
      key.type !== 'public' ||
      key.asymmetricKeyType !== 'ed25519' ||
      signature.length !== 64 ||
      signature.toString('base64url') !== sig.value ||
      !verify(null, Buffer.from(canonicalize(unsigned)), key, signature)
    )
      invalid();
    const issued = instant(s.issuedAt),
      expires = instant(e.notAfter);
    if (!Number.isSafeInteger(now)) invalid();
    const clock = BigInt(now) * 1000000n;
    if (
      issued > clock ||
      clock >= expires ||
      expires <= issued ||
      expires - issued > 86400000000000n
    )
      invalid();
    if (
      e.tenantId !== expected.tenantId ||
      e.packageId !== expected.packageId ||
      e.versionId !== expected.versionId ||
      e.releaseSequence !== expected.releaseSequence ||
      instant(s.issuedAt) !== instant(expected.statement.issuedAt) ||
      canonicalize(s) !==
        canonicalize({ ...expected.statement, issuedAt: s.issuedAt })
    )
      invalid();
    if (
      highWater &&
      (!counter(highWater.releaseSequence) ||
        !uuid.test(highWater.versionId) ||
        !/^sha256:[0-9a-f]{64}$/.test(highWater.digest) ||
        e.releaseSequence < highWater.releaseSequence ||
        (e.releaseSequence === highWater.releaseSequence &&
          (e.versionId !== highWater.versionId ||
            s.digest !== highWater.digest)))
    )
      invalid();
    if (
      !(archive instanceof Uint8Array) ||
      !counter(expected.sizeBytes) ||
      expected.sizeBytes > 67108864 ||
      archive.byteLength !== expected.sizeBytes ||
      'sha256:' + createHash('sha256').update(archive).digest('hex') !==
        s.digest
    )
      invalid('resource.digest_mismatch');
    return e;
  } catch (error) {
    if (
      error instanceof Error &&
      'code' in error &&
      error.code === 'resource.digest_mismatch'
    )
      throw error;
    return invalid();
  }
}
