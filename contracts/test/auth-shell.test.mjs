import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync, readdirSync } from 'node:fs';
import Ajv2020 from '../../services/contracts/node_modules/ajv/dist/2020.js';
import addFormats from '../../services/contracts/node_modules/ajv-formats/dist/index.js';
import YAML from '../../services/contracts/node_modules/yaml/dist/index.js';
import { spawnSync } from 'node:child_process';

const root = new URL('../', import.meta.url);
const read = (path) => readFileSync(new URL(path, root), 'utf8');
const ajv = new Ajv2020({ strict: true, allErrors: true });
addFormats(ajv);
for (const file of [
  ...readdirSync(new URL('network/', root))
    .filter((name) => name.endsWith('.schema.json'))
    .map((name) => 'network/' + name),
  'plugin/team-mcp.schema.json',
  'plugin/team-transport.schema.json',
  'plugin/shell-business.schema.json',
  'plugin/shell-auth-control.schema.json',
  'auth/uac-control.schema.json',
  'auth/uac-diagnostic-log.schema.json',
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
const business = validate(
  'plugin/shell-business.schema.json',
  'ShellAuthBusinessCall',
);
const businessResult = validate(
  'plugin/shell-business.schema.json',
  'ShellAuthBusinessResult',
);
const operations = JSON.parse(
  read('plugin/shell-business-operations.json'),
).operations;

test('AUTH-K fixed operation projection stays current and covers all platform families', () => {
  const result = spawnSync(
    process.execPath,
    [
      new URL(
        '../plugin/generate-shell-business.mjs',
        import.meta.url,
      ).pathname.replace(/^\/([A-Za-z]:)/, '$1'),
      '--check',
    ],
    { encoding: 'utf8' },
  );
  assert.equal(result.status, 0, result.stderr);
  for (const name of [
    'UnifiedListResourcePackages',
    'UnifiedUploadResourceVersion',
    'UnifiedDownloadArtifact',
    'listMemberResourceInstallations',
    'requestResourceDistribution',
    'listAiModels',
    'createAiChatCompletion',
    'network_open',
    'network_send',
    'network_submit',
    'team_node_bind',
    'team_node_ack',
    'team_open_thread',
    'team_read_thread',
    'profile_read',
    'provider_diagnostic',
  ]) {
    assert.ok(
      operations.some((op) => op.operation === name),
      name,
    );
  }
  assert.equal(
    new Set(operations.map((op) => op.operation)).size,
    operations.length,
  );
  for (const op of operations) {
    assert.ok(op.input.startsWith('ShellAuth'));
    assert.ok(op.output.startsWith('ShellAuth'));
    assert.ok(op.permission && op.audience && op.retry);
    assert.doesNotMatch(
      op.route,
      /\/internal\/|\/admin\/|\/oauth2\/|\/network\/tickets/,
    );
  }
});

test('business producers reject URL, header and identity injection; response consumers allow extensions', () => {
  const request = {
    protocolVersion: 1,
    requestId: id,
    shellEpoch: id,
    expectedRevision: 0,
    expiresAt: base.expiresAt,
    call: { operation: 'profile_read', input: {} },
  };
  assert.equal(business(request), true, JSON.stringify(business.errors));
  for (const field of ['url', 'headers', 'token', 'tenantId', 'memberId']) {
    assert.equal(
      business({ ...request, [field]: 'https://evil.example.com' }),
      false,
    );
    assert.equal(
      business({
        ...request,
        call: { ...request.call, input: { [field]: 'synthetic' } },
      }),
      false,
    );
  }
  assert.equal(
    business({ ...request, call: { operation: 'arbitrary_http', input: {} } }),
    false,
  );
  const good = {
    requestId: id,
    operation: 'profile_read',
    output: { memberId: id, displayName: 'Synthetic member' },
  };
  assert.equal(businessResult(good), true);
  assert.equal(
    businessResult({ ...good, output: { token: 'synthetic' } }),
    false,
  );
  const schema = structuredClone(
    JSON.parse(read('plugin/shell-business.schema.json')),
  );
  function loosen(value) {
    if (!value || typeof value !== 'object') return;
    if (value.additionalProperties === false) value.additionalProperties = true;
    Object.values(value).forEach(loosen);
  }
  loosen(schema);
  const consumer = new Ajv2020({ strict: true });
  addFormats(consumer);
  // This result arm has no external references: extension handling is independent of request admission.
  const responseSchema = schema.$defs.ShellAuthOutput_profile_read;
  assert.equal(
    consumer.compile(responseSchema)({ ...good.output, futureField: true }),
    true,
  );
});

test('binding fixtures specify rejection for cross-user/tenant, expiry, revocation and stale generations', () => {
  const fixture = JSON.parse(read('test/auth-k-fixtures.json'));
  // Contract oracle only: A/C/D must run these same vectors against their real authority.
  const accepts = (candidate) =>
    Object.entries(fixture.binding).every(([key, value]) =>
      key === 'expiresAt'
        ? Date.parse(candidate[key]) > Date.parse(fixture.now)
        : candidate[key] === value,
    );
  assert.equal(accepts(fixture.binding), true);
  for (const patch of fixture.reject)
    assert.equal(
      accepts({ ...fixture.binding, ...patch }),
      false,
      JSON.stringify(patch),
    );
});

test('revalidation config defaults and bounds remain the decided policy', () => {
  const policy = validate(
    'auth/uac-control.schema.json',
    'UacRevalidationPolicy',
  );
  assert.equal(policy({}), true);
  const properties = JSON.parse(read('auth/uac-control.schema.json')).$defs
    .UacRevalidationPolicy.properties;
  assert.equal(properties.strategy.default, 'token-verify');
  assert.equal(properties.intervalSeconds.default, 7200);
  assert.equal(properties.retryIntervalSeconds.default, 30);
  assert.equal(properties.maxRetries.default, 3);
  for (const [key, min, max] of [
    ['intervalSeconds', 60, 86400],
    ['retryIntervalSeconds', 5, 300],
    ['maxRetries', 0, 10],
  ]) {
    for (const value of [min, max])
      assert.equal(policy({ [key]: value }), true);
    for (const value of [min - 1, max + 1, 1.5, '30'])
      assert.equal(policy({ [key]: value }), false);
  }
  assert.equal(policy({ strategy: 'invented' }), false);
});

test('independent diagnostics revision requires expiry when enabled', () => {
  const settings = validate(
    'auth/uac-control.schema.json',
    'UacDiagnosticSettings',
  );
  assert.equal(settings({ enabled: false, revision: 0 }), true);
  assert.equal(settings({ enabled: true, revision: 1 }), false);
  assert.equal(
    settings({ enabled: true, revision: 1, expiresAt: base.expiresAt }),
    true,
  );
  const spec = YAML.parse(read('openapi/iam.yaml'));
  const put = spec.paths['/admin/providers/{providerId}/uac/diagnostics'].put;
  assert.equal(put['x-admin-only'], true);
  assert.equal(put['x-permission'], 'iam.providers.write');
  assert.ok(put.parameters.some((p) => p.name === 'If-Match' && p.required));
});

test('loopback tickets are bounded opaque values and never accept target URL', () => {
  const exchange = validate(
    'plugin/shell-auth-control.schema.json',
    'ShellAuthLoopbackExchange',
  );
  const value = { ticket: 'A'.repeat(43), clientNonce: 'B'.repeat(43) };
  assert.equal(exchange(value), true);
  assert.equal(exchange({ ...value, ticket: 'short' }), false);
  assert.equal(exchange({ ...value, url: 'https://evil.example.com' }), false);
  const page = validate(
    'plugin/shell-auth-control.schema.json',
    'ShellAuthPageSession',
  );
  const session = {
    pageToken: 'A'.repeat(43),
    csrf: 'B'.repeat(43),
    shellEpoch: id,
    origin: 'http://127.0.0.1:12345',
    port: 12345,
    clientNonce: 'C'.repeat(43),
    expiresAt: base.expiresAt,
  };
  assert.equal(page(session), true);
  assert.equal(page({ ...session, origin: 'http://localhost:12345' }), false);
  assert.equal(page({ ...session, port: 65536 }), false);
});

test('enterprise compatibility keeps raw enabled and refuses authentication headers', () => {
  const defs = JSON.parse(read('plugin/shell-auth-control.schema.json')).$defs;
  assert.equal(
    defs.ShellAuthEnterpriseConfig.properties.allowRawAgentToken.default,
    true,
  );
  const outbound = validate(
    'plugin/shell-auth-control.schema.json',
    'ShellAuthOutboundRequest',
  );
  assert.equal(
    outbound({
      url: 'https://api.example.com/business',
      headers: { accept: 'application/json' },
    }),
    true,
  );
  for (const key of ['Authorization', 'cookie', 'x-enterprise-token'])
    assert.equal(
      outbound({
        url: 'https://api.example.com/business',
        headers: { [key]: 'synthetic' },
      }),
      false,
    );
  assert.equal(outbound({ url: 'http://api.example.com/business' }), false);
  assert.equal(
    outbound({ url: 'https://api.example.com/business', timeoutMs: 60001 }),
    false,
  );
  const credentials = validate(
    'plugin/shell-auth-control.schema.json',
    'ShellAuthAgentCredentials',
  );
  assert.equal(
    credentials({ empNo: '00001', token: 'synthetic', updatedAt: null }),
    true,
  );
});

test('stream contracts require credit, bounded chunks and explicit terminal outcomes', () => {
  const command = validate(
    'plugin/shell-business.schema.json',
    'ShellAuthStreamCommand',
  );
  const envelope = { streamId: id, shellEpoch: id, expectedRevision: 0 };
  assert.equal(
    command({ ...envelope, action: 'credit', creditBytes: 65536 }),
    true,
  );
  assert.equal(command({ ...envelope, action: 'credit' }), false);
  assert.equal(
    command({ ...envelope, action: 'credit', creditBytes: 1048577 }),
    false,
  );
  for (const action of ['cancel', 'close']) {
    assert.equal(command({ ...envelope, action }), true);
    assert.equal(command({ ...envelope, action, creditBytes: 1 }), false);
  }
  const stream = validate(
    'plugin/shell-business.schema.json',
    'ShellAuthStreamEvent',
  );
  const event = { streamId: id, shellEpoch: id, revision: 0, sequence: 1 };
  assert.equal(stream({ ...event, type: 'data', data: 'eA==' }), true);
  assert.equal(stream({ ...event, type: 'data' }), false);
  assert.equal(
    stream({ ...event, type: 'data', data: 'A'.repeat(87385) }),
    false,
  );
  assert.equal(stream({ ...event, type: 'end' }), true);
  assert.equal(stream({ ...event, type: 'closed', data: 'eA==' }), false);
  assert.equal(stream({ ...event, type: 'error' }), false);
  assert.equal(
    stream({ ...event, type: 'error', code: 'common.unauthenticated' }),
    true,
  );
});

test('navigation proof and device source bindings reject incomplete or forged producers', () => {
  const proof = validate(
    'auth/uac-control.schema.json',
    'UacNavigationProofBinding',
  );
  const value = {
    transactionId: id,
    proofHash: 'a'.repeat(64),
    origin: 'https://portal.example.com',
    tenantId: id,
    providerId: id,
    audience: 'portal',
    purpose: 'login',
    continuation: 'device',
    autoEligible: false,
    expiresAt: base.expiresAt,
    consumed: false,
  };
  assert.equal(proof(value), true);
  for (const key of Object.keys(value)) {
    const copy = { ...value };
    delete copy[key];
    assert.equal(proof(copy), false, key);
  }
  assert.equal(proof({ ...value, audience: 'admin' }), false);
  assert.equal(proof({ ...value, purpose: 'link' }), false);
  assert.equal(proof({ ...value, returnTo: '//evil.example.com' }), false);
  const device = validate(
    'auth/uac-control.schema.json',
    'UacCredentialBinding',
  );
  const bound = {
    tenantId: id,
    memberId: id,
    deviceId: id,
    sourceSessionId: id,
    providerId: id,
    authGeneration: 1,
    credentialVersion: 1,
    expiresAt: base.expiresAt,
  };
  assert.equal(device(bound), true);
  for (const key of Object.keys(bound)) {
    const copy = { ...bound };
    delete copy[key];
    assert.equal(device(copy), false, key);
  }
});

test('encrypted credentials never carry plaintext keys and identify the decryption key', () => {
  const envelope = validate(
    'auth/uac-control.schema.json',
    'UacCredentialEnvelope',
  );
  const value = {
    keyId: 'synthetic-v2',
    algorithm: 'A256GCM',
    nonce: 'A'.repeat(16),
    ciphertext: 'A'.repeat(43),
    aad: { tenantId: id, providerId: id, purpose: 'uac_credential' },
  };
  assert.equal(envelope(value), true);
  assert.equal(envelope({ ...value, keyId: undefined }), false);
  assert.equal(envelope({ ...value, nonce: 'short' }), false);
  for (const key of ['key', 'token', 'cookie'])
    assert.equal(envelope({ ...value, [key]: 'synthetic-secret' }), false);
});

test('diagnostic projection rejects secrets and personal values in nested or unknown fields', () => {
  const diagnostic = validate(
    'auth/uac-diagnostic-log.schema.json',
    'UacDiagnosticLogProjection',
  );
  const safe = {
    correlationId: id,
    purpose: 'login_info',
    result: 'failure',
    tokenHashPrefix: 'a'.repeat(12),
    departmentPresent: false,
    message: '[REDACTED]',
  };
  assert.equal(diagnostic(safe), true);
  for (const secret of [
    'synthetic-token',
    'Synthetic Name',
    'Synthetic Department',
    'alice@example.com',
    '555-0100',
  ]) {
    for (const field of [
      'headers',
      'unknown',
      'exception',
      'array',
      'udsRawSanitized',
    ]) {
      assert.equal(
        diagnostic({ ...safe, [field]: { nested: [secret] } }),
        false,
      );
    }
    assert.equal(diagnostic({ ...safe, message: secret }), false);
    assert.equal(diagnostic({ ...safe, tokenHashPrefix: secret }), false);
  }
  assert.equal(diagnostic({ ...safe, empNo: '00001' }), true);
});
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

test('enterprise credential reads use only the authenticated caller and return a bounded current value', () => {
  const spec = YAML.parse(read('openapi/iam.yaml'));
  const operation = spec.paths['/me/uac/credentials'].post;
  const request = ajv.compile(
    operation.requestBody.content['application/json'].schema,
  );
  assert.equal(request({}), true);
  for (const field of [
    'empNo',
    'memberId',
    'sessionId',
    'providerId',
    'token',
  ]) {
    assert.equal(request({ [field]: id }), false);
  }
  assert.equal(request(null), false);
  assert.deepEqual(operation.security, [{ bearerAuth: [] }]);
  const value = ajv.compile(spec.components.schemas.UacEnterpriseCredentials);
  const valid = {
    empNo: '00123',
    token: 'synthetic',
    updatedAt: base.expiresAt,
  };
  assert.equal(value(valid), true);
  assert.equal(value({ ...valid, cookies: { example: 'synthetic' } }), true);
  assert.equal(value({ ...valid, token: '' }), false);
  assert.equal(value({ ...valid, updatedAt: 'invalid' }), false);
  assert.equal(value({ ...valid, otherAccount: id }), false);
  assert.equal(value({ ...valid, cookies: { example: 123 } }), false);
  assert.equal(
    value({
      ...valid,
      cookies: Object.fromEntries(
        Array.from({ length: 17 }, (_, i) => [String(i), 'synthetic']),
      ),
    }),
    false,
  );
  assert.deepEqual(
    operation.responses['200'].headers['Cache-Control'].schema.enum,
    ['no-store'],
  );
});
