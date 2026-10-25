import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync, readdirSync } from 'node:fs';
import { verify } from 'node:crypto';
import Ajv2020 from '../../services/contracts/node_modules/ajv/dist/2020.js';
import addFormats from '../../services/contracts/node_modules/ajv-formats/dist/index.js';
import {
  DOMAIN,
  checkAddressJson,
  checkDownstream,
  checkIngress,
  checkReceipt,
  digest,
  formatAddress,
  jcs,
  parseAddressText,
  parseStrict,
  presentError,
  privateKeyFromSeed,
  publicKeyFromSpki,
  seal,
  signedBody,
  signingInput,
} from './network-lib.mjs';

const dir = new URL('../network/', import.meta.url);
const read = (name) => JSON.parse(readFileSync(new URL(name, dir), 'utf8'));
const schemaFiles = readdirSync(dir).filter((f) => f.endsWith('.schema.json'));
const schemas = Object.fromEntries(
  schemaFiles.map((f) => [f.replace('.schema.json', ''), read(f)]),
);
const relax = (s) => {
  if (Array.isArray(s)) return s.map(relax);
  if (s === null || typeof s !== 'object') return s;
  const out = {};
  for (const [k, v] of Object.entries(s)) {
    if (k === 'additionalProperties' && v === false) continue;
    out[k] = relax(v);
  }
  if (typeof out.$id === 'string')
    out.$id = out.$id.replace('/network/', '/network-consumer/');
  return out;
};
const ajv = new Ajv2020({ strict: true, allErrors: true });
addFormats(ajv);
for (const s of Object.values(schemas)) {
  ajv.addSchema(s);
  ajv.addSchema(relax(s));
}
const validator = (def, consumer = false) => {
  const [file, name] = def.split('#');
  const base = schemas[file].$id.replace(
    '/network/',
    consumer ? '/network-consumer/' : '/network/',
  );
  return ajv.getSchema(base + '#/$defs/' + name);
};
const limits = read('limits.json');
const errors = read('errors.json').errors;
const fixtures = read('fixtures/frames.json');
const vectors = read('fixtures/signature-vectors.json');
const addressCases = read('fixtures/address-cases.json');
const frameCases = read('fixtures/frame-cases.json').cases;
const rawCases = read('fixtures/raw-cases.json');
const presentation = read('fixtures/error-presentation.json').cases;
const flatLimits = { ...limits.encoding, ...limits.serverAuth };

const keysByKid = Object.fromEntries(
  vectors.keys.map((k) => [
    k.kid,
    {
      purpose: k.purpose,
      key: publicKeyFromSpki(k.publicKeySpki),
      priv: privateKeyFromSeed(k.testOnlySeedHex),
    },
  ]),
);
const keysByPurpose = Object.fromEntries(
  Object.values(keysByKid).map((k) => [k.purpose, k]),
);
const byPurpose = (obj) => keysByPurpose[obj.server_auth.purpose].priv;
const byKid = (obj) => keysByKid[obj.server_auth.kid].priv;

const trace = process.env.NETWORK_TRACE
  ? (e) => console.error('  reject: ' + e.message)
  : undefined;
const receiverCtx = (frame, override = {}, consumer = false) => ({
  onReject: trace,
  ...fixtures.receiver,
  ...frame.context,
  ...override,
  keys: keysByKid,
  limits: flatLimits,
  features: [
    'server_auth_v1',
    'source_attestation_v1',
    'inbox_only_v1',
    'broadcast_grants_v1',
  ],
  errors,
  validate: validator(frame.def, consumer),
});

