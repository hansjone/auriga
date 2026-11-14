// Executable contract oracle and in-memory port doubles. NOT a production authority.
import { readFileSync } from 'node:fs';
import { verify, sign } from 'node:crypto';
import { Buffer } from 'node:buffer';
import Ajv from '../../services/contracts/node_modules/ajv/dist/2020.js';
import addFormats from '../../services/contracts/node_modules/ajv-formats/dist/index.js';
const schema = JSON.parse(
  readFileSync(new URL('shared-dsh.schema.json', import.meta.url)),
);
const ajv = new Ajv({ strict: true, allErrors: true });
addFormats(ajv);
ajv.addSchema(schema);
export function validate(type, value) {
  const check = ajv.getSchema(schema.$id + '#/$defs/' + type);
  if (!check?.(value)) throw new Error('common.validation_failed');
  return value;
}
export function sameBinding(actual, expected, type = 'Binding') {
  validate(type, actual);
  validate(type, expected);
  for (const key of Object.keys(expected)) {
    if (actual[key] !== expected[key])
      throw new Error('shared.binding_mismatch');
  }
}
export function lifetime(claims, now, max) {
  if (
    claims.issuedAt > now ||
    claims.expiresAt <= now ||
    claims.expiresAt <= claims.issuedAt ||
    claims.expiresAt - claims.issuedAt > max
  )
    throw new Error('shared.expired');
}
export function authorizeCapability(claims, expected, context) {
  validate('Capability', claims);
  sameBinding(claims.binding, expected, 'SessionBinding');
  lifetime(claims, context.now, 600);
  const allowed = {
    'auriga.shared.enterprise': ['credentials', 'outbound'],
    'auriga.shared.model': ['model.invoke'],
    'auriga.shared.message': ['message.send', 'message.receive'],
  };
  if (
    claims.audience !== context.audience ||
    !claims.scopes.includes(context.scope) ||
    claims.scopes.some((s) => !allowed[claims.audience].includes(s)) ||
    context.revoked.has(claims.jti) ||
    claims.taskId !== context.taskId ||
    claims.credentialVersion !== context.credentialVersion ||
    (context.scope === 'credentials' && !context.allowRawAgentToken)
  )
    throw new Error('shared.revoked');
}
// Canonical JSON subset: protocol numbers are safe integers, field names ASCII.
export function canonical(value) {
  if (Array.isArray(value)) return '[' + value.map(canonical).join(',') + ']';
  if (value && typeof value === 'object')
    return (
      '{' +
      Object.keys(value)
        .sort()
        .map((k) => JSON.stringify(k) + ':' + canonical(value[k]))
        .join(',') +
      '}'
    );
  return JSON.stringify(value);
}
export function signClaims(header, claims, key) {
  validate('SignedHeader', header);
  const input = [header, claims]
    .map((v) => Buffer.from(canonical(v)).toString('base64url'))
    .join('.');
  return (
    input + '.' + sign(null, Buffer.from(input), key).toString('base64url')
  );
}
export function verifyClaims(token, type, typ, keys) {
  if (typeof token !== 'string' || token.length > 16384)
    throw new Error('shared.signature_invalid');
  const parts = token.split('.');
  if (parts.length !== 3 || parts.some((p) => !/^[A-Za-z0-9_-]+$/.test(p)))
    throw new Error('shared.signature_invalid');
  const [header, claims] = parts.slice(0, 2).map((p) => {
    const bytes = Buffer.from(p, 'base64url');
    const value = JSON.parse(bytes.toString('utf8'));
    if (Buffer.from(canonical(value)).toString('base64url') !== p)
      throw new Error('shared.signature_invalid');
    return value;
  });
  validate('SignedHeader', header);
  validate(type, claims);
  const key = keys.get(header.kid);
  if (
    header.typ !== typ ||
    !key ||
    key.asymmetricKeyType !== 'ed25519' ||
    !verify(
      null,
      Buffer.from(parts.slice(0, 2).join('.')),
      key,
      Buffer.from(parts[2], 'base64url'),
    )
  )
    throw new Error('shared.signature_invalid');
  return claims;
}
export class EntryDouble {
  used = new Set();
  constructor(binding, platformSessionId) {
    this.binding = binding;
    this.platformSessionId = platformSessionId;
    this.active = true;
  }
  accept(claims, type, context) {
    validate(type, claims);
    sameBinding(claims.binding, this.binding);
    lifetime(claims, context.now, 60);
    if (type === 'Identity') {
      const path = claims.canonicalPath;
      if (/\/\/|(^|\/)\.{1,2}(\/|$)|%2f|%5c|%2e/i.test(path))
        throw new Error('shared.binding_mismatch');
      try {
        decodeURIComponent(path);
      } catch {
        throw new Error('shared.binding_mismatch');
      }
    }
    if (
      !this.active ||
      claims.platformSessionId !== this.platformSessionId ||
      this.used.has(claims.jti)
    )
      throw new Error('shared.revoked');
    if (
      type === 'Identity' &&
      (claims.method !== context.method ||
        claims.canonicalPath !== context.canonicalPath)
    )
      throw new Error('shared.binding_mismatch');
    this.used.add(claims.jti);
  }
}
export function exchangeTicket(request, entry, keys, context) {
  validate('TicketExchangeRequest', request);
  const claims = verifyClaims(
    request.ticket,
    'OpenTicket',
    'auriga-shared-ticket+jwt',
    keys,
  );
  if (claims.binding.physicalInstanceId !== context.physicalInstanceId)
    throw new Error('shared.binding_mismatch');
  if (!context.ready) throw new Error('common.conflict');
  if (entry.used.has(claims.jti)) throw new Error('common.conflict');
  entry.accept(claims, 'OpenTicket', context);
  return {
    status: 302,
    location: context.ipMode ? '/i/' + context.physicalInstanceId + '/' : '/',
    cacheControl: 'no-store',
  };
}
export function stepUp({ ctx, permission, stepupTime, active }, now) {
  if (ctx !== 'admin' || permission !== 'workspace.placements.write' || !active)
    throw new Error('common.forbidden');
  if (
    !Number.isSafeInteger(stepupTime) ||
    now < stepupTime ||
    now - stepupTime >= 600
  )
    throw new Error('iam.step_up_required');
}
export function confirmReceipt(intent, receipt) {
  validate('RuntimeIntent', intent);
  validate('RuntimeReceipt', receipt);
  sameBinding(receipt.binding, intent.binding);
  if (
    receipt.operationId !== intent.operationId ||
    receipt.action !== intent.action
  )
    throw new Error('shared.binding_mismatch');
  if (receipt.outcome !== 'confirmed') return 'blocked';
  if (intent.action === 'stop-pool' && !receipt.executionTreeStopped)
    return 'blocked';
  if (intent.action === 'discard-sessions' && !receipt.sessionsDiscarded)
    return 'blocked';
  if (intent.action === 'prepare-empty' && !receipt.workspaceEmpty)
    return 'blocked';
  if (
    intent.action === 'archive-reset' &&
    (!receipt.workspaceEmpty ||
      !receipt.archiveId ||
      receipt.archiveExpiresAt !== receipt.observedAt + 2592000)
  )
    return 'blocked';
  return 'confirmed';
}
export function admitResourceView(view, binding, authorizationSetDigest, now) {
  validate('ResourceView', view);
  sameBinding(view.binding, binding);
  if (
    view.authorizationSetDigest !== authorizationSetDigest ||
    view.resources.some((r) => r.expiresAt <= now)
  )
    throw new Error('shared.revoked');
}
