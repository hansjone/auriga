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
  native_binding: { binding_id: 'local-binding-1', binding_revision: '2' },
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
      operation_id: 'revoke-1',
    });
  if (type === 'source_register_result')
    Object.assign(base, {
      operation_id: 'register-1',
      result: 'registered',
      source: source(),
      failure_evidence: null,
    });
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
test('every terminal Get returns its current revision without an observation validity window', () => {
  const samples = JSON.parse(
    readFileSync(
      new URL('./source-get-terminal-cases.json', import.meta.url),
      'utf8',
    ),
  );
  for (const sample of samples) {
    const projected =
      sample.iam_state === 'revoked'
        ? 'revoked'
        : sample.lineage_stopped || sample.local_stopped
          ? 'stopped'
          : sample.iam_state;
    assert.equal(projected, sample.wire_state, sample.name);
    const value = frame('source_get_result');
    value.source.state = sample.wire_state;
    value.source.revision = sample.revision;
    value.observed_at = sample.observed_at;
    delete value.valid_until;
    assert.equal(validate(value), true, sample.name);
    for (const deadline of [null, '2026-10-25T00:00:30Z']) {
      value.valid_until = deadline;
      assert.equal(validate(value), false, sample.name);
    }
  }
  const value = frame('source_get_result');
  delete value.valid_until;
  assert.equal(validate(value), false);
});
test('an expired Get supplies the successor CAS revision without guessing', () => {
  const value = frame('source_get_result');
  value.source.state = 'expired';
  value.source.revision = '2';
  value.observed_at = '2026-10-25T00:05:01Z';
  delete value.valid_until;
  assert.equal(validate(value), true);
  const successor = frame('source_register');
  successor.predecessor_binding = value.source.source_binding;
  successor.expected_revision = value.source.revision;
  successor.native_witness.observed_at = value.observed_at;
  successor.native_witness.valid_until = '2026-10-25T00:05:31Z';
  assert.equal(validate(successor), true);
});
test('revoke accepts a binding and operation without native witness or revision', () => {
  const value = frame('source_revoke');
  assert.equal(validate(value), true);
  for (const field of ['native_witness', 'expected_revision']) {
    const changed = structuredClone(value);
    changed[field] = field === 'expected_revision' ? '1' : witness();
    assert.equal(validate(changed), false);
  }
});
test('Register definitive failures carry authority evidence and cannot carry an active source', () => {
  for (const [result, authority, reason] of [
    ['not_accepted', 'collab', 'rejected_before_pending'],
    ['not_committed', 'iam', 'original_connection_invalid_uncommitted'],
    ['not_committed', 'iam', 'revision_mismatch'],
    ['not_committed', 'iam', 'predecessor_not_head'],
    ['not_committed', 'iam', 'lineage_stopped'],
  ]) {
    const value = frame('source_register_result');
    Object.assign(value, {
      result,
      source: null,
      failure_evidence: {
        authority,
        reason,
        request_digest: `sha256:${'b'.repeat(64)}`,
      },
    });
    assert.equal(validate(value), true, JSON.stringify(validate.errors));
    value.source = source();
    assert.equal(validate(value), false);
    value.source = null;
    value.failure_evidence.authority = authority === 'iam' ? 'collab' : 'iam';
    assert.equal(validate(value), false);
  }
  const value = frame('source_register_result');
  value.result = 'unknown';
  assert.equal(validate(value), false);
  Object.assign(value, {
    result: 'not_committed',
    source: null,
    failure_evidence: {
      authority: 'iam',
      reason: 'common.conflict',
      request_digest: `sha256:${'b'.repeat(64)}`,
    },
  });
  assert.equal(
    validate(value),
    false,
    'a plain error code is not a committed refusal receipt',
  );
});
test('Get schema bound to the actual saved issuance rejects changed mapping IDs and revisions', () => {
  const cases = JSON.parse(
    readFileSync(
      new URL('./source-binding-cases.json', import.meta.url),
      'utf8',
    ),
  );
  for (const sample of cases) {
    // consts come from the trusted persisted issuance, never the incoming frame.
    const bound = ajv.compile({
      $ref: 'https://auriga.example.com/contracts/network/source-control.schema.json#/$defs/SourceGet',
      type: 'object',
      properties: {
        native_witness: {
          type: 'object',
          properties: {
            binding_id: { const: sample.issued_native_binding.binding_id },
            binding_revision: {
              const: sample.issued_native_binding.binding_revision,
            },
          },
        },
      },
    });
    const value = frame('source_get');
    Object.assign(value.native_witness, sample.native_binding);
    assert.equal(
      validate(value),
      true,
      'shape alone cannot prove a saved mapping',
    );
    assert.equal(bound(value), sample.code === null, sample.name);
    if (sample.code !== null) assert.equal(sample.code, 'network.conflict');
  }
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
test('source facts must retain the original positive native mapping without host identifiers', () => {
  for (const type of ['source_register_result', 'source_get_result']) {
    const value = frame(type);
    delete value.source.native_binding;
    assert.equal(validate(value), false);
    value.source.native_binding = {
      binding_id: 'local-binding-1',
      binding_revision: '0',
    };
    assert.equal(validate(value), false);
    value.source.native_binding.binding_revision = '2';
    value.source.native_binding.host_session_id = 'forged';
    assert.equal(validate(value), false);
  }
});
test('every source result requires a complete server signature envelope', () => {
  for (const type of types.filter((t) => t.endsWith('_result'))) {
    const value = frame(type);
    delete value.server_auth;
    assert.equal(validate(value), false, type);
  }
});
