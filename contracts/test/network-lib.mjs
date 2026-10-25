// Reference checks for contracts/network (README §2–§6). Go/Java/Node
// implementations must reproduce the same decisions on the shared fixtures;
// this module is test tooling, not a production validator.
import {
  createHash,
  createPrivateKey,
  createPublicKey,
  sign,
  verify,
} from 'node:crypto';
import { Buffer } from 'node:buffer';

export const DOMAIN = 'Auriga.ServerEnvelope.v1';
const UINT64_MAX = 18446744073709551615n;
const DEC = /^(?:0|[1-9][0-9]*)$/;
const HEX = /^(?:0|[1-9a-f][0-9a-f]{0,15})$/;

export class NetworkError extends Error {
  constructor(code, detail) {
    super(code + (detail ? ': ' + detail : ''));
    this.code = code;
  }
}
const fail = (code, detail) => {
  throw new NetworkError(code, detail);
};

// ---- strict JSON (duplicate keys, lone surrogates, numbers, depth) ----

export function parseStrict(text, limits) {
  if (Buffer.byteLength(text, 'utf8') > limits.maxPayloadBytes)
    fail('network.protocol_error', 'payload too large');
  let i = 0;
  let values = 0;
  const ws = () => {
    while (i < text.length && ' \t\n\r'.includes(text[i])) i++;
  };
  const bad = (d) => fail('network.protocol_error', d + ' at ' + i);
  function str() {
    if (text[i] !== '"') bad('expected string');
    let j = i + 1;
    let raw = '';
    for (;;) {
      if (j >= text.length) bad('unterminated string');
      const c = text[j];
      if (c === '"') break;
      if (c === '\\') {
        raw += text.slice(j, j + 2);
        if (text[j + 1] === 'u') {
          raw += text.slice(j + 2, j + 6);
          j += 6;
        } else j += 2;
        continue;
      }
      if (c < ' ') bad('control character');
      raw += c;
      j++;
    }
    const value = JSON.parse('"' + raw + '"');
    if (!value.isWellFormed()) bad('ill-formed unicode');
    if (Buffer.byteLength(value, 'utf8') > limits.maxStringBytes)
      bad('string too long');
    i = j + 1;
    return value;
  }
  function val(depth) {
    if (depth > limits.maxJsonDepth) bad('too deep');
    if (++values > limits.maxTotalValues) bad('too many values');
    ws();
    const c = text[i];
    if (c === '{') {
      i++;
      const out = {};
      const seen = new Set();
      ws();
      if (text[i] === '}') {
        i++;
        return out;
      }
      for (;;) {
        ws();
        const k = str();
        if (seen.has(k)) bad('duplicate key ' + k);
        seen.add(k);
        if (seen.size > limits.maxObjectMembers) bad('too many members');
        ws();
        if (text[i++] !== ':') bad('expected :');
        out[k] = val(depth + 1);
        ws();
        if (text[i] === ',') {
          i++;
          continue;
        }
        if (text[i++] === '}') return out;
        bad('expected , or }');
      }
    }
    if (c === '[') {
      i++;
      const out = [];
      ws();
      if (text[i] === ']') {
        i++;
        return out;
      }
      for (;;) {
        out.push(val(depth + 1));
        if (out.length > limits.maxArrayItems) bad('too many items');
        ws();
        if (text[i] === ',') {
          i++;
          continue;
        }
        if (text[i++] === ']') return out;
        bad('expected , or ]');
      }
    }
    if (c === '"') return str();
    const m =
      /^(?:true|false|null|-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?)/.exec(
        text.slice(i),
      );
    if (!m) bad('unexpected token');
    i += m[0].length;
    const v = JSON.parse(m[0]);
    if (
      typeof v === 'number' &&
      !(Number.isSafeInteger(v) && /^-?(?:0|[1-9][0-9]*)$/.test(m[0]))
    )
      bad('non-integer or unsafe number');
    return v;
  }
  const out = val(1);
  ws();
  if (i !== text.length) bad('trailing data');
  return out;
}

// ---- RFC 8785 JCS for the value space allowed above ----

