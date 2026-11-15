// Additive K2 browser contracts. Existing v1 paths and payloads are unchanged.
import { readFileSync, writeFileSync } from 'node:fs';
import { parse } from '../../services/contracts/node_modules/yaml/dist/index.js';
const read = (p) => readFileSync(new URL(p, import.meta.url), 'utf8');
const id = { type: 'string', format: 'uuid' };
const integer = (minimum = 0) => ({
  type: 'integer',
  minimum,
  maximum: Number.MAX_SAFE_INTEGER,
});
const text = { type: 'string', minLength: 1, maxLength: 512 };
const enumeration = (...values) => ({ type: 'string', enum: values });
const ref = (name) => ({ $ref: '#/$defs/' + name });
const array = (name) => ({ type: 'array', items: ref(name) });
const object = (properties, optional = []) => ({
  type: 'object',
  additionalProperties: false,
  required: Object.keys(properties).filter((k) => !optional.includes(k)),
  properties,
});
const old = parse(read('../openapi/shared-dsh.yaml'));
const wire = JSON.parse(read('shared-dsh.schema.json'));
const convert = (value) =>
  JSON.parse(
    JSON.stringify(value, (k, v) =>
      k === 'format' && v === 'int64'
        ? undefined
        : v && v.type === 'integer'
          ? { ...v, maximum: Number.MAX_SAFE_INTEGER }
          : v,
    ).replaceAll('#/components/schemas/', '#/$defs/'),
  );
