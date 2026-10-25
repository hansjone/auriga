/* global structuredClone */
import { createPublicKey, verify } from 'node:crypto';
import { Buffer } from 'node:buffer';
import { definitions } from '../../generated/network/keyset-schema.js';
import { canonicalize, parseStrictJson } from './jcs.js';
import { SessionError } from '../session/errors.js';

/** @param {string} detail @returns {never} */
export function unavailable(detail) {
  throw new SessionError('network.key_unavailable', detail);
}
/** @param {any} value */
export function positiveVersion(value) {
  return (
    typeof value === 'string' &&
    /^[1-9][0-9]{0,19}$/.test(value) &&
    BigInt(value) <= 18446744073709551615n
  );
}
/** @param {any} value */
export function timeValue(value) {
  if (
    typeof value !== 'string' ||
    !/^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ$/.test(value)
  )
    return NaN;
  const time = Date.parse(value);
  return Number.isFinite(time) &&
    new Date(time).toISOString() === value.replace('Z', '.000Z')
    ? time
    : NaN;
}
const supported = new Set([
  '$ref',
  'description',
  'title',
  'type',
  'const',
  'enum',
  'pattern',
  'format',
  'required',
  'additionalProperties',
  'properties',
  'allOf',
  'not',
  'items',
  'maxItems',
  'maxLength',
  'minLength',
]);
/** Check even optional/absent branches: regenerating a stronger contract must
 * fail closed before any publication can be accepted. @param {any} schema */
function checkSchema(schema) {
  if (
    !schema ||
    Object.keys(schema).some((keyword) => !supported.has(keyword)) ||
    (schema.type && !['object', 'array', 'string'].includes(schema.type)) ||
    (schema.format && schema.format !== 'date-time') ||
    (schema.additionalProperties !== undefined &&
      typeof schema.additionalProperties !== 'boolean')
  )
    unavailable('unsupported keyset schema keyword');
  for (const child of Object.values(schema.properties ?? {}))
    checkSchema(child);
  for (const child of schema.allOf ?? []) checkSchema(child);
  if (schema.not) checkSchema(schema.not);
  if (schema.items) checkSchema(schema.items);
}
for (const schema of Object.values(definitions)) checkSchema(schema);
/** Evaluates the finite JSON Schema subset used by the generated keyset contract.
 * @param {any} value @param {any} schema @returns {boolean} */
export function matches(value, schema) {
  checkSchema(schema);
  if (schema.$ref)
    return matches(
      value,
      /** @type {any} */ (definitions)[schema.$ref.split('/').at(-1)],
    );
  if (
    schema.allOf &&
    !schema.allOf.every(/** @param {any} s */ (s) => matches(value, s))
  )
    return false;
  if (schema.not && matches(value, schema.not)) return false;
  if (Object.hasOwn(schema, 'const') && value !== schema.const) return false;
  if (schema.enum && !schema.enum.includes(value)) return false;
  if (schema.type === 'object') {
    if (!value || typeof value !== 'object' || Array.isArray(value))
      return false;
    if (
      schema.required?.some(
        /** @param {string} k */ (k) => !Object.hasOwn(value, k),
      )
    )
      return false;
    return Object.keys(value).every((k) =>
      schema.properties && Object.hasOwn(schema.properties, k)
        ? matches(value[k], schema.properties[k])
        : schema.additionalProperties !== false,
    );
  }
  if (schema.type === 'array')
    return (
      Array.isArray(value) &&
      (!schema.maxItems || value.length <= schema.maxItems) &&
      value.every((v) => matches(v, schema.items))
    );
  if (schema.type === 'string')
    return (
      typeof value === 'string' &&
      (!schema.pattern || new RegExp(schema.pattern).test(value)) &&
      (!schema.maxLength || value.length <= schema.maxLength) &&
      (!schema.minLength || value.length >= schema.minLength) &&
      (schema.format !== 'date-time' || Number.isFinite(timeValue(value)))
    );
  return true;
}
/** @param {any} key */
export function publicKey(key) {
  try {
    if (typeof key !== 'string' || !/^[A-Za-z0-9_-]{59}$/.test(key))
      return unavailable('invalid public key');
    const imported = createPublicKey({
      key: Buffer.from(key, 'base64url'),
      format: 'der',
      type: 'spki',
    });
    if (
      imported.asymmetricKeyType !== 'ed25519' ||
      imported.export({ format: 'der', type: 'spki' }).toString('base64url') !==
        key
    )
      return unavailable('invalid public key');
    return imported;
  } catch {
    return unavailable('invalid public key');
  }
}
/** @param {any} value */
export function validatePublicKey(value) {
  if (
    !matches(value, definitions.PublicKey) ||
    !(timeValue(value.not_after) > timeValue(value.not_before))
  )
    unavailable('invalid key');
  return publicKey(value.public_key_spki);
}
/** Only installation-provisioned roots may authenticate a publication.
 * @param {any} input @param {Map<string,import('node:crypto').KeyObject>} roots
 * @returns {{publication:import('./keyset.js').SignedPublication, keys:Map<string,import('node:crypto').KeyObject>}}
 */
export function validatePublication(input, roots) {
  try {
    const publication =
      typeof input === 'string'
        ? parseStrictJson(Buffer.from(input))
        : input instanceof Uint8Array
          ? parseStrictJson(input)
          : structuredClone(input);
    canonicalize(publication);
    if (!matches(publication, definitions.SignedKeyset))
      return unavailable('invalid signed keyset');
    const root = roots.get(publication.root_auth.kid);
    const signature = Buffer.from(publication.root_auth.signature, 'base64url');
    if (
      !root ||
      signature.length !== 64 ||
      signature.toString('base64url') !== publication.root_auth.signature ||
      !verify(
        null,
        Buffer.from(
          'Auriga.NetworkKeyset.v1\0' + canonicalize(publication.keyset),
        ),
        root,
        signature,
      )
    )
      return unavailable('invalid root signature');
    const c = publication.keyset;
    if (
      !positiveVersion(c.realm) ||
      !positiveVersion(c.keyset_version) ||
      !(timeValue(c.valid_until) > timeValue(c.issued_at)) ||
      timeValue(c.valid_until) > timeValue(c.issued_at) + 86400000
    )
      return unavailable('invalid keyset lifetime');
    const keys = new Map();
    const material = new Set();
    for (const k of c.keys) {
      if (
        keys.has(k.kid) ||
        material.has(k.public_key_spki) ||
        [...roots.values()].some(
          (r) =>
            r.export({ format: 'der', type: 'spki' }).toString('base64url') ===
            k.public_key_spki,
        )
      )
        return unavailable('reused key');
      keys.set(k.kid, validatePublicKey(k));
      material.add(k.public_key_spki);
    }
    return { publication, keys };
  } catch {
    return unavailable('invalid keyset publication');
  }
}
/** @typedef {import('../../generated/network/keyset-types.d.ts').SignedKeyset} SignedPublication */