export function jcs(v) {
  if (v === null || typeof v === 'boolean') return JSON.stringify(v);
  if (typeof v === 'number') {
    if (!Number.isSafeInteger(v))
      fail('network.protocol_error', 'unsafe number');
    return JSON.stringify(v);
  }
  if (typeof v === 'string') {
    if (!v.isWellFormed()) fail('network.protocol_error', 'ill-formed unicode');
    return JSON.stringify(v);
  }
  if (Array.isArray(v)) return '[' + v.map(jcs).join(',') + ']';
  return (
    '{' +
    Object.keys(v)
      .sort()
      .map((k) => JSON.stringify(k) + ':' + jcs(v[k]))
      .join(',') +
    '}'
  );
}
export const digest = (value) =>
  'sha256:' +
  createHash('sha256')
    .update(typeof value === 'string' ? value : jcs(value))
    .digest('hex');

// ---- Ed25519 server envelope ----

export const privateKeyFromSeed = (seedHex) =>
  createPrivateKey({
    key: Buffer.concat([
      Buffer.from('302e020100300506032b657004220420', 'hex'),
      Buffer.from(seedHex, 'hex'),
    ]),
    format: 'der',
    type: 'pkcs8',
  });
export const publicKeyFromSpki = (spki) =>
  createPublicKey({
    key: Buffer.from(spki, 'base64url'),
    format: 'der',
    type: 'spki',
  });

export function signedBody(obj) {
  const body = structuredClone(obj);
  delete body.server_auth.signature;
  return jcs(body);
}
export const signingInput = (obj) =>
  Buffer.concat([
    Buffer.from(DOMAIN, 'utf8'),
    Buffer.from([0]),
    Buffer.from(signedBody(obj), 'utf8'),
  ]);

export function sealObject(obj, key) {
  obj.server_auth.signature = '';
  obj.server_auth.signature = sign(null, signingInput(obj), key).toString(
    'base64url',
  );
  return obj;
}
// Recomputes derived digests, then signs items before the page that lists them.
// keyFor(obj) returns the private key for obj.server_auth.purpose.
export function seal(frame, keyFor, { refreshDigest = true } = {}) {
  if (frame.type === 'fetch_result') {
    for (const item of frame.items) seal(item, keyFor, { refreshDigest });
    frame.page.manifest = frame.items.map((item) => ({
      item_type: item.type,
      mail_seq: item.mail_seq ?? null,
      digest: digest(item),
    }));
    sealObject(frame.page, keyFor(frame.page));
    return frame;
  }
  if (refreshDigest && frame.type === 'delivery')
    frame.source_context.message_digest = digest(frame.message);
  if (frame.server_auth) sealObject(frame, keyFor(frame));
  return frame;
}

// ---- addresses ----

const big = (s) => BigInt(s);
function typeOf(node, local) {
  if (node === 0n) return 'group';
  if (local === 0n) return 'system';
  return null; // session or subagent: decided by the node's registration
}

export function parseAddressText(text, ctxRealm) {
  if (typeof text !== 'string') fail('network.invalid_address');
  const scheme = /^([a-z]+)([0-9]+):/.exec(text);
  if (scheme && scheme[1] === 'aur' && scheme[2] !== '1')
    fail('network.unsupported_address_version');
  if (!text.startsWith('aur1:')) fail('network.invalid_address');
  const segs = text.slice(5).split('.');
  if (segs.length < 2 || segs.length > 3)
    fail('network.invalid_address', 'segments');
  if (!segs.every((s) => HEX.test(s)))
    fail('network.invalid_address', 'segment syntax');
  const nums = segs.map((s) => BigInt('0x' + s));
  let realm;
  if (nums.length === 3) realm = nums.shift();
  else {
    if (ctxRealm === undefined)
      fail('network.invalid_address', 'realm context');
    realm = big(ctxRealm);
  }
  if (realm === 0n) fail('network.invalid_address', 'realm 0');
  if (ctxRealm !== undefined && realm !== big(ctxRealm))
    fail('network.realm_not_supported');
  const [node, local] = nums;
  if (node === 0n && local === 0n)
    fail('network.invalid_address', 'node 0 local 0');
  return {
    version: 1,
    realm: realm.toString(),
    node: node.toString(),
    local: local.toString(),
    type: typeOf(node, local),
  };
}

export function formatAddress(a, { short = false } = {}) {
  const hex = (s) => big(s).toString(16);
  return (
    'aur1:' +
    (short ? '' : hex(a.realm) + '.') +
    hex(a.node) +
    '.' +
    hex(a.local)
  );
}

const isU64 = (s) =>
  typeof s === 'string' && DEC.test(s) && big(s) <= UINT64_MAX;

