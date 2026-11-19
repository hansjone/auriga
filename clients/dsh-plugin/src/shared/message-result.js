import validateResult from '../generated/network/validate-result.js';
import { parseAddress } from '../network/host/address.js';
import { fail } from './protocol.js';

/** SUBMIT's existing HTTP result is unsigned. Do not invent a server signature.
 * Its schema and addresses are bound to this authenticated, unreplayed call.
 * @param {any} value @param {{realm:string,node:string,local:string}} source @param {{realm:string,node:string,local:string}} target */
export function assertSharedSendResult(value, source, target) {
  if (!validateResult(value)) fail('common.validation_failed');
  const from = parseAddress(value.from, {
    role: 'source',
    realm: source.realm,
  });
  const to = parseAddress(value.to, { realm: target.realm });
  if (
    from.type !== 'session' ||
    from.node !== source.node ||
    from.local !== source.local ||
    to.node !== target.node ||
    to.local !== target.local
  )
    fail('shared.binding_mismatch');
  return value;
}
