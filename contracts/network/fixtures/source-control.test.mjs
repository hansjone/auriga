import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import Ajv2020 from '../../../services/contracts/node_modules/ajv/dist/2020.js';
import addFormats from '../../../services/contracts/node_modules/ajv-formats/dist/index.js';

const ajv = new Ajv2020({ strict: true, allErrors: true });
addFormats(ajv);
for (const name of ['address', 'envelope', 'source-control', 'protocol']) {
  ajv.addSchema(
    JSON.parse(
      readFileSync(new URL(`../${name}.schema.json`, import.meta.url), 'utf8'),
    ),
  );
}
const validate = ajv.getSchema(
  'https://auriga.example.com/contracts/network/protocol.schema.json',
);
const registration = () => ({
  registration_id: 'registration-1',
  allocation_op_id: 'allocation-1',
  allocation_fingerprint: `sha256:${'a'.repeat(64)}`,
  registration_revision: '3',
  local: '7',
  address_type: 'session',
});
const witness = () => ({
  witness_id: 'witness-1',
  binding_id: 'local-binding-1',
  binding_revision: '2',
  observed_at: '2026-10-25T00:00:00Z',
  valid_until: '2026-10-25T00:00:30Z',
});
// Reuse an actual signed fixture's auth shape; schema checks do not verify signatures.
function serverAuth() {
  const find = (value) => {
    if (!value || typeof value !== 'object') return null;
    if (value.server_auth) return value.server_auth;
    for (const child of Object.values(value)) {
      const result = find(child);
      if (result) return result;
    }
    return null;
  };
  return structuredClone(
    find(
      JSON.parse(
        readFileSync(new URL('./frames.json', import.meta.url), 'utf8'),
      ),
    ),
  );
}
const source = () => ({
  source_binding: 'source-1',
  registration: registration(),
  scope: 'durable_unicast_send',
  revision: '1',
  state: 'active',
  not_before: '2026-10-25T00:00:00Z',
  expires_at: '2026-10-25T00:05:00Z',
  predecessor_binding: '',
});
function frame(type) {
  const base = { app_version: '1.0', type, request_id: 'request-1' };
  if (!type.endsWith('_result'))
    Object.assign(base, { epoch: '4', boot_id: 'boot-1' });
  else base.server_auth = serverAuth();
  if (type === 'source_register')
    Object.assign(base, {
      operation_id: 'register-1',
      mode: 'issue',
      registration: registration(),
      scope: 'durable_unicast_send',
      expected_revision: '0',
      predecessor_binding: '',
      native_witness: witness(),
    });
  if (type === 'source_get')
    Object.assign(base, {
      source_binding: 'source-1',
      registration: registration(),
      native_witness: witness(),
    });
  if (type === 'source_revoke')
    Object.assign(base, {
      source_binding: 'source-1',
      expected_revision: '1',
      operation_id: 'revoke-1',
    });
  if (type === 'source_register_result')
    Object.assign(base, { operation_id: 'register-1', source: source() });
  if (type === 'source_get_result')
    Object.assign(base, {
      source: source(),
      policy_revision: '2',
      observed_at: '2026-10-25T00:00:01Z',
      valid_until: '2026-10-25T00:00:30Z',
      witness_id: 'witness-1',
    });
  if (type === 'source_revoke_result')
    Object.assign(base, {
      operation_id: 'revoke-1',
      source_binding: 'source-1',
      revision: '2',
      state: 'revoked',
      lineage_stopped: true,
    });
  return base;
}
const types = [
  'source_register',
  'source_register_result',
  'source_get',
  'source_get_result',
  'source_revoke',
  'source_revoke_result',
];
test('all six source frames are part of formal Upstream/Downstream', () => {
  for (const type of types)
    assert.equal(
      validate(frame(type)),
      true,
      `${type}: ${JSON.stringify(validate.errors)}`,
    );
});
test('source control never accepts self-declared tenant, connection or native host identifiers', () => {
  for (const type of types.filter((t) => !t.endsWith('_result')))
    for (const key of [
      'tenant',
      'realm',
      'node',
      'connection',
      'host_session_id',
      'source_context',
    ]) {
      const value = frame(type);
      value[key] = 'forged';
      assert.equal(validate(value), false, `${type}/${key}`);
    }
  for (const key of ['host_session_id', 'cwd', 'owner_user_id']) {
    const value = frame('source_get');
    value.native_witness[key] = 'forged';
    assert.equal(validate(value), false, key);
  }
});
test('native and typed-local witnesses require every original field', () => {
  for (const field of ['registration', 'native_witness'])
    for (const key of Object.keys(frame('source_get')[field])) {
      const value = frame('source_get');
      delete value[field][key];
      assert.equal(validate(value), false, `${field}/${key}`);
    }
});
test('source scope and type do not grant human, subagent, system, group or broadcast authority', () => {
  for (const type of ['subagent', 'system', 'group', 'unknown']) {
    const value = frame('source_register');
    value.registration.address_type = type;
    assert.equal(validate(value), false);
  }
  for (const scope of ['', 'broadcast_send', 'first_contact', 'control']) {
    const value = frame('source_register');
    value.scope = scope;
    assert.equal(validate(value), false);
  }
});
test('uint64 strings and IDs reject loss of precision, zero revisions and oversized keys', () => {
  for (const local of ['0', '01', '18446744073709551616', 7]) {
    const value = frame('source_get');
    value.registration.local = local;
    assert.equal(validate(value), false);
  }
  for (const revision of ['0', '01', 1]) {
    const value = frame('source_get');
    value.registration.registration_revision = revision;
    assert.equal(validate(value), false);
  }
  const value = frame('source_register');
  value.operation_id = 'a'.repeat(129);
  assert.equal(validate(value), false);
});
test('first generation and successor require matching predecessor/CAS shape', () => {
  const value = frame('source_register');
  value.predecessor_binding = 'source-0';
  assert.equal(validate(value), false);
  value.expected_revision = '2';
  assert.equal(validate(value), true);
  value.predecessor_binding = '';
  assert.equal(validate(value), false);
});
test('receipt reconciliation cannot carry an apparently fresh issuance witness', () => {
  const value = frame('source_register');
  value.mode = 'reconcile';
  assert.equal(validate(value), false);
  value.native_witness = null;
  assert.equal(validate(value), true);
  value.mode = 'issue';
  assert.equal(validate(value), false);
});
test('terminal Get cannot carry an active validity window', () => {
  const value = frame('source_get_result');
  value.source.state = 'expired';
  assert.equal(validate(value), false);
  value.valid_until = null;
  assert.equal(validate(value), true);
  value.source.state = 'active';
  assert.equal(validate(value), false);
});
test('mutation receipts cannot claim current allow or successful active revocation', () => {
  const value = frame('source_revoke_result');
  value.state = 'active';
  assert.equal(validate(value), false);
  value.state = 'expired';
  assert.equal(validate(value), true);
  value.lineage_stopped = false;
  assert.equal(validate(value), false);
  const registered = frame('source_register_result');
  registered.source.revision = '2';
  assert.equal(validate(registered), false);
  registered.source.revision = '1';
  registered.allowed = true;
  assert.equal(validate(registered), false);
});
test('every source result requires a complete server signature envelope', () => {
  for (const type of types.filter((t) => t.endsWith('_result'))) {
    const value = frame(type);
    delete value.server_auth;
    assert.equal(validate(value), false, type);
  }
});
