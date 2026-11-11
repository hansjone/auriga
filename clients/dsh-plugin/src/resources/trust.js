import { createHash, createPublicKey, verify } from 'node:crypto';
import { open, constants, realpath } from 'node:fs/promises';
import { isAbsolute, resolve } from 'node:path';
import { canonicalize, parseStrictJson } from './json.js';

/** @param {string} [code] @returns {never} */
export function refuse(code = 'resource.signature_invalid') {
  throw Object.assign(new Error(code), { code });
}
/** @param {any} v @param {string[]} keys */
export function exact(v, keys) {
  return (
    !!v &&
    typeof v === 'object' &&
    !Array.isArray(v) &&
    Object.keys(v).sort().join() === [...keys].sort().join()
  );
}
/** @param {any} v */
export const uuid = (v) =>
  typeof v === 'string' &&
  /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(v);
/** @param {any} v */
export const digest = (v) =>
  typeof v === 'string' && /^sha256:[0-9a-f]{64}$/.test(v);
/** @param {any} v */
export const counter = (v) => Number.isSafeInteger(v) && v > 0;
/** Exact UTC nanosecond instant, preserving the original signed spelling.
 * @param {any} text */
export function instant(text) {
  const m =
    typeof text === 'string' &&
    /^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2})(?:\.(\d{1,9}))?Z$/.exec(text);
  if (!m) return refuse();
  const ms = Date.parse(m[1] + 'Z');
  if (!Number.isFinite(ms) || new Date(ms).toISOString().slice(0, 19) !== m[1])
    return refuse();
  return BigInt(ms) * 1000000n + BigInt((m[2] ?? '').padEnd(9, '0'));
}
/** @param {any} value @param {ReadonlyMap<string, import('node:crypto').KeyObject>} roots */
export function signed(value, roots) {
  const s = value?.signature;
  if (
    !exact(s, ['alg', 'kid', 'value']) ||
    s.alg !== 'Ed25519' ||
    typeof s.kid !== 'string' ||
    !/^[A-Za-z0-9._-]{1,128}$/.test(s.kid) ||
    typeof s.value !== 'string' ||
    !/^[A-Za-z0-9_-]{85}[AQgw]$/.test(s.value)
  )
    refuse();
  const key = roots.get(s.kid),
    bytes = Buffer.from(s.value, 'base64url');
  const body = { ...value };
  delete body.signature;
  if (
    !key ||
    key.type !== 'public' ||
    key.asymmetricKeyType !== 'ed25519' ||
    bytes.length !== 64 ||
    bytes.toString('base64url') !== s.value ||
    !verify(null, Buffer.from(canonicalize(body)), key, bytes)
  )
    refuse();
  return (
    'sha256:' + createHash('sha256').update(canonicalize(body)).digest('hex')
  );
}
/** Independent protected deployment roots, never read from an archive, ticket or
 * panel. Deployment owns file ACLs; POSIX also rejects group/world-writable files.
 * Shape: {schemaVersion:1,releaseKeys:[{kid,publicKeySpki}],revocationKeys:[...]}.
 * @param {string|undefined} file */
