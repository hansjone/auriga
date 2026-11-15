import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { spawnSync } from 'node:child_process';
import { randomUUID as id } from 'node:crypto';
import { parse } from '../../services/contracts/node_modules/yaml/dist/index.js';
import { checkEvent, checkMemory, checkSwitchProbe } from './k3-ports.mjs';
const read = (p) =>
  JSON.parse(readFileSync(new URL(p, import.meta.url), 'utf8'));
const binding = {
  tenantId: id(),
  memberId: id(),
  placementId: id(),
  physicalInstanceId: id(),
  mode: 'shared',
  epoch: 2,
  revision: 3,
};
test('K3: R five receipt goldens run in check-schemas, missing script fails', () => {
  const data = read(
    '../../infra/agent/internal/runtime/testdata/shared-pool-receipts-v1.json',
  );
  assert.deepEqual(data.cases.map((c) => c.intent.action).sort(), [
    'archive-reset',
    'cooperative-cancel',
    'discard-sessions',
    'prepare-empty',
    'stop-pool',
  ]);
  const result = spawnSync(
    process.execPath,
    [
      fileURLToPath(
        new URL(
          '../../infra/agent/internal/runtime/testdata/validate-shared-pool-receipts.mjs',
          import.meta.url,
        ),
      ),
    ],
    { encoding: 'utf8' },
  );
  assert.equal(result.status, 0, result.stderr);
  assert.match(result.stdout, /five receipt goldens confirmed/);
});
test('K3: all nine admin operations have read/write/CAS-specific responses', () => {
  const api = parse(
    readFileSync(
      new URL('../openapi/shared-dsh-admin.yaml', import.meta.url),
      'utf8',
    ),
  );
  let count = 0;
  for (const methods of Object.values(api.paths))
    for (const [method, op] of Object.entries(methods)) {
      count++;
      const cas = op.parameters.some((p) => p.name === 'If-Match');
      assert.deepEqual(
        Object.keys(op.responses).sort(),
        [
          op.operationId === 'startDshRebuildPlan' ? '202' : '200',
          '400',
          '401',
          '403',
          '404',
          ...(method === 'get' ? [] : ['409']),
          ...(cas ? ['412', '428'] : []),
          '503',
        ].sort(),
      );
    }
  assert.equal(count, 9);
});
test('K3: five CloudEvents use frozen data schemas and exact subject/tenant/publisher', () => {
  const rows = [
    [
      'placement.switched',
      {
        operationId: id(),
        memberId: binding.memberId,
        placementId: binding.placementId,
        fromMode: 'dedicated',
        targetMode: 'shared',
        epoch: 2,
        revision: 3,
        sessionsDiscarded: true,
        actorId: id(),
      },
    ],
    [
      'placement.switch_failed',
      {
        operationId: id(),
        memberId: binding.memberId,
        placementId: binding.placementId,
        fromMode: 'dedicated',
        targetMode: 'shared',
        epoch: 2,
        revision: 3,
        sessionsDiscarded: false,
        actorId: id(),
        failureCode: 'capacity',
      },
    ],
    [
      'placement.revoked',
      {
        schemaVersion: 1,
        tenantId: binding.tenantId,
        memberId: binding.memberId,
        placementId: binding.placementId,
        epoch: 3,
        authGeneration: 2,
        revision: 4,
        reason: 'switch',
        revokedAt: 1000,
      },
    ],
    [
      'placement.resource_reported',
      {
        schemaVersion: 1,
        binding,
        viewId: id(),
        status: 'ready',
        reason: 'verified',
        observedAt: 1000,
      },
    ],
    [
      'placement.guardian_impact',
      {
        schemaVersion: 1,
        tenantId: binding.tenantId,
        physicalInstanceId: binding.physicalInstanceId,
        operationId: id(),
        reason: 'crash',
        scope: 'pool',
        affected: [binding],
        state: 'blocked',
        observedAt: 1000,
      },
    ],
  ];
  const catalog = read('../catalog/m3.json');
  for (const [type, data] of rows) {
    const event = {
      specversion: '1.0',
      id: id(),
      source: 'auriga/workspace',
      type,
      time: '2026-11-15T00:00:00Z',
      subject:
        type === 'placement.guardian_impact'
          ? binding.physicalInstanceId
          : binding.placementId,
      datacontenttype: 'application/json',
      dataversion: '1',
      tenantid: binding.tenantId,
      data,
    };
    checkEvent(event, 'workspace');
    assert.equal(catalog.events[type].publish, 'auriga.evt.' + type);
    for (const patch of [
      { source: 'auriga/agent' },
      { dataversion: '2' },
      { subject: id() },
      { data: { ...data, token: 'forbidden' } },
      { type: 'placement.unknown' },
    ])
      assert.throws(() => checkEvent({ ...event, ...patch }, 'workspace'));
    assert.throws(() => checkEvent(event, 'agent'));
    if (data.tenantId || data.binding)
      assert.throws(() =>
        checkEvent({ ...event, tenantid: id() }, 'workspace'),
      );
  }
});
test('K3: node memory requires current assignment, fresh exact-byte sample and no rewind', () => {
  const sample = {
    schemaVersion: 1,
    tenantId: binding.tenantId,
    physicalInstanceId: binding.physicalInstanceId,
    observedGeneration: 2,
    observedAt: 1000,
    validUntil: 1030,
    memoryUsedBytes: 80,
    memoryLimitBytes: 100,
  };
  const authority = {
    authenticated: true,
    ...binding,
    observedGeneration: 2,
    receivedAt: 1000,
    lastObservedAt: 999,
  };
  assert.deepEqual(checkMemory(sample, authority, 1029), {
    memoryUsedBytes: 80,
    memoryLimitBytes: 100,
  });
  for (const patch of [
    { observedGeneration: 1 },
    { tenantId: id() },
    { physicalInstanceId: id() },
    { observedAt: 998 },
    { validUntil: 1031 },
    { memoryLimitBytes: 0 },
    { memoryUsedBytes: '80' },
  ])
    assert.throws(() => checkMemory({ ...sample, ...patch }, authority, 1000));
  for (const now of [999, 1030])
    assert.throws(() => checkMemory(sample, authority, now));
  assert.throws(() =>
    checkMemory(sample, { ...authority, authenticated: false }, 1000),
  );
});
test('K3: internal switch probe requires fenced candidate and completed prerequisites', () => {
  const operation = {
    operationId: id(),
    binding,
    platformSessionId: id(),
    placementState: 'switching',
    state: 'committing',
    receiptsConfirmed: true,
    resourcesReady: true,
  };
  const probe = {
    operationId: operation.operationId,
    binding,
    platformSessionId: operation.platformSessionId,
    internalAuthenticated: true,
    memberActive: true,
    platformSessionActive: true,
    method: 'GET',
    path: '/',
    normalEntry: true,
    freshChallenge: true,
    success: true,
    observedAt: 1000,
  };
  checkSwitchProbe(probe, operation, 1059);
  checkSwitchProbe(probe, { ...operation, placementState: 'pending' }, 1000);
  for (const patch of [
    { internalAuthenticated: false },
    { operationId: id() },
    { binding: { ...binding, epoch: 1 } },
    { method: 'POST' },
    { path: '/health' },
    { platformSessionActive: false },
    { freshChallenge: false },
    { success: false },
  ])
    assert.throws(() =>
      checkSwitchProbe({ ...probe, ...patch }, operation, 1000),
    );
  for (const patch of [
    { placementState: 'ready' },
    { state: 'draining' },
    { receiptsConfirmed: false },
    { resourcesReady: false },
  ])
    assert.throws(() =>
      checkSwitchProbe(probe, { ...operation, ...patch }, 1000),
    );
  assert.throws(() => checkSwitchProbe(probe, operation, 1060));
});
