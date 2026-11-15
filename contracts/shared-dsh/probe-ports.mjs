// Contract oracle for the W -> E -> ingress -> P candidate probe.
import { readFileSync } from 'node:fs';
import { Buffer } from 'node:buffer';
import { sign, verify } from 'node:crypto';
import Ajv from '../../services/contracts/node_modules/ajv/dist/2020.js';
import addFormats from '../../services/contracts/node_modules/ajv-formats/dist/index.js';
import { canonical, lifetime, sameBinding } from './ports.mjs';
import { checkSwitchProbe } from './k3-ports.mjs';

const ajv = new Ajv({ strict: true, allErrors: true });
addFormats(ajv);
for (const file of ['shared-dsh.schema.json', 'probe.schema.json']) {
  const url = new URL(file, import.meta.url);
  ajv.addSchema(JSON.parse(readFileSync(url, 'utf8')), url.href);
}
export function validateProbe(type, value) {
  const validator = ajv.getSchema(
    new URL('probe.schema.json', import.meta.url).href + '#/$defs/' + type,
  );
  if (!validator?.(value)) throw new Error('common.validation_failed');
  return value;
}
const purposes = {
  ProbeRequest: 'auriga-shared-probe+jwt',
  ProbeReceipt: 'auriga-shared-probe-receipt+jwt',
};
export function signProbe(type, claims, kid, key) {
  validateProbe(type, claims);
  const header = validateProbe('ProbeHeader', {
    alg: 'EdDSA',
    kid,
    typ: purposes[type],
  });
  const input = [header, claims]
    .map((v) => Buffer.from(canonical(v)).toString('base64url'))
    .join('.');
  return (
    input + '.' + sign(null, Buffer.from(input), key).toString('base64url')
  );
}
export function verifyProbe(type, token, keys, now) {
  validateProbe('CompactJws', token);
  if (!Number.isSafeInteger(now) || now < 0) throw new Error('shared.expired');
  const parts = token.split('.');
  const [header, claims] = parts.slice(0, 2).map((part) => {
    const value = JSON.parse(Buffer.from(part, 'base64url').toString('utf8'));
    if (Buffer.from(canonical(value)).toString('base64url') !== part)
      throw new Error('shared.signature_invalid');
    return value;
  });
  validateProbe('ProbeHeader', header);
  validateProbe(type, claims);
  const key = keys.get(header.kid);
  if (
    header.typ !== purposes[type] ||
    key?.asymmetricKeyType !== 'ed25519' ||
    !verify(
      null,
      Buffer.from(parts.slice(0, 2).join('.')),
      key,
      Buffer.from(parts[2], 'base64url'),
    )
  )
    throw new Error('shared.signature_invalid');
  if (
    Buffer.from(claims.challenge, 'base64url').toString('base64url') !==
    claims.challenge
  )
    throw new Error('common.validation_failed');
  lifetime(claims, now, 60);
  return claims;
}
function matchOperation(request, operation) {
  sameBinding(request.binding, operation.binding);
  if (
    request.operationId !== operation.operationId ||
    request.platformSessionId !== operation.platformSessionId ||
    request.jti !== operation.probeJti ||
    request.challenge !== operation.probeChallenge ||
    operation.state !== 'committing' ||
    !['pending', 'switching'].includes(operation.placementState) ||
    !operation.memberActive ||
    !operation.platformSessionActive ||
    !operation.receiptsConfirmed ||
    !operation.resourcesReady
  )
    throw new Error('shared.revoked');
}
// Each hop owns a separate atomic consume store in production. The Set models it.
export function acceptProbeHop(token, keys, context, consumed) {
  const callers = { edge: 'workspace', ingress: 'edge', host: 'ingress' };
  if (
    !Object.hasOwn(callers, context.hop) ||
    !context.authenticated ||
    context.caller !== callers[context.hop]
  )
    throw new Error('common.forbidden');
  const request = verifyProbe('ProbeRequest', token, keys, context.now);
  matchOperation(request, context.operation);
  const key = request.issuer + '/' + request.jti;
  if (consumed.has(key)) throw new Error('common.conflict');
  consumed.add(key);
  return request;
}
export function acceptProbeResult(
  result,
  request,
  keys,
  operation,
  now,
  consumed,
) {
  validateProbe('ProbeResult', result);
  validateProbe('ProbeRequest', request);
  lifetime(request, now, 60);
  matchOperation(request, operation);
  const receipt = verifyProbe('ProbeReceipt', result.receipt, keys, now);
  sameBinding(receipt.binding, request.binding);
  if (
    receipt.operationId !== request.operationId ||
    receipt.platformSessionId !== request.platformSessionId ||
    receipt.requestJti !== request.jti ||
    receipt.challenge !== request.challenge ||
    receipt.contentSha256 !== result.contentSha256 ||
    receipt.expiresAt > request.expiresAt ||
    receipt.observedAt < request.issuedAt ||
    receipt.observedAt > receipt.issuedAt ||
    receipt.observedAt > now
  )
    throw new Error('shared.binding_mismatch');
  checkSwitchProbe(
    {
      ...receipt,
      internalAuthenticated: true,
      memberActive: operation.memberActive,
      platformSessionActive: operation.platformSessionActive,
      path: receipt.canonicalPath,
      normalEntry: true,
      freshChallenge: true,
      success: true,
    },
    operation,
    now,
  );
  const key = receipt.issuer + '/' + receipt.jti;
  if (consumed.has(key)) throw new Error('common.conflict');
  consumed.add(key);
  return receipt;
}
