import { Buffer } from 'node:buffer';
import { HostSeamError } from '../host/errors.js';
import { encoding } from '../../generated/network/contract.js';

/**
 * Bound work before schema validation and detach model data before any await.
 * Reject accessors, prototypes and non-JSON values instead of silently dropping
 * reserved fields through JSON.stringify/toJSON.
 * @param {unknown} input
 * @returns {unknown}
 */
export function snapshotInput(input) {
  let values = 0;
  /** @param {unknown} value @param {number} depth @returns {unknown} */
  function visit(value, depth) {
    if (++values > encoding.maxTotalValues || depth > encoding.maxJsonDepth)
      throw new HostSeamError('network.quota_exceeded');
    if (typeof value === 'string') {
      if (Buffer.byteLength(value) > encoding.maxStringBytes)
        throw new HostSeamError('network.quota_exceeded');
      return value;
    }
    if (value === null || typeof value === 'boolean') return value;
    if (typeof value === 'number' && Number.isFinite(value)) return value;
    if (typeof value !== 'object')
      throw new HostSeamError('network.protocol_error');
    if (Array.isArray(value)) {
      if (value.length > encoding.maxArrayItems)
        throw new HostSeamError('network.quota_exceeded');
      // The send schema has no array fields; reject without reading accessors.
      throw new HostSeamError('network.protocol_error');
    }
    if (![Object.prototype, null].includes(Object.getPrototypeOf(value)))
      throw new HostSeamError('network.protocol_error');
    const keys = Reflect.ownKeys(value);
    if (keys.length > encoding.maxObjectMembers)
      throw new HostSeamError('network.quota_exceeded');
    return Object.fromEntries(
      keys.map((key) => {
        const descriptor = Object.getOwnPropertyDescriptor(value, key);
        if (
          typeof key !== 'string' ||
          !descriptor?.enumerable ||
          !('value' in descriptor)
        )
          throw new HostSeamError('network.protocol_error');
        if (Buffer.byteLength(key) > encoding.maxStringBytes)
          throw new HostSeamError('network.quota_exceeded');
        return [key, visit(descriptor.value, depth + 1)];
      }),
    );
  }
  const result = visit(input, 1);
  if (Buffer.byteLength(JSON.stringify(result)) > encoding.maxPayloadBytes)
    throw new HostSeamError('network.quota_exceeded');
  return result;
}