// ---- path helpers for fixture ops ----
const split = (p) => (p === '' ? [] : p.split('.'));
function at(root, path) {
  const keys = split(path);
  const leaf = keys.pop();
  const parent = keys.reduce((v, k) => v[k], root);
  return [parent, leaf];
}
function expand(template, i) {
  if (typeof template === 'string')
    return template
      .replaceAll('{i1}', String(i + 1))
      .replaceAll('{i}', String(i));
  if (Array.isArray(template)) return template.map((t) => expand(t, i));
  if (template && typeof template === 'object')
    return Object.fromEntries(
      Object.entries(template).map(([k, v]) => [k, expand(v, i)]),
    );
  return template;
}
function apply(value, ops) {
  let root = value;
  for (const op of ops) {
    if ('set' in op) {
      const [p, k] = at(root, op.set);
      p[k] = structuredClone(op.value);
    } else if ('remove' in op) {
      const [p, k] = at(root, op.remove);
      if (Array.isArray(p)) p.splice(Number(k), 1);
      else delete p[k];
    } else if ('seal' in op) {
      if (op.seal === 'keep_digest')
        seal(root, byPurpose, { refreshDigest: false });
      else if (op.seal === 'with_kid_key') seal(root, byKid);
      else seal(root, byPurpose);
    } else if ('repeat' in op) {
      const [p, k] = at(root, op.repeat);
      p[k] = op.char.repeat(op.count);
    } else if ('fill' in op) {
      const [p, k] = at(root, op.fill);
      p[k] = Array.from({ length: op.count }, (_, i) => expand(op.template, i));
    } else if ('copy' in op) {
      const [p, k] = at(root, op.copy);
      p[k] = structuredClone(fixtures.frames[op.from].value);
    } else if ('noncanonical_sig' in op) {
      const [p, k] = at(root, op.noncanonical_sig);
      const last = p[k].at(-1);
      // Same decoded bytes, different trailing bits: must not be accepted.
      p[k] = p[k].slice(0, -1) + String.fromCharCode(last.charCodeAt(0) + 1);
    } else if ('reorder' in op) {
      const target =
        op.reorder === '' ? root : at(root, op.reorder).reduce((p, k) => p[k]);
      const entries = Object.entries(target).reverse();
      for (const [k] of entries) delete target[k];
      for (const [k, v] of entries) target[k] = v;
    } else throw new Error('unknown op ' + JSON.stringify(op));
  }
  return root;
}

// ---- schemas and catalogs ----

test('network schema 全部在严格模式下编译', () => {
  for (const [file, s] of Object.entries(schemas)) {
    assert.ok(ajv.getSchema(s.$id), file);
    for (const name of Object.keys(s.$defs ?? {}))
      assert.ok(validator(file + '#' + name), file + '#' + name);
  }
});

test('错误码目录：命名、可见性与掩码一致', () => {
  const visibility = new Set([
    'public',
    'status_viewer',
    'diagnostic',
    'own_node',
    'local_only',
  ]);
  for (const [code, def] of Object.entries(errors)) {
    assert.match(code, /^network\.[a-z][a-z0-9_]*$/);
    assert.ok(visibility.has(def.visibility), code);
    assert.equal(typeof def.retryable, 'boolean', code);
    assert.ok(def.message['zh-CN'] && def.message.en, code);
    if (
      def.visibility === 'status_viewer' ||
      def.visibility === 'diagnostic' ||
      def.maskIfTargetRelated
    )
      assert.ok(
        def.maskedAs === 'request_submitted' ||
          errors[def.maskedAs]?.visibility === 'public',
        code,
      );
  }
  for (const code of [
    'network.contact_pending',
    'network.contact_denied',
    'network.contact_expired',
    'network.broadcast_forbidden',
    'network.stale_epoch',
    'network.recovery_conflict',
    'network.target_unavailable',
  ])
    assert.ok(errors[code], code);
  const used = new Set(
    [
      ...frameCases.map((c) => c.expect),
      ...addressCases.text.map((c) => c.error),
      ...addressCases.json.map((c) => c.error),
    ].filter((c) => typeof c === 'string'),
  );
  for (const code of used)
    assert.ok(errors[code], 'fixture uses undefined ' + code);
});

test('limits.json 与 schema 上限一致', () => {
  const fetch = validator('protocol#Fetch').schema;
  assert.equal(fetch.properties.max_items.maximum, limits.fetch.maxItems);
  assert.equal(fetch.properties.max_bytes.maximum, limits.fetch.maxBytes);
  assert.equal(fetch.properties.held_ids.maxItems, limits.fetch.maxHeldIds);
  assert.equal(fetch.properties.paused.maxItems, limits.fetch.maxPaused);
  assert.equal(
    fetch.properties.state_barrier.maxItems,
    limits.fetch.maxStateBarrier,
  );
  assert.equal(
    schemas.envelope.$defs.Recipients.maxItems,
    limits.dispatch.maxLocalsPerNodeItem,
  );
  assert.equal(
    schemas.protocol.$defs.Receipt.properties.results.maxItems,
    limits.receipt.maxResultsPerReceipt,
  );
  assert.equal(
    schemas.protocol.$defs.FetchPage.properties.manifest.maxItems,
    limits.fetch.maxItems,
  );
  assert.equal(
    schemas.envelope.$defs.Message.properties.ttl.maximum,
    limits.message.maxTtlSeconds,
  );
  assert.equal(
    schemas.envelope.$defs.Text.maxLength,
    limits.encoding.maxBodyBytes,
  );
  assert.equal(
    schemas.envelope.$defs.Ext.maxProperties,
    limits.encoding.maxExtMembers,
  );
  assert.equal(schemas.envelope.$defs.Flags.maxItems, limits.encoding.maxFlags);
  assert.equal(
    schemas.envelope.$defs.ContactPromptItem.properties.preview.maxLength,
    limits.encoding.maxContactPreviewChars,
  );
  assert.equal(
    schemas.envelope.$defs.SourceContext.properties.display_label.maxLength,
    limits.encoding.maxDisplayLabelChars,
  );
  assert.equal(
    schemas.protocol.$defs.AttemptResult.properties.max_attempts.const,
    limits.processing.maxAutomaticAttempts,
  );
  assert.ok(limits.dispatch.atomicMaxLocals <= limits.encoding.maxArrayItems);
});