// role: 'source' (from/reply_to), 'node' (unicast target, recipient), 'dest' (to)
export function checkAddressJson(a, ctxRealm, role = 'dest') {
  if (a === null || typeof a !== 'object' || Array.isArray(a))
    fail('network.invalid_address');
  if (a.version !== 1) {
    if (Number.isInteger(a.version) && a.version > 1)
      fail('network.unsupported_address_version');
    fail('network.invalid_address', 'version');
  }
  for (const f of ['realm', 'node', 'local'])
    if (!isU64(a[f])) fail('network.invalid_address', f);
  if (a.realm === '0') fail('network.invalid_address', 'realm 0');
  if (ctxRealm !== undefined && a.realm !== ctxRealm)
    fail('network.realm_not_supported');
  const node = big(a.node);
  const local = big(a.local);
  const ok = {
    system: node > 0n && local === 0n,
    session: node > 0n && local > 0n,
    subagent: node > 0n && local > 0n,
    group: node === 0n && local > 0n,
  }[a.type];
  if (!ok) fail('network.invalid_address', 'type/node/local');
  if (a.type === 'group' && role === 'source')
    fail('network.sender_mismatch', 'group as source');
  if (a.type === 'group' && role === 'node')
    fail('network.target_mismatch', 'group as node target');
}
const sameAddress = (a, b) =>
  !!a &&
  !!b &&
  ['version', 'realm', 'type', 'node', 'local'].every((k) => a[k] === b[k]);

export function classFor(to) {
  if (to?.type !== 'group') return 'unicast';
  return to.local === '1' ? 'broadcast' : 'multicast';
}

// ---- message semantics (platform ingress and receiver both run these) ----

export function checkMessage(m, ctx) {
  if (m === null || typeof m !== 'object') fail('network.protocol_error');
  if (m.to === undefined || m.to === null) fail('network.target_required');
  checkAddressJson(m.from, ctx.realm, 'source');
  checkAddressJson(m.to, ctx.realm, 'dest');
  checkAddressJson(m.reply_to, ctx.realm, 'source');
  if (!sameAddress(m.reply_to, m.from))
    fail('network.sender_mismatch', 'reply_to must equal from');
  if (
    ctx.authenticated &&
    (m.from.realm !== ctx.authenticated.realm ||
      m.from.node !== ctx.authenticated.node)
  )
    fail('network.sender_mismatch', 'from is not the authenticated node');
  const humanOk = m.sender_kind === 'human' && m.from.type === 'system';
  const agentOk =
    m.sender_kind === 'agent' &&
    (m.from.type === 'session' || m.from.type === 'subagent');
  if (!humanOk && !agentOk) fail('network.sender_mismatch', 'sender_kind/from');
  const derived = classFor(m.to);
  const control =
    m.kind === 'control' ||
    m.intent === 'control' ||
    m.body?.subtype === 'control';
  if (control && derived !== 'unicast')
    fail('network.dangerous_multicast_forbidden');
  if (m.message_class !== derived) fail('network.class_mismatch');
  if (
    control &&
    !(
      m.kind === 'control' &&
      m.intent === 'control' &&
      m.body?.subtype === 'control'
    )
  )
    fail('network.protocol_error', 'control kind/intent/body');
  if (m.kind === 'first_contact') {
    if (derived !== 'unicast')
      fail('network.target_mismatch', 'first_contact must be unicast');
    if (m.contact_scope !== 'group_join' && m.to.type !== 'system')
      fail('network.target_mismatch', 'first_contact target');
    if (m.contact_scope === 'human_dialogue' && m.sender_kind !== 'human')
      fail('network.sender_mismatch');
  }
  if (m.kind === 'reply') {
    if (typeof m.in_reply_to !== 'string')
      fail('network.target_mismatch', 'reply without in_reply_to');
    const want = m.reply_mode === 'group' ? 'multicast' : 'unicast';
    if (derived !== want) fail('network.target_mismatch', 'reply_mode');
  }
  if (
    derived === 'broadcast' &&
    typeof m.authorization_ref?.broadcast_grant_id !== 'string'
  )
    fail('network.broadcast_forbidden');
  if (m.durability === 'ephemeral') {
    const ok =
      m.kind === 'message' &&
      m.intent === 'information' &&
      derived !== 'broadcast' &&
      ['progress', 'status'].includes(m.body?.subtype);
    if (!ok)
      fail(
        'network.protocol_error',
        'ephemeral only for progress/status information',
      );
  }
  if (
    m.body !== undefined &&
    Buffer.byteLength(jcs(m.body), 'utf8') > ctx.limits.maxBodyBytes
  )
    fail('network.protocol_error', 'body too large');
  if (
    typeof m.created_at === 'string' &&
    typeof m.expires_at === 'string' &&
    Number.isInteger(m.ttl)
  ) {
    const span = (Date.parse(m.expires_at) - Date.parse(m.created_at)) / 1000;
    if (!(span > 0 && span <= m.ttl))
      fail('network.protocol_error', 'expires_at beyond ttl');
  }
}

