// Test oracle; implementations must perform these checks with authoritative tenant data.
import Ajv from '../../services/contracts/node_modules/ajv/dist/2020.js';
import formats from '../../services/contracts/node_modules/ajv-formats/dist/index.js';
import { readFileSync } from 'node:fs';
const schema = JSON.parse(
  readFileSync(new URL('shared-dsh-admin.schema.json', import.meta.url)),
);
const ajv = new Ajv({ strict: true, allErrors: true });
formats(ajv);
ajv.addSchema(schema);
export function validateAdmin(name, value) {
  if (!ajv.getSchema(schema.$id + '#/$defs/' + name)(value))
    throw new Error('common.validation_failed');
  if (
    name === 'DshMemberPlacement' &&
    (value.memberId !== value.entitlement.memberId ||
      (value.placement && value.placement.memberId !== value.memberId))
  )
    throw new Error('shared.binding_mismatch');
  return value;
}
export function checkProgress(progress, plan) {
  validateAdmin('DshRebuildProgress', progress);
  validateAdmin('DshRebuildPlan', plan);
  if (
    progress.planId !== plan.planId ||
    progress.members.length !== plan.members.length
  )
    throw new Error('shared.binding_mismatch');
  const operations = new Set();
  for (let i = 0; i < plan.members.length; i++) {
    const row = progress.members[i];
    if (row.memberId !== plan.members[i].memberId)
      throw new Error('shared.binding_mismatch');
    if (row.operationId) {
      if (operations.has(row.operationId)) throw new Error('common.conflict');
      operations.add(row.operationId);
    }
  }
  if (
    progress.state === 'completed' &&
    !progress.members.every((row) => row.state === 'succeeded')
  )
    throw new Error('shared.stop_unconfirmed');
}
export function checkPlan(request, memberInstances, entitled) {
  validateAdmin('DshRebuildPlanRequest', request);
  const seen = new Set();
  const instances = new Set();
  for (const row of request.members) {
    if (seen.has(row.memberId) || instances.has(row.legacyInstanceId))
      throw new Error('common.conflict');
    if (memberInstances.get(row.memberId) !== row.legacyInstanceId)
      throw new Error('common.not_found');
    if (row.targetMode === 'dedicated' && !entitled.has(row.memberId))
      throw new Error('common.forbidden');
    seen.add(row.memberId);
    instances.add(row.legacyInstanceId);
  }
}
export function checkSnapshot(snapshot, tenantId, now) {
  validateAdmin('DshCapacitySnapshot', snapshot);
  if (
    snapshot.observedAt > now ||
    now >= snapshot.validUntil ||
    snapshot.validUntil <= snapshot.observedAt
  )
    throw new Error('common.unavailable');
  const pools = new Set();
  for (const pool of snapshot.pools) {
    if (pools.has(pool.physicalInstanceId)) throw new Error('common.conflict');
    pools.add(pool.physicalInstanceId);
    if (
      pool.accepting &&
      (pool.registered >= 20 ||
        pool.active >= 4 ||
        BigInt(pool.memoryUsedBytes) * 100n >=
          BigInt(pool.memoryLimitBytes) * 80n)
    )
      throw new Error('common.conflict');
  }
  for (const impact of snapshot.impacts) {
    if (impact.tenantId !== tenantId || !pools.has(impact.physicalInstanceId))
      throw new Error('common.not_found');
    for (const binding of impact.affected) {
      if (
        binding.tenantId !== tenantId ||
        binding.physicalInstanceId !== impact.physicalInstanceId
      )
        throw new Error('common.not_found');
    }
  }
}
