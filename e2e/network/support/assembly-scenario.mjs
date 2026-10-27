import assert from 'node:assert/strict';

// The production collab guard is per node, across every local. A deliberately
// unresolved live-C mutation must not become setup for another issuance case.
export function assertSourceScenarioUnblocked(db, identity, label) {
  const operations = db['collab.network_source_operation'];
  assert.ok(Array.isArray(operations), `${label}: operation snapshot required`);
  const pending = operations.filter(
    (row) =>
      row.tenant_id === identity.tenant &&
      row.realm === identity.realm &&
      row.node_id === identity.node &&
      row.method === 'Register' &&
      row.status === 'pending',
  );
  assert.equal(
    pending.length,
    0,
    `${label}: unresolved Register on this node blocks new issuance across locals`,
  );
}