async function loadResourceRoots(file) {
  if (
    !file ||
    !isAbsolute(file) ||
    resolve(await realpath(file)) !== resolve(file)
  )
    return refuse('resource.storage_unavailable');
  const handle = await open(
    file,
    constants.O_RDONLY | (constants.O_NOFOLLOW ?? 0),
  );
  try {
    const stat = await handle.stat();
    if (
      !stat.isFile() ||
      stat.size > 65536 ||
      (process.platform !== 'win32' && stat.mode & 0o022)
    )
      refuse('resource.storage_unavailable');
    const value = parseStrictJson(await handle.readFile(), true);
    if (
      !exact(value, ['schemaVersion', 'releaseKeys', 'revocationKeys']) ||
      value.schemaVersion !== 1
    )
      refuse();
    const used = new Set();
    /** @param {any} entries */
    function keys(entries) {
      if (!Array.isArray(entries) || !entries.length || entries.length > 32)
        return refuse();
      const map = new Map();
      for (const row of entries) {
        if (
          !exact(row, ['kid', 'publicKeySpki']) ||
          typeof row.kid !== 'string' ||
          !/^[A-Za-z0-9._-]{1,128}$/.test(row.kid) ||
          typeof row.publicKeySpki !== 'string' ||
          used.has(row.kid) ||
          used.has(row.publicKeySpki)
        )
          refuse();
        const bytes = Buffer.from(row.publicKeySpki, 'base64url');
        if (
          bytes.toString('base64url') !== row.publicKeySpki ||
          bytes.length > 128
        )
          refuse();
        const key = createPublicKey({
          key: bytes,
          format: 'der',
          type: 'spki',
        });
        if (key.asymmetricKeyType !== 'ed25519') refuse();
        used.add(row.kid);
        used.add(row.publicKeySpki);
        map.set(row.kid, key);
      }
      return map;
    }
    return {
      releaseKeys: keys(value.releaseKeys),
      revocationKeys: keys(value.revocationKeys),
    };
  } finally {
    await handle.close();
  }
}
/** @param {string|undefined} file */
export async function readResourceRoots(file) {
  try {
    return await loadResourceRoots(file);
  } catch (error) {
    if (
      ['resource.signature_invalid', 'resource.storage_unavailable'].includes(
        /** @type {any} */ (error).code,
      )
    )
      throw error;
    return refuse('resource.storage_unavailable');
  }
}
/** @param {any} list @param {string} tenantId @param {ReadonlyMap<string, import('node:crypto').KeyObject>} keys
 * @param {number} now @param {{revision:number,digest:string}|undefined} previous */
export function verifyRevocations(list, tenantId, keys, now, previous) {
  if (
    !Number.isSafeInteger(now) ||
    (previous && (!counter(previous.revision) || !digest(previous.digest)))
  )
    refuse();
  if (
    !exact(list, [
      'schemaVersion',
      'tenantId',
      'revision',
      'issuedAt',
      'expiresAt',
      'revokedKids',
      'revokedArtifacts',
      'signature',
    ]) ||
    list.schemaVersion !== 1 ||
    !uuid(list.tenantId) ||
    list.tenantId !== tenantId ||
    !counter(list.revision) ||
    !Array.isArray(list.revokedKids) ||
    list.revokedKids.length > 2048 ||
    !Array.isArray(list.revokedArtifacts) ||
    list.revokedArtifacts.length > 8192
  )
    refuse();
  const kids = new Set(),
    artifacts = new Set();
  for (const kid of list.revokedKids) {
    if (
      typeof kid !== 'string' ||
      !kid.length ||
      kid.length > 128 ||
      kids.has(kid)
    )
      refuse();
    kids.add(kid);
  }
  for (const a of list.revokedArtifacts) {
    if (
      !exact(a, ['packageId', 'versionId', 'digest']) ||
      !uuid(a.packageId) ||
      !uuid(a.versionId) ||
      !digest(a.digest)
    )
      refuse();
    const key = canonicalize(a);
    if (artifacts.has(key)) refuse();
    artifacts.add(key);
  }
  const issued = instant(list.issuedAt),
    expiry = instant(list.expiresAt),
    clock = BigInt(now) * 1000000n;
  if (
    issued > clock ||
    expiry <= clock ||
    expiry <= issued ||
    expiry - issued > 86400000000000n
  )
    refuse();
  const hash = signed(list, keys);
  if (
    previous &&
    (list.revision < previous.revision ||
      (list.revision === previous.revision && hash !== previous.digest))
  )
    refuse();
  return { revision: list.revision, digest: hash, list };
}
/** @param {any} list @param {any} envelope */
export function isRevoked(list, envelope) {
  return (
    list.revokedKids.includes(envelope.signature.kid) ||
    list.revokedArtifacts.some(
      (/** @type {any} */ a) =>
        a.packageId === envelope.packageId &&
        a.versionId === envelope.versionId &&
        a.digest === envelope.statement.digest,
    )
  );
}