// ---- downstream receiver pipeline (README §4–§5) ----

const PURPOSE = {
  delivery: 'business_delivery',
  contact_prompt: 'business_delivery',
  state_update: 'management_state',
};
const secs = (t) => Date.parse(t) / 1000;

function checkAuth(
  obj,
  ctx,
  { requestId, unsolicited = false, epochZeroOk = false },
) {
  const a = obj.server_auth;
  if (!a || typeof a !== 'object' || typeof a.signature !== 'string')
    fail('network.signature_required');
  if (a.version !== 1 || a.alg !== 'Ed25519')
    fail('network.signature_invalid', 'alg');
  const entry = ctx.keys[a.kid];
  if (!entry) fail('network.key_unavailable', String(a.kid));
  const key = entry.key;
  let ok = false;
  try {
    const sig = Buffer.from(a.signature, 'base64url');
    ok =
      sig.length === 64 &&
      sig.toString('base64url') === a.signature &&
      verify(null, signingInput(obj), key, sig);
  } catch (e) {
    if (e instanceof NetworkError) throw e;
  }
  if (!ok) fail('network.signature_invalid');
  const purpose = PURPOSE[obj.type] ?? 'protocol_response';
  if (a.purpose !== purpose || entry.purpose !== purpose)
    fail('network.signature_invalid', 'purpose');
  const au = a.audience ?? {};
  if (
    a.issuer_realm !== ctx.realm ||
    au.realm !== ctx.realm ||
    au.node !== ctx.node ||
    au.incarnation !== ctx.incarnation
  )
    fail('network.signature_invalid', 'audience');
  if (au.epoch !== ctx.epoch && !(epochZeroOk && au.epoch === '0'))
    fail('network.signature_invalid', 'epoch');
  if (a.channel_nonce !== ctx.channelNonce)
    fail('network.signature_invalid', 'channel_nonce');
  if (unsolicited ? a.request_id !== null : a.request_id !== requestId)
    fail('network.signature_invalid', 'request_id');
  const now = secs(ctx.now);
  const life = secs(a.not_after) - secs(a.issued_at);
  if (!(life > 0 && life <= ctx.limits.maxEnvelopeLifetimeSeconds))
    fail('network.signature_invalid', 'lifetime');
  if (
    now < secs(a.issued_at) - ctx.limits.maxClockSkewSeconds ||
    now > secs(a.not_after)
  )
    fail('network.signature_invalid', 'time');
}

function checkItem(item, ctx, requestId) {
  checkAuth(item, ctx, { requestId });
  if (item.epoch !== ctx.epoch) fail('network.signature_invalid', 'item epoch');
  if (item.type === 'contact_prompt')
    checkAddressJson(item.from, ctx.realm, 'source');
  if (item.type === 'delivery' || item.type === 'terminal')
    if (
      item.target_node?.realm !== ctx.realm ||
      item.target_node?.node !== ctx.node
    )
      fail('network.signature_invalid', 'target node');
  if (item.type === 'delivery') {
    checkMessage(item.message, ctx);
    const sc = item.source_context ?? {};
    const m = item.message;
    if (
      sc.realm !== m.from.realm ||
      sc.node !== m.from.node ||
      sc.sender_kind !== m.sender_kind
    )
      fail('network.sender_mismatch', 'source_context');
    if (sc.message_digest !== digest(m))
      fail('network.sender_mismatch', 'message_digest');
    if (secs(item.server_auth.not_after) > secs(m.expires_at))
      fail('network.signature_invalid', 'beyond expires_at');
    const seen = new Set();
    for (const r of item.recipients ?? []) {
      if (seen.has(r.recipient_id))
        fail('network.protocol_error', 'duplicate recipient');
      seen.add(r.recipient_id);
    }
  }
}

function checkCounts(s) {
  if (s.visibility !== 'full' || !s.receipts) return;
  const sum = (o) => Object.values(o).reduce((n, v) => n + big(v), 0n);
  const total = big(s.total_members);
  if (sum(s.receipts) !== total)
    fail('network.protocol_error', 'receipt buckets');
  if (
    s.quarantined !== undefined &&
    big(s.quarantined) > big(s.receipts.pending)
  )
    fail('network.protocol_error', 'quarantined not subset of pending');
  if (s.responses && sum(s.responses) !== total)
    fail('network.protocol_error', 'response buckets');
}