// ---- addresses ----

for (const c of addressCases.text)
  test(
    '文本地址 ' + JSON.stringify(c.input) + (c.error ? ' 拒绝' : ' 规范化'),
    () => {
      const ctxRealm = c.realm ?? undefined;
      if (c.error) {
        assert.throws(
          () => parseAddressText(c.input, ctxRealm),
          (e) => e.code === c.error,
        );
      } else {
        assert.deepEqual(parseAddressText(c.input, ctxRealm), {
          version: 1,
          ...c.expect,
        });
        assert.equal(validator('address#AddressText')(c.input), true);
      }
    },
  );
test('两段/三段等价、规范输出与短别名', () => {
  for (const [a, b] of addressCases.equivalent)
    assert.deepEqual(parseAddressText(a, '2'), parseAddressText(b, '2'));
  for (const c of addressCases.format) {
    assert.equal(formatAddress(c.address), c.full);
    assert.equal(formatAddress(c.address, { short: true }), c.short);
    const back = parseAddressText(c.full, '2');
    assert.deepEqual(
      [back.realm, back.node, back.local],
      [c.address.realm, c.address.node, c.address.local],
    );
  }
});
for (const c of addressCases.json)
  test('JSON 地址：' + c.name, () => {
    const run = () => checkAddressJson(c.input, c.realm ?? '2', c.role);
    if (c.error) assert.throws(run, (e) => e.code === c.error);
    else run();
    const role = {
      source: 'SourceAddress',
      node: 'NodeAddress',
      dest: 'DestinationAddress',
    }[c.role];
    const schemaOk = validator('address#' + role)(c.input);
    // realm_not_supported depends on context, not syntax.
    if (c.error === 'network.realm_not_supported') assert.equal(schemaOk, true);
    else assert.equal(schemaOk, c.error === null, c.name);
  });

// ---- legal frames ----

for (const [name, f] of Object.entries(fixtures.frames))
  test('合法帧通过生产方 schema：' + name, () => {
    const v = validator(f.def);
    assert.equal(v(f.value), true, JSON.stringify(v.errors));
  });
for (const [name, f] of Object.entries(fixtures.frames).filter(
  ([, f]) => f.value.server_auth || f.value.page,
))
  test('合法下行帧通过接收方检查：' + name, () => {
    assert.equal(
      checkDownstream(structuredClone(f.value), receiverCtx(f)),
      null,
    );
  });
test('下行帧重新封装得到相同字节（JCS + Ed25519 确定性）', () => {
  for (const [name, f] of Object.entries(fixtures.frames).filter(
    ([, f]) => f.value.server_auth || f.value.page,
  )) {
    const again = seal(structuredClone(f.value), byPurpose);
    assert.deepEqual(again, f.value, name);
  }
});

// ---- signature vectors ----

