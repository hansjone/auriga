import test from 'node:test';
import assert from 'node:assert/strict';
import { assertSourceScenarioUnblocked } from './support/assembly-scenario.mjs';

const identity = { tenant: 'tenant-a', realm: '1', node: '2' };
const operation = {
  tenant_id: identity.tenant,
  realm: identity.realm,
  node_id: identity.node,
  method: 'Register',
  status: 'pending',
  local: '2',
};
const db = (...rows) => ({ 'collab.network_source_operation': rows });

test('another local on the same node cannot bypass the previous live-C pending operation', () => {
  assert.throws(
    () =>
      assertSourceScenarioUnblocked(db(operation), identity, 'commit local 3'),
    (error) =>
      error.code === 'ERR_ASSERTION' &&
      error.actual === 1 &&
      error.expected === 0,
  );
});

test('completed operations and independent tenants, realms or nodes do not block the scenario', () => {
  assertSourceScenarioUnblocked(
    db(
      { ...operation, status: 'registered' },
      { ...operation, status: 'not_committed' },
      { ...operation, status: 'not_accepted' },
      { ...operation, method: 'Revoke' },
      { ...operation, tenant_id: 'tenant-b' },
      { ...operation, realm: '3' },
      { ...operation, node_id: '4' },
    ),
    identity,
    'commit',
  );
  assertSourceScenarioUnblocked(db(), identity, 'commit');
});

test('missing operation facts are not proof that issuance is unblocked', () => {
  assert.throws(
    () => assertSourceScenarioUnblocked({}, identity, 'commit'),
    /operation snapshot required/,
  );
});
