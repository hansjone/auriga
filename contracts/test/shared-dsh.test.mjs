import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { parse } from '../../services/contracts/node_modules/yaml/dist/index.js';
import Ajv from '../../services/contracts/node_modules/ajv/dist/2020.js';
import addFormats from '../../services/contracts/node_modules/ajv-formats/dist/index.js';

const ajv = new Ajv({ strict: true, allErrors: true });
addFormats(ajv);
const file = new URL('../events/placement.schema.json', import.meta.url);
ajv.addSchema(JSON.parse(readFileSync(file, 'utf8')), file.href);
const success = ajv.getSchema(file.href + '#/$defs/PlacementSwitched');
const failed = ajv.getSchema(file.href + '#/$defs/PlacementSwitchFailed');
const id = '019319fa-0000-7000-8000-000000000001';
const event = {
  operationId: id,
  memberId: id,
  placementId: id,
  fromMode: 'shared',
  targetMode: 'dedicated',
  epoch: 2,
  revision: 3,
  sessionsDiscarded: true,
  actorId: id,
};

test('成功事件必须丢弃会话且不能携带失败码', () => {
  assert.equal(success(event), true);
  assert.equal(success({ ...event, sessionsDiscarded: false }), false);
  assert.equal(success({ ...event, failureCode: 'capacity' }), false);
});
test('失败事件要求已知分类并保留实际会话处置结果', () => {
  assert.equal(failed(event), false);
  assert.equal(
    failed({ ...event, failureCode: 'capacity', sessionsDiscarded: false }),
    true,
  );
  assert.equal(failed({ ...event, failureCode: 'unknown' }), false);
});
test('拒绝零代次、非法模式、缺少归属和额外凭据字段', () => {
  for (const patch of [
    { epoch: 0 },
    { targetMode: 'root' },
    { memberId: undefined },
    { token: 'must-not-be-an-event-field' },
  ]) {
    assert.equal(success({ ...event, ...patch }), false);
  }
});

test('workspace 引用共享 DSH 的三个公开路径', () => {
  const workspace = parse(
    readFileSync(new URL('../openapi/workspace.yaml', import.meta.url), 'utf8'),
  );
  for (const path of [
    '/me/placement',
    '/admin/members/{memberId}/placement-switches',
    '/admin/placement-switches/{operationId}',
  ]) {
    assert.equal(
      workspace.paths[path].$ref,
      'shared-dsh.yaml#/paths/' + path.replaceAll('/', '~1'),
    );
  }
});

const sharedApi = parse(
  readFileSync(new URL('../openapi/shared-dsh.yaml', import.meta.url), 'utf8'),
);
const catalog = JSON.parse(
  readFileSync(new URL('../catalog/m3.json', import.meta.url), 'utf8'),
);
const createSwitch =
  sharedApi.paths['/admin/members/{memberId}/placement-switches'].post;
const getSwitch =
  sharedApi.paths['/admin/placement-switches/{operationId}'].get;

test('分配管理接口关联管理员权限及敏感写操作的 step-up', () => {
  for (const [operation, permission, sensitive] of [
    [createSwitch, 'workspace.placements.write', true],
    [getSwitch, 'workspace.placements.read', false],
  ]) {
    assert.equal(operation['x-admin-only'], true);
    assert.equal(operation['x-permission'], permission);
    assert.equal(catalog.permissions[permission].adminOnly, true);
    assert.equal(catalog.permissions[permission].sensitive, sensitive);
    assert.deepEqual(catalog.permissions[permission].builtinRoles, [
      'owner',
      'admin',
    ]);
  }
  assert.equal(createSwitch['x-step-up-max-age-seconds'], 600);
  assert(createSwitch['x-error-codes'].includes('iam.step_up_required'));
});

test('冲突和未分配响应均返回公共 Problem', () => {
  const responses = [
    ...['409', '412', '428'].map((status) => createSwitch.responses[status]),
    sharedApi.paths['/me/placement'].get.responses['404'],
  ];
  for (const response of responses) {
    assert.equal(
      response.content['application/problem+json'].schema.$ref,
      'common.yaml#/components/schemas/Problem',
    );
  }
});

test('切换事件目录固定生产方、主题和数据定义', () => {
  assert.deepEqual(createSwitch['x-events'], [
    'placement.switched',
    'placement.switch_failed',
  ]);
  for (const [type, definition] of [
    ['placement.switched', 'PlacementSwitched'],
    ['placement.switch_failed', 'PlacementSwitchFailed'],
  ]) {
    assert.deepEqual(catalog.events[type], {
      source: 'auriga/workspace',
      subject: 'placementId',
      schema: '../events/placement.schema.json#/$defs/' + definition,
      consumers: ['audit'],
      publish: 'auriga.evt.' + type,
    });
  }
});

test('切换结果在 failed/blocked 时要求失败码，成功时要求会话已丢弃', () => {
  const definitions = JSON.parse(
    JSON.stringify(sharedApi.components.schemas).replaceAll(
      '#/components/schemas/',
      '#/$defs/',
    ),
  );
  const validate = ajv.compile({
    $defs: definitions,
    $ref: '#/$defs/DshPlacementSwitch',
  });
  const result = {
    operationId: id,
    memberId: id,
    fromMode: 'shared',
    targetMode: 'dedicated',
    state: 'succeeded',
    revision: 3,
    sessionsDiscarded: true,
  };
  assert.equal(validate(result), true);
  assert.equal(validate({ ...result, failureCode: 'capacity' }), false);
  assert.equal(validate({ ...result, sessionsDiscarded: false }), false);
  for (const state of ['failed', 'blocked']) {
    assert.equal(validate({ ...result, state }), false);
    assert.equal(
      validate({ ...result, state, failureCode: 'stop-unconfirmed' }),
      true,
    );
    assert.equal(validate({ ...result, state, failureCode: 'unknown' }), false);
  }
});
