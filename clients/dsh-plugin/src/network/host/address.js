import { CODES, HostSeamError } from './errors.js';

const UINT64_MAX = (1n << 64n) - 1n;
const DECIMAL = /^(0|[1-9][0-9]{0,19})$/;
const TYPES = new Set(['system', 'session', 'subagent', 'group']);

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
 * @returns {Address}
 */
export function parseAddress(value) {
  const a = /** @type {Record<string, unknown>|null} */ (
    value && typeof value === 'object' ? value : null
  );
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
    throw new HostSeamError(CODES.protocolError, 'invalid address');
  const type = /** @type {Address['type']} */ (a.type);
  const node = /** @type {string} */ (a.node);
  const local = /** @type {string} */ (a.local);
  if (type === 'group' ? node !== '0' || local === '0' : node === '0')
    throw new HostSeamError(CODES.protocolError, 'invalid address');
  if ((type === 'system') !== (local === '0') && type !== 'group')
    throw new HostSeamError(CODES.protocolError, 'invalid address');
  return {
    version: 1,
    realm: /** @type {string} */ (a.realm),
    type,
    node,
    local,
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
 * @param {Address} address */
export function formatAddress(address) {
  const hex = (/** @type {string} */ v) => BigInt(v).toString(16);
  return `aur1:${hex(address.realm)}.${hex(address.node)}.${hex(address.local)}`;
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
