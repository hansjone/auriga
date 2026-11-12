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

test('公开模型能力接受大容量与安全整数，缺失保持未知，非法数值拒绝', () => {
  const models = ajv.getSchema(aiUri + '#/$defs/ModelList');
  const entry = { id: 'auto', object: 'model', created: 0, owned_by: 'auriga' };
  const response = (extra) => ({
    object: 'list',
    data: [{ ...entry, ...extra }],
  });
  for (const extra of [
    {},
    { context_window: 4000000, max_output_tokens: 256000 },
    {
      context_window: Number.MAX_SAFE_INTEGER,
      max_output_tokens: Number.MAX_SAFE_INTEGER - 1,
    },
  ])
    assert.equal(models(response(extra)), true, JSON.stringify(models.errors));
  for (const extra of [
    { context_window: 1 },
    { context_window: 1.5 },
    { context_window: Number.MAX_SAFE_INTEGER + 1 },
    { max_output_tokens: 0 },
    { max_output_tokens: 1.5 },
    { max_output_tokens: Number.MAX_SAFE_INTEGER + 1 },
  ])
    assert.equal(models(response(extra)), false, JSON.stringify(extra));
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
test('IAM network administration has one authoritative security declaration', () => {
  const permission = catalog.permissions['iam.network.manage'];
  assert.equal(permission.adminOnly, true);
  assert.equal(permission.sensitive, true);
  assert.deepEqual([...permission.builtinRoles].sort(), ['admin', 'owner']);
});
const apiSpecs = Object.fromEntries(
  ['ai', 'resource'].map((service) => [
    service,
    read('openapi/' + service + '.yaml'),
  ]),
);
function operations(specs) {
  return Object.entries(specs).flatMap(([service, spec]) =>
    Object.entries(spec.paths).flatMap(([url, methods]) =>
      Object.entries(methods).map(([method, operation]) => ({
        service,
        url,
        method,
        operation,
      })),
    ),
  );
}
function sensitiveAdminOperations(specs) {
  return operations(specs).filter(({ operation }) => {
    const permission = catalog.permissions[operation['x-permission']];
    return permission?.adminOnly && permission.sensitive;
  });
}
function assertSensitiveAdminStepup(specs) {
  const sensitive = sensitiveAdminOperations(specs);
  for (const { operation } of sensitive) {
    assert.equal(
      operation['x-step-up-max-age-seconds'],
      600,
      operation.operationId,
    );
    assert(
      operation['x-error-codes']?.includes('iam.step_up_required'),
      operation.operationId + ' must declare iam.step_up_required',
    );
  }
  return sensitive;
}
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
          assert.equal(
            Boolean(o['x-admin-only']),
            p.adminOnly || (service === 'resource' && url.includes('/admin/')),
            o.operationId,
          );
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
  assert.equal(count, 74);
  assert(ids.has('listRelayProviderModels'));
  assert.equal(assertSensitiveAdminStepup(apiSpecs).length, 32);
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

test('目录驱动的全部敏感管理操作逐项删除stepup或错误声明都会真失败', () => {
  const sensitive = assertSensitiveAdminStepup(apiSpecs);
  for (const { service, url, method, operation } of sensitive) {
    for (const missing of ['stepup', 'error']) {
      const changed = clone(apiSpecs);
      const variant = changed[service].paths[url][method];
      if (missing === 'stepup') delete variant['x-step-up-max-age-seconds'];
      else
        variant['x-error-codes'] = variant['x-error-codes'].filter(
          (code) => code !== 'iam.step_up_required',
        );
      assert.throws(
        () => assertSensitiveAdminStepup(changed),
        (error) =>
          error.code === 'ERR_ASSERTION' &&
          error.message.includes(operation.operationId),
        operation.operationId + ' missing ' + missing,
      );
    }
  }
});

const instanceRouteUri = new URL(
  apiSpecs.ai['x-instance-channel'].routeTable,
  aiUri,
);
const instanceRoutes = JSON.parse(readFileSync(instanceRouteUri, 'utf8'));
const instancePermissions = read('auth/access-token.schema.json').allOf.find(
  (rule) => rule.if?.properties?.ctx?.const === 'instance',
).then.properties.perms.items.enum;
function acceptsInstanceAi(spec, operation) {
  return (operation.security ?? spec.security ?? []).some((requirement) =>
    Object.hasOwn(requirement, 'aiDataAuth'),
  );
}
function assertInstanceRoutes(table, spec = apiSpecs.ai) {
  const check = validate('ai/instance-routes.schema.json');
  assert.equal(check(table), true, JSON.stringify(check.errors));
  const eligible = operations({ ai: spec }).filter(({ operation }) =>
    acceptsInstanceAi(spec, operation),
  );
  assert(eligible.length > 0, 'instance operations must be enumerated');
  const sourceKeys = new Set();
  const targetIds = new Set();
  for (const route of table.routes) {
    const sourceKey = route.method + ' ' + route.internalPath;
    assert(!sourceKeys.has(sourceKey), 'duplicate internal method/path');
    sourceKeys.add(sourceKey);
    assert(
      !targetIds.has(route.operationId),
      'duplicate destination operation',
    );
    targetIds.add(route.operationId);
    for (const rawPath of [route.internalPath, route.gatewayPath]) {
      assert(!rawPath.includes('//'), 'no ambiguous repeated slash');
      assert(!rawPath.split('/').includes('admin'), 'no management paths');
    }
    const target = spec.paths[route.gatewayPath]?.[route.method.toLowerCase()];
    assert(target, 'destination method/path must exist');
    assert.equal(
      target.operationId,
      route.operationId,
      'destination operation must match',
    );
    assert(
      acceptsInstanceAi(spec, target),
      'destination must accept aiDataAuth',
    );
    const permission = catalog.permissions[target['x-permission']];
    assert(
      permission && !permission.adminOnly && !target['x-admin-only'],
      'member scope only',
    );
    assert(
      instancePermissions.includes(target['x-permission']),
      'instance own permissions only',
    );
  }
  assert.equal(
    table.routes.length,
    eligible.length,
    'every instance operation has exactly one route',
  );
  for (const { operation } of eligible)
    assert(targetIds.has(operation.operationId), operation.operationId);
  return eligible;
}
// A contract-table lookup for positive/negative fixtures, not an implementation of edge routing.
function declaredInstanceRoute(table, method, rawPath) {
  return table.routes.find(
    (route) => route.method === method && route.internalPath === rawPath,
  );
}
test('实例AI允许表覆盖全部aiDataAuth操作，本人用量精确可达且无管理路径', () => {
  const eligible = assertInstanceRoutes(instanceRoutes);
  assert.equal(
    apiSpecs.ai['x-instance-channel'].publicEntry,
    'reject-ctx-instance',
  );
  assert.equal(
    apiSpecs.ai['x-instance-channel'].gatewayPeer,
    'trusted-edge-mtls',
  );
  for (const route of instanceRoutes.routes) {
    assert.equal(
      declaredInstanceRoute(instanceRoutes, route.method, route.internalPath),
      route,
    );
  }
  const usage = eligible.find(
    ({ operation }) => operation.operationId === 'listOwnAiUsage',
  );
  assert(usage, 'instance own usage must remain declared');
  const route = instanceRoutes.routes.find(
    (item) => item.operationId === usage.operation.operationId,
  );
  assert.equal(route.method, usage.method.toUpperCase());
  assert.equal(route.gatewayPath, usage.url);
  assert.equal(usage.operation['x-permission'], 'ai.usage.read');
});

test('实例允许表逐项拒绝错方法、未列路径和编码歧义，不做前缀或管理转发', () => {
  assertInstanceRoutes(instanceRoutes);
  for (const route of instanceRoutes.routes) {
    for (const method of [
      route.method === 'GET' ? 'POST' : 'GET',
      'HEAD',
      'OPTIONS',
      route.method.toLowerCase(),
    ]) {
      assert.equal(
        declaredInstanceRoute(instanceRoutes, method, route.internalPath),
        undefined,
      );
    }
    for (const rawPath of [
      route.internalPath + '/',
      route.internalPath.replace('/internal/', '/internal//'),
      route.internalPath.replace('/ai/', '/%61i/'),
      route.internalPath + '%2f',
      route.internalPath.replace('/v1/', '/v1/./'),
      route.internalPath.replace('/v1/', '/v1/../'),
      route.internalPath.replaceAll('/', '\\'),
      'https://example.com' + route.internalPath,
      '/internal/ai/v1/admin/providers',
      '/api/ai/v1/admin/usage',
      route.gatewayPath,
    ])
      assert.equal(
        declaredInstanceRoute(instanceRoutes, route.method, rawPath),
        undefined,
        rawPath,
      );
  }
});

test('路由表或OpenAPI的错目标、管理扩权、缺项和重复项变体都拒绝', () => {
  const admin = operations({ ai: apiSpecs.ai }).find(
    ({ operation }) => operation['x-admin-only'],
  );
  for (const mutate of [
    (table) =>
      (table.routes[0].method =
        table.routes[0].method === 'GET' ? 'POST' : 'GET'),
    (table) => (table.routes[0].gatewayPath += '/missing'),
    (table) => {
      table.routes[0].gatewayPath = admin.url;
      table.routes[0].method = admin.method.toUpperCase();
      table.routes[0].operationId = admin.operation.operationId;
    },
    (table) =>
      (table.routes.find(
        (route) => route.operationId === 'listOwnAiUsage',
      ).gatewayPath = '/ai/v1/me/usage'),
    (table) => (table.routes[0].operationId += 'Missing'),
    (table) => (table.routes[0].internalPath = '/internal/ai/v1/%6dodels'),
    (table) => (table.routes[0].internalPath = '/internal/ai/v1/admin/models'),
    (table) => (table.routes[0].internalPath = '/internal/ai/v1//models'),
    (table) => (table.routes[0].gatewayPath = '/api/iam/v1/me'),
    (table) => table.routes.pop(),
    (table) => table.routes.push(clone(table.routes[0])),
    (table) => (table.matching = 'prefix-fallback'),
  ]) {
    const changed = clone(instanceRoutes);
    mutate(changed);
    assert.throws(() => assertInstanceRoutes(changed), {
      code: 'ERR_ASSERTION',
    });
  }
  const added = clone(apiSpecs.ai);
  added.paths[admin.url][admin.method].security = [{ aiDataAuth: [] }];
  assert.throws(() => assertInstanceRoutes(instanceRoutes, added), {
    code: 'ERR_ASSERTION',
  });
  const elevated = clone(apiSpecs.ai);
  const target = instanceRoutes.routes[0];
  elevated.paths[target.gatewayPath][target.method.toLowerCase()][
    'x-permission'
  ] = 'ai.usage.admin';
  assert.throws(() => assertInstanceRoutes(instanceRoutes, elevated), {
    code: 'ERR_ASSERTION',
  });
  const removed = clone(apiSpecs.ai);
  removed.paths[target.gatewayPath][target.method.toLowerCase()].security = [
    { bearerAuth: [] },
  ];
  assert.throws(() => assertInstanceRoutes(instanceRoutes, removed), {
    code: 'ERR_ASSERTION',
  });
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
const unifiedUri = pathToFileURL(path.join(root, 'openapi/resource.yaml')).href;
ajv.addSchema({
  $id: unifiedUri,
  $defs: schemaRefs(read('openapi/resource.yaml').components.schemas),
});
const unifiedSchema = 'resource/package.schema.json';
const artifactDigest = 'sha256:' + 'a'.repeat(64);
function resourceManifest(kind) {
  const common = {
    schemaVersion: 1,
    name: 'network-guide',
    version: '1.0.0',
    description: 'Network guide',
    author: { displayName: 'Example author' },
    license: 'MIT',
    permissions: { tools: [], outboundHosts: [], dataAccess: [] },
    runtimes: [],
    artifacts: [
      {
        id: 'payload',
        role: 'payload',
        digest: artifactDigest,
        sizeBytes: 1024,
        mediaType: 'application/gzip',
        os: 'any',
        arch: 'any',
      },
    ],
    signature: { mode: 'detached', algorithm: 'Ed25519' },
    kind,
  };
  if (kind === 'skill')
    common.skill = {
      entry: 'SKILL.md',
      contentProfile: 'text-only',
      dshVersionRange: '>=0.2.1-rc.3',
    };
  if (kind === 'plugin')
    common.plugin = {
      packageName: '@example/plugin',
      dshVersionRange: '>=0.2.1-rc.3',
    };
  if (kind === 'tool')
    common.tool = {
      delivery: 'offline',
      entrypoints: [{ artifactId: 'payload', path: 'bin/tool.exe', args: [] }],
    };
  return common;
}

test('通用草案按kind创建共享资源，拒绝私有上传与客户端指定身份', () => {
  const check = ajv.getSchema(unifiedUri + '#/$defs/UnifiedPackageInput');
  for (const kind of ['plugin', 'skill', 'tool'])
    assert.equal(
      check({ name: 'network-guide', kind, visibility: 'members' }),
      true,
      JSON.stringify(check.errors),
    );
  const body = { name: 'network-guide', kind: 'skill', visibility: 'members' };
  for (const extra of [
    { kind: 'agent-preset' },
    { visibility: 'private' },
    { name: '../escape' },
    { ownerId: uuid },
    { tenantId: uuid },
    { level: 'l1' },
  ])
    assert.equal(check({ ...body, ...extra }), false, JSON.stringify(extra));
  const manifest = validate(unifiedSchema, 'Manifest');
  for (const kind of ['plugin', 'skill', 'tool'])
    assert.equal(
      manifest(resourceManifest(kind)),
      true,
      JSON.stringify(manifest.errors),
    );
  for (const mutate of [
    (v) => (v.skill.contentProfile = 'code'),
    (v) => (v.skill.scripts = ['run.sh']),
    (v) => (v.schemaVersion = 2),
    (v) => (v.version = '01.0.0'),
    (v) => (v.plugin = { packageName: 'other', dshVersionRange: '*' }),
    (v) => (v.artifacts[0].os = 'windows'),
    (v) => (v.artifacts[0].sizeBytes = 5242881),
    (v) => v.runtimes.push({ kind: 'node', versionRange: '*' }),
  ]) {
    const invalid = resourceManifest('skill');
    mutate(invalid);
    assert.equal(manifest(invalid), false);
  }
});

test('通用v2与plugin v1签名结构相互拒绝，未知字段和purpose拒绝', () => {
  const check = validate(unifiedSchema, 'Envelope');
  const plugin = read('resource/signature-vectors.json').cases[0].document;
  const value = clone(plugin);
  value.schemaVersion = 2;
  value.statement = {
    purpose: 'resource',
    kind: 'skill',
    name: 'network-guide',
    version: '1.0.0',
    manifestDigest: artifactDigest,
    artifactDigests: [artifactDigest],
    issuedAt: plugin.statement.issuedAt,
  };
  assert.equal(check(value), true, JSON.stringify(check.errors));
  assert.equal(check(plugin), false);
  assert.equal(
    validate('resource/signatures.schema.json', 'ResourceEnvelope')(value),
    false,
  );
  for (const mutate of [
    (v) => (v.statement.purpose = 'plugin'),
    (v) => delete v.statement.manifestDigest,
    (v) => (v.statement.manifestDigest = 'sha256:INVALID'),
    (v) => (v.unknown = true),
    (v) => (v.statement.script = 'run.sh'),
    (v) => (v.schemaVersion = 1),
    (v) => v.statement.artifactDigests.push(artifactDigest),
  ]) {
    const invalid = clone(value);
    mutate(invalid);
    assert.equal(check(invalid), false);
  }
});

test('在线和Git工具保留不同制品与runtime要求，源码禁止启用安装脚本', () => {
  const check = validate(unifiedSchema, 'Manifest');
  const online = resourceManifest('tool');
  online.artifacts = [];
  online.tool = {
    delivery: 'online',
    protocol: 'mcp',
    url: 'https://tools.example.com/mcp',
    authentication: 'member_oauth',
  };
  assert.equal(check(online), true, JSON.stringify(check.errors));
  const ipHttp = clone(online);
  ipHttp.tool.protocol = 'web';
  ipHttp.tool.url = 'http://192.0.2.10:8080/tool';
  assert.equal(check(ipHttp), true, JSON.stringify(check.errors));
  for (const mutate of [
    (v) => v.artifacts.push(resourceManifest('skill').artifacts[0]),
    (v) => (v.tool.url = 'file:///tmp/tool'),
    (v) => (v.tool.entrypoints = []),
    (v) => v.runtimes.push({ kind: 'python', versionRange: '>=3.12' }),
  ]) {
    const invalid = clone(online);
    mutate(invalid);
    assert.equal(check(invalid), false);
  }
  const git = resourceManifest('tool');
  git.runtimes = [{ kind: 'node', versionRange: '>=24 <25' }];
  git.artifacts = [
    { ...git.artifacts[0], id: 'source', role: 'source_snapshot' },
    {
      ...git.artifacts[0],
      id: 'lock',
      role: 'lockfile',
      mediaType: 'application/json',
    },
  ];
  git.tool = {
    delivery: 'git',
    repository: 'https://git.example.com/tools/example.git',
    commit: 'a'.repeat(40),
    sourceArtifactId: 'source',
    lockArtifactIds: ['lock'],
    packageManager: 'npm',
    installScripts: false,
    entrypoint: { artifactId: 'source', path: 'src/main.js', args: [] },
  };
  assert.equal(check(git), true, JSON.stringify(check.errors));
  for (const mutate of [
    (v) => (v.tool.commit = 'main'),
    ...[true, 'false', 'true', 0, 1, null, {}, []].map(
      (value) => (v) => (v.tool.installScripts = value),
    ),
    (v) => delete v.tool.installScripts,
    (v) => (v.tool.lockArtifactIds = []),
    (v) => (v.runtimes = []),
    (v) => (v.tool.entrypoint.path = '../outside.js'),
    (v) => (v.tool.entrypoint.path = 'C:\\outside.js'),
    (v) => (v.artifacts[0].digest = 'sha256:INVALID'),
  ]) {
    const invalid = clone(git);
    mutate(invalid);
    assert.equal(check(invalid), false);
  }
});

test('通用路径替换本轮Skill路径，查询按kind筛选且上传保留原始清单字节', () => {
  const spec = read('openapi/resource.yaml');
  assert(!Object.keys(spec.paths).some((p) => p.includes('/skills/')));
  const paths = Object.entries(spec.paths).filter(([p]) =>
    p.includes('/catalog/'),
  );
  assert.equal(paths.length, 10);
  for (const [path, methods] of paths) {
    for (const [method, operation] of Object.entries(methods)) {
      assert(operation.operationId.startsWith('Unified'));
      assert.equal(operation['x-auriga-contract-stage'], 'draft');
      if (method === 'get' && !path.includes('{')) {
        const kind = operation.parameters.find((p) => p.name === 'kind');
        assert.deepEqual(kind.schema.enum, ['plugin', 'skill', 'tool']);
      }
    }
  }
  const upload =
    spec.paths['/api/resource/v1/catalog/packages/{packageId}/versions'].post;
  const multipart = upload.requestBody.content['multipart/form-data'].schema;
  assert.equal(multipart.properties.manifest.format, 'binary');
  assert.equal(multipart.properties.artifacts.type, 'array');
});
