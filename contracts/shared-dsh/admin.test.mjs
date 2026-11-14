import test from 'node:test';
import assert from 'node:assert/strict';
import { randomUUID as id } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { parse } from '../../services/contracts/node_modules/yaml/dist/index.js';
import {
  validateAdmin,
  checkPlan,
  checkSnapshot,
  checkProgress,
} from './admin-ports.mjs';
const memberId = id(),
  legacyInstanceId = id();
const request = {
  members: [{ memberId, legacyInstanceId, targetMode: 'dedicated' }],
  reason: 'maintenance',
};
test('K2: plan rejects duplicate members, foreign instances and absent entitlement', () => {
  const members = new Map([[memberId, legacyInstanceId]]);
  checkPlan(request, members, new Set([memberId]));
  assert.throws(() => checkPlan(request, members, new Set()));
  assert.throws(() => checkPlan(request, new Map(), new Set([memberId])));
  assert.throws(() =>
    checkPlan(
      { ...request, members: [...request.members, ...request.members] },
      members,
      new Set([memberId]),
    ),
  );
  for (const patch of [
    { members: [] },
    { owner: id() },
    { members: [{ ...request.members[0], targetMode: 'future' }] },
  ])
    assert.throws(() =>
      validateAdmin('DshRebuildPlanRequest', { ...request, ...patch }),
    );
});
test('K2: destructive start requires explicit export confirmation and frozen policies', () => {
  const start = {
    exportConfirmed: true,
    filePolicy: 'archive-and-reset',
    sessionPolicy: 'discard',
  };
  validateAdmin('DshRebuildStartRequest', start);
  for (const patch of [
    { exportConfirmed: false },
    { filePolicy: 'copy' },
    { sessionPolicy: 'restore' },
    { exportConfirmed: undefined },
  ])
    assert.throws(() =>
      validateAdmin('DshRebuildStartRequest', { ...start, ...patch }),
    );
});
test('K2: authoritative member read may omit an unallocated placement', () => {
  const snapshot = {
    memberId,
    entitlement: { memberId, granted: false, revision: 1 },
  };
  validateAdmin('DshMemberPlacement', snapshot);
  for (const patch of [
    { entitlement: undefined },
    { placement: null },
    { owner: true },
  ])
    assert.throws(() =>
      validateAdmin('DshMemberPlacement', { ...snapshot, ...patch }),
    );
  assert.throws(() =>
    validateAdmin('DshEntitlementRequest', { granted: true }),
  );
  assert.throws(() =>
    validateAdmin('DshMemberPlacement', {
      ...snapshot,
      entitlement: { ...snapshot.entitlement, memberId: id() },
    }),
  );
});
test('K2: progress never completes with unconfirmed stop or mismatched member', () => {
  const plan = { ...request, planId: id(), revision: 1, state: 'running' };
  const row = {
    memberId,
    operationId: id(),
    revision: 1,
    sessionsDiscarded: true,
    workspaceEmpty: true,
    state: 'succeeded',
  };
  const progress = {
    planId: plan.planId,
    revision: 2,
    state: 'completed',
    members: [row],
  };
  checkProgress(progress, plan);
  for (const patch of [
    { sessionsDiscarded: false },
    { workspaceEmpty: false },
    { failureCode: 'capacity' },
    { operationId: undefined },
    { memberId: id() },
    { state: 'blocked' },
  ])
    assert.throws(() =>
      checkProgress({ ...progress, members: [{ ...row, ...patch }] }, plan),
    );
  const blocked = {
    ...row,
    state: 'blocked',
    failureCode: 'stop-unconfirmed',
    sessionsDiscarded: false,
    workspaceEmpty: false,
  };
  checkProgress({ ...progress, state: 'blocked', members: [blocked] }, plan);
  assert.throws(() => checkProgress({ ...progress, members: [blocked] }, plan));
  assert.throws(() => checkProgress({ ...progress, members: [] }, plan));
});
test('K2: capacity freshness, memory boundary and guardian tenant binding', () => {
  const tenantId = id(),
    physicalInstanceId = id();
  const pool = {
    physicalInstanceId,
    registered: 2,
    active: 1,
    registeredLimit: 20,
    activeLimit: 4,
    perMemberActiveLimit: 1,
    memoryUsedBytes: 79,
    memoryLimitBytes: 100,
    admissionMemoryPercent: 80,
    workspaceBytesPerMember: 5368709120,
    workspaceInodesPerMember: 100000,
    accepting: true,
  };
  const binding = {
    tenantId,
    memberId,
    physicalInstanceId,
    placementId: id(),
    mode: 'shared',
    epoch: 1,
    revision: 1,
  };
  const impact = {
    schemaVersion: 1,
    tenantId,
    physicalInstanceId,
    operationId: id(),
    reason: 'cancel-timeout',
    scope: 'pool',
    affected: [binding],
    state: 'blocked',
    observedAt: 100,
  };
  const snapshot = {
    observedAt: 100,
    validUntil: 110,
    pools: [pool],
    impacts: [impact],
  };
  checkSnapshot(snapshot, tenantId, 109);
  assert.throws(() => checkSnapshot(snapshot, tenantId, 110));
  assert.throws(() => checkSnapshot(snapshot, tenantId, 99));
  assert.throws(() => checkSnapshot(snapshot, id(), 100));
  for (const patch of [
    { memoryUsedBytes: 80 },
    { active: 4 },
    { registered: 20 },
    { memoryUsedBytes: Number.MAX_SAFE_INTEGER + 1 },
    { activeLimit: 5 },
  ])
    assert.throws(() =>
      checkSnapshot(
        { ...snapshot, pools: [{ ...pool, ...patch }] },
        tenantId,
        100,
      ),
    );
  assert.throws(() =>
    checkSnapshot(
      {
        ...snapshot,
        impacts: [{ ...impact, affected: [{ ...binding, tenantId: id() }] }],
      },
      tenantId,
      100,
    ),
  );
});
test('K2: every new route is exposed with admin permissions and write step-up', () => {
  const admin = parse(
    readFileSync(
      new URL('../openapi/shared-dsh-admin.yaml', import.meta.url),
      'utf8',
    ),
  );
  const workspace = parse(
    readFileSync(new URL('../openapi/workspace.yaml', import.meta.url), 'utf8'),
  );
  for (const [path, methods] of Object.entries(admin.paths)) {
    assert(workspace.paths[path]);
    for (const [method, op] of Object.entries(methods)) {
      assert.equal(op['x-admin-only'], true);
      assert.equal(
        op['x-permission'],
        'workspace.placements.' + (method === 'get' ? 'read' : 'write'),
      );
      if (method !== 'get') assert.equal(op['x-step-up-max-age-seconds'], 600);
      assert(op.responses['404']);
    }
  }
  const lookup =
    admin.paths['/admin/members/{memberId}/placement-switches/lookup'].get;
  assert(
    lookup.parameters.some(
      (p) => p.name === 'Idempotency-Key' && p.in === 'header',
    ),
  );
  assert(!lookup.parameters.some((p) => p.in === 'query'));
});
