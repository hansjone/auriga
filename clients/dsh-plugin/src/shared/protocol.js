// @ts-nocheck
import { readFileSync } from 'node:fs';
import { Buffer } from 'node:buffer';
import { verify } from 'node:crypto';
import { canonicalize } from '../managed/jcs.js';

// Packaged snapshot of K's schema; drift is checked in the adapter tests.
const schema = JSON.parse(
  readFileSync(new URL('./contract.schema.json', import.meta.url), 'utf8'),
);
export const canonical = canonicalize;
export function fail(code = 'shared.revoked') {
  throw Object.assign(new Error(code), { code });
}
function check(s, v) {
  if (s.$ref) return check(schema.$defs[s.$ref.split('/').at(-1)], v);
  if (s.const !== undefined && v !== s.const) fail('common.validation_failed');
  if (s.enum && !s.enum.includes(v)) fail('common.validation_failed');
  if (s.type === 'object') {
    if (!v || typeof v !== 'object' || Array.isArray(v))
      fail('common.validation_failed');
    if (s.required?.some((k) => !Object.hasOwn(v, k)))
      fail('common.validation_failed');
    for (const k of Object.keys(v)) {
      if (!s.properties || !Object.hasOwn(s.properties, k))
        fail('common.validation_failed');
      check(s.properties[k], v[k]);
    }
  } else if (s.type === 'array') {
    if (
      !Array.isArray(v) ||
      v.length < (s.minItems ?? 0) ||
      (s.uniqueItems && new Set(v.map(canonical)).size !== v.length)
    )
      fail('common.validation_failed');
    v.forEach((x) => check(s.items, x));
  } else if (s.type === 'integer') {
    if (
      !Number.isSafeInteger(v) ||
      v < (s.minimum ?? -Infinity) ||
      v > (s.maximum ?? Infinity)
    )
      fail('common.validation_failed');
  } else if (s.type === 'string') {
    if (
      typeof v !== 'string' ||
      v.length < (s.minLength ?? 0) ||
      v.length > (s.maxLength ?? Infinity) ||
      (s.pattern && !new RegExp(s.pattern).test(v)) ||
      (s.format === 'uuid' &&
        !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(
          v,
        ))
    )
      fail('common.validation_failed');
  } else if (s.type === 'boolean' && typeof v !== 'boolean')
    fail('common.validation_failed');
}
export function validate(type, value) {
  if (
    ![
      'Binding',
      'SessionBinding',
      'Identity',
      'Capability',
      'ResourceView',
      'Revocation',
      'SignedHeader',
    ].includes(type)
  )
    fail('common.validation_failed');
  check(schema.$defs[type], value);
  return value;
}
export function same(actual, expected) {
  if (Object.keys(expected).some((k) => actual[k] !== expected[k]))
    fail('shared.binding_mismatch');
}
export function lifetime(c, now, max) {
  if (
    c.issuedAt > now ||
    c.expiresAt <= now ||
    c.expiresAt <= c.issuedAt ||
    c.expiresAt - c.issuedAt > max
  )
    fail('shared.expired');
}
export function verifyToken(token, type, keys) {
  try {
    if (typeof token !== 'string' || token.length > 16384) fail();
    const p = token.split('.');
    if (p.length !== 3 || p.some((x) => !/^[A-Za-z0-9_-]+$/.test(x))) fail();
    const [h, c] = p.slice(0, 2).map((x) => {
      const v = JSON.parse(Buffer.from(x, 'base64url').toString('utf8'));
      if (Buffer.from(canonical(v)).toString('base64url') !== x) fail();
      return v;
    });
    validate('SignedHeader', h);
    validate(type, c);
    const key = keys.get(h.kid);
    const typ = type === 'Identity' ? 'identity' : 'capability';
    if (
      h.typ !== `auriga-shared-${typ}+jwt` ||
      key?.asymmetricKeyType !== 'ed25519' ||
      !verify(
        null,
        Buffer.from(p.slice(0, 2).join('.')),
        key,
        Buffer.from(p[2], 'base64url'),
      )
    )
      fail();
    return c;
  } catch {
    fail('shared.signature_invalid');
  }
}

export const SHARED_RISK =
  '共享池仅限互信团队。成员可能互相看到文件，并可能以 owner 能力操作。成员之间会话内容可见，且可能以其他成员的身份操作内部应用、审计记在被冒用者名下。同一进程卡死、崩溃或取消超时会中断全池任务。需要保密或独立故障域请使用独立容器。';
