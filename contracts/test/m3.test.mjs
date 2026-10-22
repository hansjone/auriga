import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync, readdirSync } from 'node:fs';
import { createHash, createPublicKey, verify } from 'node:crypto';
import { fileURLToPath, pathToFileURL } from 'node:url';
import path from 'node:path';
import Ajv2020 from '../../services/contracts/node_modules/ajv/dist/2020.js';
import addFormats from '../../services/contracts/node_modules/ajv-formats/dist/index.js';

const root = fileURLToPath(new URL('../', import.meta.url));
const read = (file) => JSON.parse(readFileSync(path.join(root, file), 'utf8'));
const ajv = new Ajv2020({
  strict: true,
  allowUnionTypes: true,
  allErrors: true,
});
addFormats(ajv);
function load(folder) {
  for (const entry of readdirSync(folder, { withFileTypes: true })) {
    const p = path.join(folder, entry.name);
    if (entry.isDirectory()) load(p);
    else if (p.endsWith('.schema.json'))
      ajv.addSchema(JSON.parse(readFileSync(p, 'utf8')), pathToFileURL(p).href);
  }
}
load(root);
const validate = (file, definition) =>
  ajv.getSchema(
    pathToFileURL(path.join(root, file)).href +
      (definition ? '#/$defs/' + definition : ''),
  );
const canon = (v) =>
  v === null || typeof v !== 'object'
    ? JSON.stringify(v)
    : Array.isArray(v)
      ? '[' + v.map(canon).join(',') + ']'
      : '{' +
        Object.keys(v)
          .sort()
          .map((k) => JSON.stringify(k) + ':' + canon(v[k]))
          .join(',') +
        '}';
const clone = (v) => structuredClone(v);
const aiUri = pathToFileURL(path.join(root, 'openapi/ai.yaml')).href;
function schemaRefs(value) {
  if (Array.isArray(value)) return value.map(schemaRefs);
  if (!value || typeof value !== 'object') return value;
  return Object.fromEntries(
    Object.entries(value).map(([key, child]) => [
      key,
      key === '$ref' && typeof child === 'string'
        ? child.replace('#/components/schemas/', '#/$defs/')
        : schemaRefs(child),
    ]),
  );
}
ajv.addSchema({
  $id: aiUri,
  $defs: schemaRefs(read('openapi/ai.yaml').components.schemas),
});