// Returns null when the frame is acceptable for this receiver context.
export function checkDownstream(frame, ctx) {
  try {
    const t = frame?.type;
    if (
      typeof frame?.app_version !== 'string' ||
      !/^1\.[0-9]+$/.test(frame.app_version)
    )
      fail('network.unsupported_app_version');
    if (t === 'fetch_result') {
      const page = frame.page ?? {};
      checkAuth(page, ctx, { requestId: ctx.requestId });
      const items = frame.items ?? [];
      const manifest = page.manifest ?? [];
      if (items.length !== manifest.length)
        fail('network.signature_invalid', 'manifest length');
      items.forEach((item, n) => {
        const e = manifest[n];
        if (
          e.item_type !== item.type ||
          e.mail_seq !== (item.mail_seq ?? null) ||
          e.digest !== digest(item)
        )
          fail('network.signature_invalid', 'manifest entry ' + n);
        checkItem(item, ctx, page.request_id);
      });
    } else if (t === 'head') {
      checkAuth(frame, ctx, { unsolicited: true });
    } else if (
      [
        'delivery',
        'terminal',
        'gap',
        'contact_prompt',
        'state_update',
      ].includes(t)
    ) {
      checkItem(frame, ctx, ctx.requestId);
    } else {
      if (
        frame.request_id !== ctx.requestId &&
        !(t === 'error' && frame.request_id === null)
      )
        fail('network.signature_invalid', 'request correlation');
      checkAuth(frame, ctx, {
        requestId: frame.request_id,
        epochZeroOk: t === 'error',
      });
      if (t === 'ready_result') {
        const missing = (frame.required_features ?? []).filter(
          (f) => !ctx.features.includes(f),
        );
        if (missing.length)
          fail(
            'network.unsupported_app_version',
            'required ' + missing.join(','),
          );
      }
      if (t === 'status') checkCounts(frame);
      if (t === 'error') {
        const def = ctx.errors[frame.code];
        if (!def || def.visibility === 'local_only')
          fail('network.protocol_error', 'error code');
      }
    }
    if (!ctx.validate(frame))
      fail('network.protocol_error', JSON.stringify(ctx.validate.errors?.[0]));
    return null;
  } catch (e) {
    if (e instanceof NetworkError) {
      ctx.onReject?.(e);
      return e.code;
    }
    throw e;
  }
}

// Platform ingress for a plugin-submitted message after stamping (README §3.5).
export function checkIngress(message, ctx) {
  try {
    checkMessage(message, ctx);
    if (!ctx.validate(message))
      fail('network.protocol_error', JSON.stringify(ctx.validate.errors?.[0]));
    return null;
  } catch (e) {
    if (e instanceof NetworkError) {
      ctx.onReject?.(e);
      return e.code;
    }
    throw e;
  }
}

export function checkReceipt(frame, ctx) {
  try {
    const seen = new Set();
    for (const r of frame.results ?? []) {
      if (seen.has(r.recipient_id))
        fail('network.protocol_error', 'duplicate recipient');
      seen.add(r.recipient_id);
      checkAddressJson(r.target, ctx.realm, 'node');
      if (r.target.node !== ctx.node)
        fail('network.target_mismatch', 'receipt for another node');
    }
    if (!ctx.validate(frame))
      fail('network.protocol_error', JSON.stringify(ctx.validate.errors?.[0]));
    return null;
  } catch (e) {
    if (e instanceof NetworkError) {
      ctx.onReject?.(e);
      return e.code;
    }
    throw e;
  }
}

// What a caller may see for an error (README §6).
export function presentError(
  code,
  catalog,
  {
    statusViewer = false,
    diagnostic = false,
    ownNode = false,
    targetRelated = false,
  } = {},
) {
  const def = catalog[code];
  if (!def) throw new Error('unknown code ' + code);
  const allowed = {
    public: !(def.maskIfTargetRelated && targetRelated && !statusViewer),
    status_viewer: statusViewer,
    diagnostic,
    own_node: ownNode,
    local_only: false,
  }[def.visibility];
  if (allowed) return code;
  if (def.visibility === 'own_node' || def.visibility === 'local_only')
    return null;
  return def.maskedAs;
}
