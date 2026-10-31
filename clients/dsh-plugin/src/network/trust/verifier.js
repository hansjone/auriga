import { platformStarted } from '../clock.js';
import { createHash, verify } from 'node:crypto';
import { Buffer } from 'node:buffer';
import { SessionError } from '../session/errors.js';
import { canonicalize } from './jcs.js';
import { timeValue } from './store.js';

/** @typedef {{realm: string, node: string, nodeIncarnation: string, epoch: string, channelNonce: string, requestId: string|null}} VerifyContext */
/** @param {string} detail @returns {never} */
function invalid(detail) {
  throw new SessionError('network.signature_invalid', detail);
}
/** @param {any} value */
export function digest(value) {
  return (
    'sha256:' + createHash('sha256').update(canonicalize(value)).digest('hex')
  );
}
/** @param {any} value */
export function signingInput(value) {
  const auth = { ...value.server_auth };
  delete auth.signature;
  return Buffer.from(
    'Auriga.ServerEnvelope.v1\0' +
      canonicalize({ ...value, server_auth: auth }),
    'utf8',
  );
}

/** Verify the original complete object before projecting any fields.
 * @param {any} value
 * @param {{trust: Awaited<ReturnType<import('./store.js').openTrustStore>>, context: VerifyContext, now?: () => number}} options
 * @returns {true}
 */
export function verifyEnvelope(value, { trust, context: c, now = Date.now }) {
  if (!/^1\.(?:0|[1-9][0-9]{0,3})$/.test(value?.app_version ?? ''))
    throw new SessionError(
      'network.unsupported_app_version',
      'unsupported app version',
    );
  const a = value?.server_auth;
  if (!a)
    throw new SessionError(
      'network.signature_required',
      'server signature missing',
    );
  if (
    a.version !== 1 ||
    a.alg !== 'Ed25519' ||
    !/^[A-Za-z0-9_-]{85}[AQgw]$/.test(a.signature ?? '')
  )
    invalid('signature encoding or algorithm');
  const purpose =
    value.type === 'delivery' || value.type === 'contact_prompt'
      ? 'business_delivery'
      : value.type === 'state_update'
        ? 'management_state'
        : 'protocol_response';
  const key = trust.key(a.kid, a.purpose, a.keyset_version);
  const signature = Buffer.from(a.signature, 'base64url');
  if (
    signature.length !== 64 ||
    signature.toString('base64url') !== a.signature ||
    !key.publicKey ||
    !verify(null, signingInput(value), key.publicKey, signature)
  )
    invalid('bad signature');
  const audience = a.audience;
  if (
    a.purpose !== purpose ||
    a.issuer_realm !== key.realm ||
    a.issuer_realm !== c.realm ||
    !audience ||
    audience.realm !== c.realm ||
    audience.node !== c.node ||
    audience.incarnation !== c.nodeIncarnation ||
    audience.epoch !== c.epoch ||
    a.channel_nonce !== c.channelNonce ||
    a.request_id !== c.requestId ||
    (value.epoch !== undefined && value.epoch !== c.epoch) ||
    (value.request_id !== undefined && value.request_id !== c.requestId) ||
    (value.type === 'head' && a.request_id !== null)
  )
    invalid('context mismatch');
  const issued = timeValue(a.issued_at),
    expiry = timeValue(a.not_after),
    clock = now();
  if (
    ![issued, expiry, clock].every(Number.isFinite) ||
    expiry <= issued ||
    expiry - issued > 300000 ||
    !platformStarted(issued, clock) ||
    clock >= expiry ||
    issued < key.notBefore ||
    expiry > key.notAfter
  )
    invalid('signature time');
  if (value.type === 'delivery') {
    const m = value.message,
      source = value.source_context;
    if (Buffer.byteLength(canonicalize(m?.body)) > 16384)
      throw new SessionError(
        'network.protocol_error',
        'message body exceeds 16KiB',
      );
    if (
      value.target_node?.realm !== c.realm ||
      value.target_node?.node !== c.node ||
      !(timeValue(m?.expires_at) >= expiry)
    )
      invalid('delivery target or expiry');
    if (
      !source ||
      source.message_digest !== digest(m) ||
      source.realm !== m.from?.realm ||
      source.node !== m.from?.node ||
      source.sender_kind !== m.sender_kind
    )
      throw new SessionError(
        'network.sender_mismatch',
        'source attestation mismatch',
      );
  }
  return true;
}

/** Refresh a missing/newer keyset once, then repeat full signature/context checks.
 * @param {any} value
 * @param {Parameters<typeof verifyEnvelope>[1]} options
 * @returns {Promise<true>}
 */
export async function verifyEnvelopeWithRefresh(value, options) {
  try {
    return verifyEnvelope(value, options);
  } catch (error) {
    if (
      !(error instanceof SessionError) ||
      error.code !== 'network.key_unavailable'
    )
      throw error;
    await options.trust.refreshFor(
      value.server_auth.kid,
      value.server_auth.keyset_version,
    );
    return verifyEnvelope(value, options);
  }
}