test('受限JSON与SSE golden逐帧校验，未知字段/单字符串stop/缺输出上限拒绝', () => {
  const request = ajv.getSchema(aiUri + '#/$defs/ChatCompletionRequest');
  const completion = ajv.getSchema(aiUri + '#/$defs/ChatCompletion');
  const chunk = ajv.getSchema(aiUri + '#/$defs/ChatCompletionChunk');
  const body = read('ai/fixtures/chat-request.json');
  assert.equal(request(body), true, JSON.stringify(request.errors));
  for (const mutate of [
    (r) => (r.stop = 'END'),
    (r) => delete r.max_tokens,
    (r) => (r.baseUrl = 'https://example.com'),
    (r) => (r.max_tokens = 0),
  ]) {
    const invalid = clone(body);
    mutate(invalid);
    assert.equal(request(invalid), false);
  }
  assert.equal(
    completion(read('ai/fixtures/chat-completion.json')),
    true,
    JSON.stringify(completion.errors),
  );
  const frames = readFileSync(
    path.join(root, 'ai/fixtures/chat-completion.sse'),
    'utf8',
  )
    .split(/\r?\n/)
    .filter((line) => line.startsWith('data: '))
    .map((line) => line.slice(6));
  assert.equal(frames.at(-1), '[DONE]');
  assert.equal(frames.length, 5);
  for (const frame of frames.slice(0, -1))
    assert.equal(chunk(JSON.parse(frame)), true, JSON.stringify(chunk.errors));
});
const uuid = '019a0000-0000-7000-8000-000000000001';
const token = {
  iss: 'http://192.0.2.10:8080',
  sub: uuid,
  aud: 'auriga.ai-gateway',
  exp: 300,
  iat: 0,
  jti: 'test',
  tid: uuid,
  ptyp: 'user',
  ctx: 'instance',
  instanceId: uuid,
  credentialVersion: 1,
  perms: ['ai.models.invoke', 'ai.usage.read'],
};
test('实例AI token：允许内网IP issuer，但身份/权限/绑定严格拒绝越权', () => {
  const check = validate('auth/access-token.schema.json');
  assert.equal(check(token), true, JSON.stringify(check.errors));
  for (const mutate of [
    (t) => t.perms.push('ai.providers.write'),
    (t) => delete t.instanceId,
    (t) => (t.credentialVersion = 0),
    (t) => (t.credentialVersion = 9007199254740992),
    (t) => (t.ptyp = 'service_account'),
    (t) => (t.aud = 'auriga'),
    (t) => (t.aud = ['auriga.ai-gateway', 'auriga']),
    (t) => (t.sid = uuid),
    (t) => (t.did = uuid),
    (t) => (t.roles = ['owner']),
    (t) => (t.ctx = 'portal'),
  ]) {
    const t = clone(token);
    mutate(t);
    assert.equal(check(t), false, JSON.stringify(t));
  }
  const portal = { ...token, ctx: 'portal', aud: 'auriga' };
  delete portal.instanceId;
  delete portal.credentialVersion;
  assert.equal(check(portal), true);
});

const catalog = read('catalog/m3.json');
test('AI/资源每个操作引用唯一权限和错误目录，管理面标注与敏感stepup一致', () => {
  let count = 0;
  const ids = new Set();
  for (const service of ['ai', 'resource']) {
    const spec = read('openapi/' + service + '.yaml');
    for (const [url, paths] of Object.entries(spec.paths)) {
      for (const [method, o] of Object.entries(paths)) {
        count++;
        assert(!ids.has(o.operationId));
        ids.add(o.operationId);
        if (o['x-internal-only']) {
          assert.equal(o['x-authorization'], 'node-bound-grant');
          assert.deepEqual(o.security, [{ nodeMtls: [] }]);
        } else {
          const p = catalog.permissions[o['x-permission']];
          assert(p, o.operationId);
          assert.equal(Boolean(o['x-admin-only']), p.adminOnly, o.operationId);
          if (
            p.sensitive &&
            [
              'ai.credentials.rotate',
              'resource.versions.review',
              'resource.versions.publish',
              'resource.levels.admin',
            ].includes(o['x-permission'])
          )
            assert.equal(o['x-step-up-max-age-seconds'], 600);
        }
        for (const code of o['x-error-codes'])
          assert(
            catalog.errors[code] ||
              code.startsWith('common.') ||
              code === 'iam.step_up_required',
            code,
          );
        for (const evt of o['x-events']) assert(catalog.events[evt], evt);
        if (method === 'post')
          assert(o.parameters.some((p) => p.$ref?.endsWith('/IdempotencyKey')));
      }
    }
  }
  assert.equal(count, 34);
  for (const [key, p] of Object.entries(catalog.permissions)) {
    assert(p.description.en && p.description['zh-CN'], key);
    assert.equal(typeof p.adminOnly, 'boolean');
    assert.equal(typeof p.sensitive, 'boolean');
    assert(p.builtinRoles.length);
  }
  for (const [type, e] of Object.entries(catalog.events)) {
    assert.equal(e.publish, 'auriga.evt.' + type);
    assert(e.source.startsWith('auriga/'));
    const [file, fragment] = e.schema.split('#/$defs/');
    assert(validate(file.replace('../', ''), fragment));
  }
});

