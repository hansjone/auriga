import { CODES, HostSeamError } from './errors.js';

const UINT64_MAX = (1n << 64n) - 1n;
const DECIMAL = /^(0|[1-9][0-9]{0,19})$/;
const TYPES = new Set(['system', 'session', 'subagent', 'group']);
const HEX = /^(0|[1-9a-f][0-9a-f]{0,15})$/;

/**
 * @typedef {{ version: 1, realm: string, type: 'system'|'session'|'subagent'|'group', node: string, local: string }} Address
 */

/** @param {unknown} value */
export function isUint64String(value) {
  return (
    typeof value === 'string' &&
    DECIMAL.test(value) &&
    BigInt(value) <= UINT64_MAX
  );
}

/**
 * Validate a full JSON address (decimal uint64 strings, never JS numbers).
 * @param {unknown} value
 * @param {{ realm?: string|null, role?: 'source'|'dest'|'node' }} [options]
 * @returns {Address}
 */
export function parseAddress(value, { realm, role = 'dest' } = {}) {
  const a = /** @type {Record<string, unknown>|null} */ (
    value && typeof value === 'object' ? value : null
  );
  if (Number.isInteger(a?.version) && Number(a?.version) > 1)
    throw new HostSeamError(CODES.unsupportedAddressVersion);
  if (
    !a ||
    a.version !== 1 ||
    !isUint64String(a.realm) ||
    !isUint64String(a.node) ||
    !isUint64String(a.local) ||
    typeof a.type !== 'string' ||
    !TYPES.has(a.type) ||
    a.realm === '0'
  )
    throw new HostSeamError(CODES.invalidAddress);
  const type = /** @type {Address['type']} */ (a.type);
  const node = /** @type {string} */ (a.node);
  const local = /** @type {string} */ (a.local);
  if (type === 'group' ? node !== '0' || local === '0' : node === '0')
    throw new HostSeamError(CODES.invalidAddress);
  if ((type === 'system') !== (local === '0') && type !== 'group')
    throw new HostSeamError(CODES.invalidAddress);
  if (realm != null && a.realm !== realm)
    throw new HostSeamError(CODES.realmNotSupported);
  if (type === 'group' && role === 'source')
    throw new HostSeamError(CODES.senderMismatch);
  if (type === 'group' && role === 'node')
    throw new HostSeamError(CODES.targetMismatch);
  return {
    version: 1,
    realm: /** @type {string} */ (a.realm),
    type,
    node,
    local,
  };
}

/**
 * Text cannot distinguish session from subagent; resolve that from the
 * authoritative node registration before constructing a wire address.
 * @param {unknown} text
 * @param {string|null} [realm] authenticated realm, never inferred from text
 */
export function parseAddressText(text, realm = null) {
  if (typeof text !== 'string') throw new HostSeamError(CODES.invalidAddress);
  if (/^aur[0-9]+:/.test(text) && !text.startsWith('aur1:'))
    throw new HostSeamError(CODES.unsupportedAddressVersion);
  if (!text.startsWith('aur1:')) throw new HostSeamError(CODES.invalidAddress);
  const parts = text.slice(5).split('.');
  if (![2, 3].includes(parts.length) || !parts.every((v) => HEX.test(v)))
    throw new HostSeamError(CODES.invalidAddress);
  const values = parts.map((v) => BigInt(`0x${v}`).toString());
  if (values.length === 2) {
    if (!isUint64String(realm) || realm === '0')
      throw new HostSeamError(CODES.invalidAddress);
    values.unshift(/** @type {string} */ (realm));
  }
  const [parsedRealm, node, local] = values;
  if (parsedRealm === '0') throw new HostSeamError(CODES.invalidAddress);
  if (realm !== null && parsedRealm !== realm)
    throw new HostSeamError(CODES.realmNotSupported);
  if (node === '0' && local === '0')
    throw new HostSeamError(CODES.invalidAddress);
  return {
    realm: parsedRealm,
    node,
    local,
    type: node === '0' ? 'group' : local === '0' ? 'system' : null,
  };
}

/** Stable equality key for a full address: `realm.node.local` in decimal.
 * @param {Address} address */
export function addressKey(address) {
  return `${address.realm}.${address.node}.${address.local}`;
}

/** `realm.node` key used for node-scoped relations.
 * @param {{ realm: string, node: string }} address */
export function nodeKey(address) {
  return `${address.realm}.${address.node}`;
}

/** Display text `aur1:realm.node.local` with lowercase hex, no leading zeros.
 * @param {{ realm: string, node: string, local: string }} address
 * @param {string|null} [realm] authenticated realm for optional short display
 */
export function formatAddress(address, realm = null) {
  if (
    ![address.realm, address.node, address.local].every(isUint64String) ||
    address.realm === '0' ||
    (address.node === '0' && address.local === '0')
  )
    throw new HostSeamError(CODES.invalidAddress);
  const hex = (/** @type {string} */ v) => BigInt(v).toString(16);
  const prefix = realm === address.realm ? '' : `${hex(address.realm)}.`;
  return `aur1:${prefix}${hex(address.node)}.${hex(address.local)}`;
}

/**
 * @param {{ realm: string, node: string }} node
 * @param {string} local
 * @param {Address['type']} type
 * @returns {Address}
 */
export function makeAddress(node, local, type) {
  return parseAddress({
    version: 1,
    realm: node.realm,
    node: node.node,
    local,
    type,
  });
}