test('签名输入：域分隔串 + 0x00 + JCS（去掉 server_auth.signature）', () => {
  assert.equal(DOMAIN, limits.serverAuth.domainSeparator);
  for (const v of vectors.vectors) {
    const frame = fixtures.frames[v.name].value;
    assert.equal(signedBody(frame), v.canonical);
    assert.equal(frame.server_auth.signature, v.signature);
    const input = signingInput(frame);
    assert.equal(
      input.subarray(0, DOMAIN.length + 1).toString('latin1'),
      DOMAIN + '\0',
    );
    const sig = Buffer.from(v.signature, 'base64url');
    assert.equal(sig.length, limits.serverAuth.signatureBytes);
    assert.ok(verify(null, input, keysByKid[v.kid].key, sig), v.name);
    // Without the domain prefix the same bytes must not verify.
    assert.equal(
      verify(null, Buffer.from(v.canonical), keysByKid[v.kid].key, sig),
      false,
    );
    const tampered = JSON.parse(v.canonical);
    tampered.server_auth.keyset_version = '6';
    assert.equal(
      verify(
        null,
        Buffer.concat([Buffer.from(DOMAIN + '\0'), Buffer.from(jcs(tampered))]),
        keysByKid[v.kid].key,
        sig,
      ),
      false,
    );
  }
  for (const d of vectors.digests) assert.equal(digest(d.canonical), d.digest);
  const m = fixtures.frames.delivery_first_contact.value;
  assert.equal(m.source_context.message_digest, digest(m.message));
});
test('JCS：键排序、无空白、uint64 保持字符串、拒绝非安全数字', () => {
  assert.equal(
    jcs({ b: '18446744073709551615', a: [1, true, null], é: 'é' }),
    '{"a":[1,true,null],"b":"18446744073709551615","é":"é"}',
  );
  assert.throws(
    () => jcs({ n: 2 ** 53 }),
    (e) => e.code === 'network.protocol_error',
  );
  assert.throws(
    () => jcs({ n: 0.5 }),
    (e) => e.code === 'network.protocol_error',
  );
  assert.throws(
    () => jcs({ s: '\ud800' }),
    (e) => e.code === 'network.protocol_error',
  );
});

// ---- mutation cases ----

const ingressCtx = (consumer) => ({
  onReject: trace,
  realm: '2',
  authenticated: { realm: '2', node: '63' },
  limits: flatLimits,
  validate: validator('envelope#Message', consumer),
});
for (const c of frameCases)
  test('[' + c.mode + '] ' + c.name, () => {
    const f = fixtures.frames[c.base];
    assert.ok(f, c.base);
    if (c.mode === 'ingress') {
      const message = apply(structuredClone(f.value.message), c.ops);
      assert.equal(checkIngress(message, ingressCtx(c.consumer)), c.expect);
    } else if (c.mode === 'downstream') {
      const frame = apply(structuredClone(f.value), c.ops);
      assert.equal(
        checkDownstream(frame, receiverCtx(f, c.context, c.consumer)),
        c.expect,
      );
    } else if (c.mode === 'receipt') {
      const frame = apply(structuredClone(f.value), c.ops);
      assert.equal(
        checkReceipt(frame, {
          realm: '2',
          node: '64',
          validate: validator(f.def),
          onReject: trace,
        }),
        c.expect,
      );
    } else if (c.mode === 'schema') {
      const v = validator(f.def);
      assert.equal(v(apply(structuredClone(f.value), c.ops)), c.expect);
    } else throw new Error('unknown mode ' + c.mode);
  });

// ---- raw bytes ----

const rawText = (c) => {
  if (c.nest) return '['.repeat(c.nest) + ']'.repeat(c.nest);
  if (c.members)
    return (
      '{' +
      Array.from({ length: c.members }, (_, i) => '"k' + i + '":' + i).join(
        ',',
      ) +
      '}'
    );
  let text = JSON.stringify(fixtures.frames[c.base].value);
  if (c.replace) {
    assert.ok(text.includes(c.replace[0]), c.name);
    text = text.replace(c.replace[0], c.replace[1]);
  }
  if (c.append) text += c.append.repeat(c.repeatAppend ?? 1);
  return text;
};
for (const c of rawCases.cases)
  test('原始字节拒绝：' + c.name, () => {
    assert.throws(
      () => parseStrict(rawText(c), limits.encoding),
      (e) => e.code === 'network.protocol_error',
    );
  });
for (const c of rawCases.accept)
  test('原始字节接受：' + c.name, () => {
    parseStrict(rawText(c), limits.encoding);
  });
test('严格解析与 JSON.parse 对合法帧结果一致', () => {
  for (const f of Object.values(fixtures.frames)) {
    const text = JSON.stringify(f.value);
    assert.deepEqual(parseStrict(text, limits.encoding), JSON.parse(text));
  }
});

// ---- presentation / privacy ----

for (const c of presentation)
  test('错误呈现：' + c.code + ' ' + JSON.stringify(c.viewer), () => {
    assert.equal(presentError(c.code, errors, c.viewer), c.expect);
  });
test('目标不可接收对外只有一种外观', () => {
  // Not-found / archived / no relation / blocked share one public code and receipt shape.
  assert.equal(errors['network.target_unavailable'].visibility, 'public');
  assert.equal(
    schemas.protocol.$defs.ReceiptResult.properties.code.const,
    'network.target_unavailable',
  );
  assert.equal(
    fixtures.frames.receipt.value.results[1].code,
    'network.target_unavailable',
  );
});