test('每个模型的required字段有正式定义，不因patch别名而丢失创建字段', () => {
  for (const service of ['ai', 'resource']) {
    const spec = read('openapi/' + service + '.yaml');
    for (const [name, schema] of Object.entries(spec.components.schemas)) {
      for (const field of schema.required ?? [])
        assert(
          Object.hasOwn(schema.properties ?? {}, field),
          service + '.' + name + '.' + field,
        );
    }
  }
  const ai = read('openapi/ai.yaml');
  assert(ai.components.schemas.ModelInput.properties.id);
  assert(!ai.components.schemas.ModelPatch.properties.id);
});

test('未知用量不是0，事件主体只能instance/device二选一', () => {
  const check = validate('events/usage.schema.json', 'UsageRecorded');
  const e = {
    requestId: uuid,
    accountId: uuid,
    instanceId: uuid,
    modelId: 'example-model',
    outcome: 'unknown',
    inputTokens: null,
    outputTokens: null,
    costMicros: null,
    usageQuality: 'unknown',
    currency: 'USD',
    dayPeriodStart: '2026-10-21T00:00:00Z',
    monthPeriodStart: '2026-10-01T00:00:00Z',
    priceVersion: 'v1',
    revision: 1,
  };
  assert.equal(check(e), true, JSON.stringify(check.errors));
  for (const modify of [
    (e) => (e.inputTokens = 0),
    (e) => delete e.outputTokens,
    (e) => (e.deviceId = uuid),
    (e) => delete e.instanceId,
    (e) => (e.costMicros = -1),
  ]) {
    const t = clone(e);
    modify(t);
    assert.equal(check(t), false);
  }
});

for (const vector of read('resource/signature-vectors.json').cases)
  test('JCS/Ed25519向量 ' + vector.name, () => {
    const check = validate('resource/signatures.schema.json', vector.schema);
    assert.equal(check(vector.document), true, JSON.stringify(check.errors));
    const { signature, ...body } = vector.document;
    const canonical = canon(body);
    assert.equal(canonical, vector.canonical);
    assert.equal(
      createHash('sha256').update(canonical).digest('hex'),
      vector.sha256,
    );
    const key = createPublicKey({
      key: Buffer.from(vector.publicKeySpki, 'base64url'),
      type: 'spki',
      format: 'der',
    });
    const sig = Buffer.from(signature.value, 'base64url');
    assert.equal(verify(null, Buffer.from(canonical), key, sig), true);
    for (const field of Object.keys(body)) {
      const t = clone(body);
      if (typeof t[field] === 'string') t[field] += 'x';
      else if (typeof t[field] === 'number') t[field] -= 1;
      else if (Array.isArray(t[field])) t[field].push('changed');
      else t[field].digest = 'sha256:' + 'b'.repeat(64);
      assert.equal(verify(null, Buffer.from(canon(t)), key, sig), false, field);
    }
    const extra = clone(vector.document);
    extra.unsigned = 'escape';
    assert.equal(check(extra), false);
  });

test('签名schema不接受缺省空数组、错purpose/租户/整数/时间表示', () => {
  const vectors = read('resource/signature-vectors.json').cases;
  const envelope = vectors[0].document;
  const check = validate('resource/signatures.schema.json', 'ResourceEnvelope');
  for (const mutate of [
    (e) => (e.statement.purpose = 'skill'),
    (e) => (e.tenantId = 'not-a-uuid'),
    (e) => (e.releaseSequence = '7'),
    (e) => (e.releaseSequence = 9007199254740992),
    (e) => (e.notAfter = '2026-10-21T08:00:00+00:00'),
    (e) => delete e.statement.arch,
    (e) => (e.signature.value += '='),
  ]) {
    const t = clone(envelope);
    mutate(t);
    assert.equal(check(t), false);
  }
  const rev = clone(vectors[1].document);
  delete rev.revokedKids;
  assert.equal(
    validate('resource/signatures.schema.json', 'RevocationList')(rev),
    false,
  );
});
