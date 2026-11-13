import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { execFileSync } from 'node:child_process';
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

test('精确基线的 workspace 契约只增路径，旧定义完整保留', () => {
  const root = new URL('../../', import.meta.url);
  const before = parse(
    execFileSync(
      'git',
      [
        'show',
        '270ed0d8eca1c8c34e30d21207e6bff304a27b91:contracts/openapi/workspace.yaml',
      ],
      { cwd: root, encoding: 'utf8' },
    ),
  );
  const after = parse(
    readFileSync(new URL('../openapi/workspace.yaml', import.meta.url), 'utf8'),
  );
  for (const [key, value] of Object.entries(before)) {
    if (key !== 'paths') assert.deepEqual(after[key], value);
  }
  for (const [key, value] of Object.entries(before.paths))
    assert.deepEqual(after.paths[key], value);
  assert.equal(
    Object.keys(after.paths).length,
    Object.keys(before.paths).length + 3,
  );
});
