// Contract oracles only; production authorities must supply these observations.
import Ajv from '../../services/contracts/node_modules/ajv/dist/2020.js';
import addFormats from '../../services/contracts/node_modules/ajv-formats/dist/index.js';
import { readFileSync } from 'node:fs';
import { validate } from './ports.mjs';
const read = (p) =>
  JSON.parse(readFileSync(new URL(p, import.meta.url), 'utf8'));
const ajv = new Ajv({ strict: true, allErrors: true });
addFormats(ajv);
for (const p of [
  '../events/envelope.schema.json',
  '../events/placement.schema.json',
  'shared-dsh.schema.json',
  'events.schema.json',
])
  ajv.addSchema(read(p), new URL(p, import.meta.url).href);
const eventValidator = ajv.getSchema(
  new URL('events.schema.json', import.meta.url).href,
);
export function checkEvent(event, publisher) {
  if (!eventValidator(event) || publisher !== 'workspace')
    throw new Error('invalid event or publisher');
  const data = event.data;
  const tenant = data.binding?.tenantId ?? data.tenantId;
  const subject =
    event.type === 'placement.guardian_impact'
      ? data.physicalInstanceId
      : (data.binding?.placementId ?? data.placementId);
  if (
    (tenant && tenant !== event.tenantid) ||
    event.subject !== subject ||
    data.affected?.some(
      (b) =>
        b.tenantId !== event.tenantid ||
        b.physicalInstanceId !== data.physicalInstanceId,
    )
  )
    throw new Error('event binding mismatch');
  return event;
}
export function checkMemory(sample, authority, now) {
  validate('PoolMemorySample', sample);
  if (
    !authority.authenticated ||
    sample.tenantId !== authority.tenantId ||
    sample.physicalInstanceId !== authority.physicalInstanceId ||
    sample.observedGeneration !== authority.observedGeneration ||
    sample.observedAt > now ||
    now >= sample.validUntil ||
    sample.validUntil - sample.observedAt > 30 ||
    now - authority.receivedAt >= 30 ||
    authority.receivedAt > now ||
    sample.observedAt < authority.lastObservedAt
  )
    throw new Error('unavailable memory authority');
  return {
    memoryUsedBytes: sample.memoryUsedBytes,
    memoryLimitBytes: sample.memoryLimitBytes,
  };
}
export function checkSwitchProbe(probe, operation, now) {
  validate('Binding', probe.binding);
  if (
    !probe.internalAuthenticated ||
    !probe.memberActive ||
    !probe.platformSessionActive ||
    !['pending', 'switching'].includes(operation.placementState) ||
    operation.state !== 'committing' ||
    probe.operationId !== operation.operationId ||
    Object.keys(probe.binding).some(
      (k) => probe.binding[k] !== operation.binding[k],
    ) ||
    probe.platformSessionId !== operation.platformSessionId ||
    !operation.receiptsConfirmed ||
    !operation.resourcesReady ||
    probe.method !== 'GET' ||
    probe.path !== '/' ||
    !probe.normalEntry ||
    !probe.freshChallenge ||
    !probe.success ||
    probe.observedAt > now ||
    now - probe.observedAt >= 60
  )
    throw new Error('probe cannot commit');
  return true;
}
