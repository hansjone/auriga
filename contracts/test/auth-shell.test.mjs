import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import Ajv2020 from '../../services/contracts/node_modules/ajv/dist/2020.js';
import addFormats from '../../services/contracts/node_modules/ajv-formats/dist/index.js';
import YAML from '../../services/contracts/node_modules/yaml/dist/index.js';

const root = new URL('../', import.meta.url);
const read = (path) => readFileSync(new URL(path, root), 'utf8');
const ajv = new Ajv2020({ strict: true, allErrors: true });
addFormats(ajv);
for (const file of [
  'auth/uac-session-binding.schema.json',
  'auth/uac-login-info.schema.json',
  'plugin/shell-auth.schema.json',
]) {
  ajv.addSchema(JSON.parse(read(file)), new URL(file, root).href);
}
const validate = (file, name) =>
  ajv.getSchema(new URL(file, root).href + '#/$defs/' + name);
const snapshot = validate('plugin/shell-auth.schema.json', 'ShellAuthSnapshot');
const event = validate('plugin/shell-auth.schema.json', 'ShellAuthEvent');
const loginInfo = validate('auth/uac-login-info.schema.json', 'UacLoginInfo');
const binding = validate(
  'auth/uac-session-binding.schema.json',
  'UacSessionBinding',
);
const id = '00000000-0000-4000-8000-000000000001';
const identity = { tenantId: id, memberId: id, deviceId: id, providerId: id };
const base = {
  protocolVersion: 1,
  shellEpoch: id,
  revision: 0,
  state: 'signed_out',
  expiresAt: '2026-11-13T00:00:00Z',
};

for (const state of ['signed_out', 'authenticating', 'revoked']) {
  test(state + ' rejects inherited identity', () => {
    assert.equal(snapshot({ ...base, state }), true);
    assert.equal(snapshot({ ...base, state, identity }), false);
  });
}
test('authenticated snapshot requires identity', () => {
  assert.equal(snapshot({ ...base, state: 'authenticated' }), false);
  assert.equal(snapshot({ ...base, state: 'authenticated', identity }), true);
});
for (const state of [
  'signed_out',
  'authenticating',
  'unreachable',
  'reauth_required',
  'revoked',
]) {
  test(state + ' cannot expose UAC department metadata', () => {
    assert.equal(
      snapshot({
        ...base,
        state,
        uacLoginInfo: { purpose: 'login_info', status: 'pending' },
      }),
      false,
    );
  });
}
for (const reason of [
  'revalidation_rejected',
  'revalidation_unreachable',
  'user_logout',
  'admin_disabled',
  'member_disabled',
  'source_disabled',
]) {
  test('snapshot accepts explicit reason ' + reason, () => {
    assert.equal(snapshot({ ...base, state: 'revoked', reason }), true);
  });
}
test('event payload conditions reject incomplete errors and state updates', () => {
  const envelope = { protocolVersion: 1, requestId: id };
  assert.equal(event({ ...envelope, type: 'error' }), false);
  assert.equal(
    event({ ...envelope, type: 'error', code: 'common.unauthenticated' }),
    true,
  );
  for (const type of ['snapshot', 'changed']) {
    assert.equal(event({ ...envelope, type }), false);
    assert.equal(event({ ...envelope, type, snapshot: base }), true);
  }
});
test('login information cannot claim availability without complete display data', () => {
  assert.equal(
    loginInfo({ purpose: 'login_info', status: 'available' }),
    false,
  );
  assert.equal(
    loginInfo({
      purpose: 'login_info',
      status: 'available',
      department: 'Example department',
      updatedAt: base.expiresAt,
    }),
    true,
  );
  for (const status of ['pending', 'unavailable']) {
    assert.equal(loginInfo({ purpose: 'login_info', status }), true);
    assert.equal(
      loginInfo({
        purpose: 'login_info',
        status,
        department: 'Stale department',
      }),
      false,
    );
  }
  assert.equal(loginInfo({ purpose: 'revalidate', status: 'pending' }), false);
  assert.equal(
    loginInfo({ purpose: 'login_info', status: 'pending', token: 'synthetic' }),
    false,
  );
});
test('device binding requires its complete server-issued source identity', () => {
  const valid = { sourceSessionId: id, authGeneration: 1, providerId: id };
  assert.equal(binding(valid), true);
  for (const field of Object.keys(valid)) {
    const incomplete = { ...valid };
    delete incomplete[field];
    assert.equal(binding(incomplete), false);
  }
  assert.equal(binding({ ...valid, authGeneration: 0 }), false);
  assert.equal(binding({ ...valid, sourceSessionId: 'invalid' }), false);
  assert.equal(binding({ ...valid, token: 'synthetic' }), false);
});
test('UAC link and diagnostics contracts preserve explicit authentication boundaries', () => {
  const spec = YAML.parse(read('openapi/iam.yaml'));
  const create = spec.paths['/login/uac/challenges'].post;
  assert.deepEqual(
    spec.components.schemas.UacChallengeCreate.properties.purpose.enum,
    ['login', 'link'],
  );
  assert.equal(
    create.security.some((item) => item.bearerAuth),
    true,
  );
  const admin = spec.paths['/admin/uac/diagnostic-bundles'].post;
  assert.equal(admin['x-admin-only'], true);
  assert.equal(
    admin.responses['200'].content['application/zip'].schema.format,
    'binary',
  );
  assert.match(
    spec.paths['/admin/providers/{providerId}'].patch.description,
    /UAC.*enabled.*config/,
  );
  const status =
    spec.components.schemas.UacChallengeStatus.properties.state.enum;
  assert.equal(status.includes('cancelled') && status.includes('failed'), true);
});
test('ShellAuth projection is wired into the normal generation and lint workflow', () => {
  const pkg = JSON.parse(
    readFileSync(
      new URL('../../services/contracts/package.json', import.meta.url),
      'utf8',
    ),
  );
  assert.match(pkg.scripts['lint-openapi'], /shell-auth\.yaml/);
  const spec = YAML.parse(read('openapi/shell-auth.yaml'));
  assert.equal(spec.info.license.identifier, 'AGPL-3.0-only');
});

