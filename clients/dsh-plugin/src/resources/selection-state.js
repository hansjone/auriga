import { requireSchema } from './schema.js';
import { refuse } from './trust.js';
import { canonicalize } from './json.js';
/** K2 explicit desired state wins over the legacy boolean, including tombstones.
 * @param {any} resource @returns {'ENABLED'|'DISABLED'|'REMOVED'} */
export function desiredState(resource) {
  if (Object.hasOwn(resource, 'desiredState')) {
    if (!['ENABLED', 'DISABLED', 'REMOVED'].includes(resource.desiredState))
      refuse('common.validation_failed');
    return resource.desiredState;
  }
  if (typeof resource.enabled !== 'boolean') refuse('common.validation_failed');
  return resource.enabled ? 'ENABLED' : 'DISABLED';
}
/** Member responses already carry an authoritative positive outer revision.
 * A K2 per-resource revision, when present, must preserve that same CAS value.
 * @param {any} row */
export function requireInstallation(row) {
  requireSchema('UnifiedInstallation', row);
  if (
    Object.hasOwn(row.resource, 'installationRevision') &&
    row.resource.installationRevision !== row.installationRevision
  )
    refuse('resource.state_conflict');
  desiredState(row.resource);
  return row;
}
/** An older tombstone or installation must never replace a newer local selection.
 * Equal installation revisions may renew signatures/leases, not change identity or intent.
 * @param {any} previous @param {any[]} rows */
export function assertSelectionProgress(previous, rows) {
  if (!previous) return;
  if (
    previous.selectionRevision > rows[0].selectionRevision ||
    previous.generation > rows[0].generation
  )
    refuse('common.conflict');
  for (const row of rows) {
    const prior = previous.installations.find(
      (/** @type {any} */ item) =>
        item.resource.identity.packageId === row.resource.identity.packageId,
    );
    if (
      prior &&
      (prior.installationRevision > row.installationRevision ||
        (prior.installationRevision === row.installationRevision &&
          (canonicalize(prior.resource.identity) !==
            canonicalize(row.resource.identity) ||
            desiredState(prior.resource) !== desiredState(row.resource))))
    )
      refuse('common.conflict');
  }
}