const defs = {
  DshInstanceMode: old.components.schemas.DshInstanceMode,
  DshPlacement: convert(old.components.schemas.DshPlacement),
  DshPlacementSwitch: convert(old.components.schemas.DshPlacementSwitch),
  Binding: wire.$defs.Binding,
  GuardianImpact: wire.$defs.GuardianImpact,
  DshDedicatedEntitlement: object({
    memberId: id,
    granted: { type: 'boolean' },
    revision: integer(1),
  }),
  DshMemberPlacement: object(
    {
      memberId: id,
      entitlement: ref('DshDedicatedEntitlement'),
      placement: ref('DshPlacement'),
    },
    ['placement'],
  ),
  DshEntitlementRequest: object({ granted: { type: 'boolean' }, reason: text }),
  DshRebuildMember: object({
    memberId: id,
    legacyInstanceId: id,
    targetMode: ref('DshInstanceMode'),
  }),
  DshRebuildPlanRequest: object({
    members: { ...array('DshRebuildMember'), minItems: 1, maxItems: 1000 },
    reason: text,
  }),
  DshRebuildPlan: object({
    planId: id,
    revision: integer(1),
    state: enumeration('draft', 'running', 'completed', 'blocked'),
    members: { ...array('DshRebuildMember'), minItems: 1, maxItems: 1000 },
    reason: text,
  }),
  DshRebuildStartRequest: object({
    exportConfirmed: { type: 'boolean', const: true },
    sessionPolicy: enumeration('discard'),
    filePolicy: enumeration('archive-and-reset'),
  }),
  DshRebuildMemberProgress: object(
    {
      memberId: id,
      operationId: id,
      revision: integer(1),
      sessionsDiscarded: { type: 'boolean' },
      workspaceEmpty: { type: 'boolean' },
      state: enumeration(
        'accepted',
        'fencing',
        'draining',
        'preparing',
        'committing',
        'succeeded',
        'failed',
        'blocked',
      ),
      failureCode: enumeration(
        'capacity',
        'stop-unconfirmed',
        'prepare-failed',
        'revoked',
        'recovery-required',
      ),
    },
    ['operationId', 'failureCode'],
  ),
  DshRebuildProgress: object({
    planId: id,
    revision: integer(1),
    state: enumeration('running', 'completed', 'blocked'),
    members: array('DshRebuildMemberProgress'),
  }),
  DshPoolCapacity: object({
    physicalInstanceId: id,
    registered: integer(),
    active: integer(),
    registeredLimit: { type: 'integer', const: 20 },
    activeLimit: { type: 'integer', const: 4 },
    perMemberActiveLimit: { type: 'integer', const: 1 },
    memoryUsedBytes: integer(),
    memoryLimitBytes: integer(1),
    admissionMemoryPercent: { type: 'integer', const: 80 },
    workspaceBytesPerMember: { type: 'integer', const: 5368709120 },
    workspaceInodesPerMember: { type: 'integer', const: 100000 },
    accepting: { type: 'boolean' },
  }),
  DshCapacitySnapshot: object({
    observedAt: integer(),
    validUntil: integer(),
    pools: array('DshPoolCapacity'),
    impacts: array('GuardianImpact'),
  }),
};
defs.DshRebuildMemberProgress.allOf = [
  {
    if: {
      properties: { state: { enum: ['failed', 'blocked'] } },
      required: ['state'],
    },
    then: {
      properties: { failureCode: { type: 'string' } },
      required: ['failureCode'],
    },
  },
  {
    if: { properties: { state: { const: 'succeeded' } }, required: ['state'] },
    then: {
      required: ['operationId'],
      properties: {
        operationId: id,
        failureCode: false,
        sessionsDiscarded: { const: true },
        workspaceEmpty: { const: true },
      },
    },
  },
];
const schemas = JSON.parse(
  JSON.stringify(defs).replaceAll('#/$defs/', '#/components/schemas/'),
);
// Reuse frozen public DTOs rather than creating competing generated names.
for (const name of ['DshPlacement', 'DshPlacementSwitch', 'DshInstanceMode']) {
  delete schemas[name];
}
const publicSchemas = JSON.parse(
  JSON.stringify(schemas).replaceAll(
    /"#\/components\/schemas\/(DshPlacement|DshPlacementSwitch|DshInstanceMode)"/g,
    '"shared-dsh.yaml#/components/schemas/$1"',
  ),
);
// Explicit width is necessary for Java literals/validation annotations as well
// as protobuf/JSON adapters. Strict schema retains its safe-integer bounds.
function markIntegers(value) {
  if (!value || typeof value !== 'object') return;
  if (value.type === 'integer') value.format = 'int64';
  for (const child of Object.values(value)) markIntegers(child);
}
markIntegers(publicSchemas);
const parameter = (name, location = 'path') => ({
  name,
  in: location,
  required: true,
  schema:
    location === 'path' ? id : { type: 'string', minLength: 1, maxLength: 128 },
});
const response = (name) => ({
  description: '权威快照；仅管理会话，同租户。',
  headers: {
    'Cache-Control': { schema: { type: 'string', const: 'no-store' } },
    ETag: {
      description: '服务端不透明强标签，客户端原样回传，禁止由 revision 拼装。',
      schema: { type: 'string' },
    },
  },
  content: {
    'application/json': { schema: { $ref: '#/components/schemas/' + name } },
  },
});
const operation = (
  operationId,
  summary,
  result,
  parameters = [],
  input,
  accepted = false,
) => ({
  tags: ['placements'],
  operationId,
  summary,
  'x-admin-only': true,
  'x-permission': 'workspace.placements.' + (input ? 'write' : 'read'),
  ...(input ? { 'x-step-up-max-age-seconds': 600 } : {}),
  description:
    '租户来自可信认证上下文；目标对象跨租户或不存在均404。每次重新检查有效管理会话和权限。' +
    (input
      ? '写入还要求 Origin/CSRF 和严格600秒内step-up；未来时间拒绝。幂等键按租户、操作者、操作、目标及完整请求指纹24小时持久化；同键异请求409，先查已有幂等结果再核原If-Match。'
      : ''),
  parameters,
  ...(input
    ? {
        requestBody: {
          required: true,
          content: {
            'application/json': {
              schema: { $ref: '#/components/schemas/' + input },
            },
          },
        },
      }
    : {}),
  responses: {
    [accepted ? '202' : '200']: response(result),
    ...Object.fromEntries(
      ['400', '401', '403', '404', '409', '412', '428', '503'].map((code) => [
        code,
        {
          description: {
            400: 'common.validation_failed',
            401: 'common.unauthenticated',
            403: 'common.forbidden 或 iam.step_up_required',
            404: 'common.not_found',
            409: 'common.conflict 或 shared.stop_unconfirmed',
            412: 'common.precondition_failed',
            428: '缺少 If-Match，common.validation_failed',
            503: 'common.unavailable',
          }[code],
          content: {
            'application/problem+json': {
              schema: { $ref: 'common.yaml#/components/schemas/Problem' },
            },
          },
        },
      ]),
    ),
  },
});
const writes = [
  parameter('If-Match', 'header'),
  parameter('Idempotency-Key', 'header'),
];
const paths = {
  '/admin/members/{memberId}/placement': {
    get: operation(
      'getMemberDshPlacement',
      '读取成员分配及独立授权',
      'DshMemberPlacement',
      [parameter('memberId')],
    ),
  },
  '/admin/members/{memberId}/dedicated-entitlement': {
    put: operation(
      'setMemberDshDedicatedEntitlement',
      '授予或撤销独立授权',
      'DshDedicatedEntitlement',
      [parameter('memberId'), ...writes],
      'DshEntitlementRequest',
    ),
  },
  '/admin/members/{memberId}/placement-switches/lookup': {
    get: operation(
      'findDshPlacementSwitch',
      '按原幂等键恢复切换查询',
      'DshPlacementSwitch',
      [parameter('memberId'), parameter('Idempotency-Key', 'header')],
    ),
  },
  '/admin/placement-rebuild-plans': {
    post: operation(
      'createDshRebuildPlan',
      '持久化维护名单及继续独立选择',
      'DshRebuildPlan',
      [parameter('Idempotency-Key', 'header')],
      'DshRebuildPlanRequest',
    ),
  },
  '/admin/placement-rebuild-plans/{planId}': {
    get: operation('getDshRebuildPlan', '读取维护名单', 'DshRebuildPlan', [
      parameter('planId'),
    ]),
    put: operation(
      'updateDshRebuildPlan',
      '替换尚未执行的维护名单',
      'DshRebuildPlan',
      [parameter('planId'), ...writes],
      'DshRebuildPlanRequest',
    ),
  },
  '/admin/placement-rebuild-plans/{planId}/start': {
    post: operation(
      'startDshRebuildPlan',
      '确认导出窗口后批量重建',
      'DshRebuildProgress',
      [parameter('planId'), ...writes],
      'DshRebuildStartRequest',
      true,
    ),
  },
  '/admin/placement-rebuild-plans/{planId}/progress': {
    get: operation(
      'getDshRebuildProgress',
      '读取逐成员持久化进度',
      'DshRebuildProgress',
      [parameter('planId')],
    ),
  },
  '/admin/placement-capacity': {
    get: operation(
      'getDshPlacementCapacity',
      '轮询实时池容量与全池影响',
      'DshCapacitySnapshot',
    ),
  },
};
// Lookup returns the original switch payload.
paths['/admin/members/{memberId}/placement-switches/lookup'].get.responses[
  '200'
].content['application/json'].schema.$ref =
  'shared-dsh.yaml#/components/schemas/DshPlacementSwitch';
const api = {
  openapi: '3.1.0',
  info: {
    title: 'Auriga shared DSH additive administration',
    version: '1.0.0',
    license: { name: 'AGPL-3.0-only', identifier: 'AGPL-3.0-only' },
  },
  servers: [{ url: '/api/workspace/v1' }],
  security: [{ bearerAuth: [] }],
  tags: [{ name: 'placements', description: '共享 DSH 管理。' }],
  paths,
  components: {
    securitySchemes: old.components.securitySchemes,
    schemas: publicSchemas,
  },
};
const emit = (path, value) => {
  const content = JSON.stringify(value, null, 2) + '\n';
  if (process.argv.includes('--check')) {
    if (read(path) !== content) throw new Error('K2 contract drift: ' + path);
  } else writeFileSync(new URL(path, import.meta.url), content);
};
emit('shared-dsh-admin.schema.json', {
  $schema: 'https://json-schema.org/draft/2020-12/schema',
  $id: 'https://auriga.dev/contracts/shared-dsh/admin/v1',
  $defs: defs,
});
emit('../openapi/shared-dsh-admin.yaml', api);