test('SSO login accepts navigation intent and rejects client-supplied upstream credentials', () => {
  const spec = YAML.parse(read('openapi/iam.yaml'));
  const request = ajv.compile(spec.components.schemas.UacSsoLogin);
  const valid = {
    providerId: id,
    navigationProof: 'A'.repeat(43),
    mode: 'auto',
  };
  assert.equal(request(valid), true);
  assert.equal(request({ ...valid, mode: 'continue' }), true);
  for (const field of ['providerId', 'navigationProof', 'mode']) {
    const incomplete = { ...valid };
    delete incomplete[field];
    assert.equal(request(incomplete), false);
  }
  for (const field of [
    'token',
    'account',
    'cookie',
    'clientIP',
    'returnTo',
    'device_code',
  ]) {
    assert.equal(request({ ...valid, [field]: 'synthetic' }), false);
  }
  assert.equal(request({ ...valid, mode: 'link' }), false);
  assert.equal(request({ ...valid, navigationProof: 'short' }), false);
  assert.equal(request({ ...valid, navigationProof: '+'.repeat(43) }), false);
  assert.equal(request({ ...valid, providerId: 'invalid' }), false);
});

test('SSO result supports safe QR fallback without returning upstream tokens', () => {
  const spec = YAML.parse(read('openapi/iam.yaml'));
  const result = ajv.compile(spec.components.schemas.UacSsoLoginResult);
  for (const state of [
    'authenticated',
    'additional_step_required',
    'scan_required',
  ]) {
    const valid = { transactionId: id, state };
    assert.equal(result(valid), true);
    assert.equal(result({ ...valid, token: 'synthetic' }), false);
    assert.equal(result({ ...valid, rawResponse: {} }), false);
  }
  assert.equal(result({ state: 'authenticated' }), false);
  assert.equal(result({ transactionId: id, state: 'linked' }), false);
  const operation = spec.paths['/login/uac/sso'].post;
  assert.deepEqual(operation.security, [{}]);
  assert.equal(
    operation.responses['403'].$ref,
    'common.yaml#/components/responses/Forbidden',
  );
});
