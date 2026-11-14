import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync, readdirSync } from 'node:fs';
import Ajv2020 from '../../services/contracts/node_modules/ajv/dist/2020.js';
import addFormats from '../../services/contracts/node_modules/ajv-formats/dist/index.js';
import YAML from '../../services/contracts/node_modules/yaml/dist/index.js';
import { spawnSync } from 'node:child_process';
import {
  checkPlatformMigration,
  readPluginSources,
} from './auth-k-source-scan.mjs';

const root = new URL('../', import.meta.url);
const read = (path) => readFileSync(new URL(path, root), 'utf8');
const ajv = new Ajv2020({ strict: true, allErrors: true });
addFormats(ajv, { keywords: true });
for (const file of [
  ...readdirSync(new URL('network/', root))
    .filter((name) => name.endsWith('.schema.json'))
    .map((name) => 'network/' + name),
  'plugin/team-mcp.schema.json',
  'plugin/team-transport.schema.json',
  'plugin/shell-business.schema.json',
  'plugin/shell-auth-control.schema.json',
  'auth/uac-control.schema.json',
  'auth/standard-device.schema.json',
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
  const inventory = JSON.parse(read('plugin/shell-auth-call-sites.json'));
  const calls = inventory.calls;
  checkPlatformMigration(inventory, operations);
  for (const call of calls) {
    assert.ok(call.methods.length);
    assert.ok(call.routePattern.startsWith('/'));
    assert.ok(call.control || call.operations.length, call.file);
    for (const operation of call.operations)
      assert.ok(
        operations.some((op) => op.operation === operation),
        operation,
      );
  }
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
    output: {
      statusCode: 200,
      body: { memberId: id, displayName: 'Synthetic member' },
    },
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
  const responseSchema =
    schema.$defs.ShellAuthOutput_profile_read.properties.body;
  assert.equal(
    consumer.compile(responseSchema)({
      ...good.output.body,
      futureField: true,
    }),
    true,
  );
});

test('binding fixtures specify rejection for cross-user/tenant, expiry, revocation and stale generations', () => {
  const fixture = JSON.parse(read('test/auth-k-fixtures.json'));
  const accepts = ajv.compile(fixture.acceptanceSchema);
  for (const vector of fixture.vectors)
    assert.equal(accepts(vector.input), vector.expected, vector.name);
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

test('enterprise compatibility accepts discarded headers and clamps oversized timeouts', () => {
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
      true,
    );
  assert.equal(outbound({ url: 'http://api.example.com/business' }), false);
  assert.equal(
    outbound({ url: 'https://api.example.com/business', timeoutMs: 60001 }),
    true,
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
    stream({
      ...event,
      type: 'error',
      code: 'common.unauthenticated',
      error: { code: 'common.unauthenticated', retryable: false },
    }),
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
    assert.equal(device(copy), key === 'credentialVersion', key);
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

test('diagnostic manifests identify versions, safe sources and final file digests', () => {
  const manifest = validate(
    'auth/uac-control.schema.json',
    'UacDiagnosticManifest',
  );
  const value = {
    version: 1,
    from: base.expiresAt,
    to: base.expiresAt,
    redactorVersion: '1',
    exportAuditId: id,
    partial: true,
    missingSources: ['loki'],
    files: [
      {
        name: 'audit.jsonl',
        sha256: 'a'.repeat(64),
        lines: 1,
        bytes: 100,
        truncated: false,
      },
    ],
    installationVersion: 'synthetic-1',
    componentVersions: { iam: 'synthetic-1' },
    sources: [{ source: 'loki', available: false, reason: 'unreachable' }],
  };
  assert.equal(manifest(value), true);
  assert.equal(
    manifest({
      ...value,
      files: [{ ...value.files[0], name: '../../private.json' }],
    }),
    false,
  );
  assert.equal(
    manifest({ ...value, files: [{ ...value.files[0], sha256: 'invalid' }] }),
    false,
  );
  assert.equal(manifest({ ...value, token: 'synthetic' }), false);
  assert.equal(
    manifest({
      ...value,
      sources: [{ source: 'https://evil.example.com', available: true }],
    }),
    false,
  );
  const keyring = validate(
    'auth/uac-control.schema.json',
    'UacDecryptionKeyring',
  );
  assert.equal(
    keyring({
      activeKeyId: 'new',
      decryptKeyIds: ['old', 'new'],
      legacyKeyId: 'old',
      revision: 1,
    }),
    true,
  );
  assert.equal(
    keyring({ activeKeyId: 'new', decryptKeyIds: ['new', 'new'], revision: 1 }),
    false,
  );
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
    operation.responses['403'].content['application/problem+json'].schema.$ref,
    'common.yaml#/components/schemas/Problem',
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
  const credentialSchema = structuredClone(
    spec.components.schemas.UacEnterpriseCredentials,
  );
  // This test compiles an extracted schema, so resolve its new component reference.
  credentialSchema.properties.binding.$ref =
    new URL('auth/uac-control.schema.json', root).href +
    '#/$defs/UacCredentialBinding';
  const value = ajv.compile(credentialSchema);
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

test('business headers preserve resource concurrency and reject credential headers', () => {
  const schema = JSON.parse(read('plugin/shell-business.schema.json'));
  for (const op of operations) {
    const input = schema.$defs[op.input];
    assert.deepEqual(
      Object.keys(input.properties.headers?.properties ?? {}),
      op.requestHeaders,
    );
    if (!op.requestHeaders.length) continue;
    const headers = ajv.compile(input.properties.headers);
    const good = Object.fromEntries(
      op.requestHeaders.map((name) => [name, 'synthetic-revision']),
    );
    assert.equal(headers(good), true, op.operation);
    for (const name of input.properties.headers.required) {
      const missing = { ...good };
      delete missing[name];
      assert.equal(headers(missing), false, op.operation + ':' + name);
      assert.ok(input.required.includes('headers'), op.operation);
    }
    for (const name of ['authorization', 'cookie', 'x-tenant-id'])
      assert.equal(
        headers({ ...good, [name]: 'synthetic' }),
        false,
        op.operation,
      );
  }
  const outputHeaders =
    schema.$defs.ShellAuthOutput_UnifiedSelectInstallation.properties.headers;
  const headers = ajv.compile(outputHeaders);
  assert.equal(headers({ etag: '"revision-2"' }), true);
  assert.equal(headers({ 'set-cookie': 'synthetic' }), false);
  assert.equal(headers({ authorization: 'synthetic' }), false);
});

test('AI SSE registers a stream and has data, end, cancel and safe error frames', () => {
  const json = validate(
    'plugin/shell-business.schema.json',
    'ShellAuthInput_createAiChatCompletion',
  );
  const streaming = validate(
    'plugin/shell-business.schema.json',
    'ShellAuthInput_createAiChatCompletionStream',
  );
  const body = {
    model: 'example-model',
    messages: [{ role: 'user', content: 'hello' }],
    max_tokens: 16,
  };
  assert.equal(json({ body }), true);
  assert.equal(json({ body: { ...body, stream: false } }), true);
  assert.equal(json({ body: { ...body, stream: true } }), false);
  assert.equal(streaming({ body: { ...body, stream: true } }), true);
  assert.equal(streaming({ body: { ...body, stream: false } }), false);
  assert.equal(streaming({ body }), false);
  assert.equal(
    businessResult({
      requestId: id,
      operation: 'createAiChatCompletionStream',
      output: { statusCode: 200, body: { streamId: id, encoding: 'sse_utf8' } },
    }),
    true,
  );
  const stream = validate(
    'plugin/shell-business.schema.json',
    'ShellAuthStreamEvent',
  );
  const envelope = { streamId: id, shellEpoch: id, revision: 1, sequence: 1 };
  for (const data of ['data: {"choices":[]}\n\n', 'data: [DONE]\n\n'])
    assert.equal(
      stream({
        ...envelope,
        type: 'data',
        data: Buffer.from(data).toString('base64'),
      }),
      true,
    );
  for (const type of ['end', 'closed'])
    assert.equal(stream({ ...envelope, type }), true);
  const error = {
    code: 'common.unavailable',
    retryable: true,
    upstreamStatus: 503,
  };
  assert.equal(
    stream({ ...envelope, type: 'error', code: error.code, error }),
    true,
  );
  assert.equal(stream({ ...envelope, type: 'error', code: error.code }), false);
  assert.equal(stream({ ...envelope, type: 'end', error }), false);
  assert.equal(
    stream({ ...envelope, type: 'data', data: 'not-base64' }),
    false,
  );
  const command = validate(
    'plugin/shell-business.schema.json',
    'ShellAuthStreamCommand',
  );
  assert.equal(
    command({
      streamId: id,
      shellEpoch: id,
      expectedRevision: 1,
      action: 'cancel',
    }),
    true,
  );
});

test('upload uses bounded base64 chunks and validated references instead of binary IPC fields', () => {
  const open = validate(
    'plugin/shell-business.schema.json',
    'ShellAuthInput_upload_open',
  );
  const input = {
    sizeBytes: 67108864,
    sha256: 'a'.repeat(64),
    mediaType: 'application/octet-stream',
  };
  assert.equal(open(input), true);
  assert.equal(open({ ...input, sizeBytes: 67108865 }), false);
  assert.equal(open({ ...input, sha256: 'invalid' }), false);
  const command = validate(
    'plugin/shell-business.schema.json',
    'ShellAuthStreamCommand',
  );
  const chunk = {
    streamId: id,
    shellEpoch: id,
    expectedRevision: 1,
    action: 'upload_chunk',
    sequence: 0,
    data: Buffer.alloc(65536).toString('base64'),
  };
  assert.equal(command(chunk), true);
  assert.equal(
    command({ ...chunk, data: Buffer.alloc(65537).toString('base64') }),
    false,
  );
  assert.equal(command({ ...chunk, data: 'invalid' }), false);
  const missing = { ...chunk };
  delete missing.sequence;
  assert.equal(command(missing), false);
  const stream = validate(
    'plugin/shell-business.schema.json',
    'ShellAuthStreamEvent',
  );
  const ack = {
    streamId: id,
    shellEpoch: id,
    revision: 1,
    sequence: 1,
    type: 'upload_ack',
    ackSequence: 0,
    creditBytes: 65536,
  };
  assert.equal(stream(ack), true);
  assert.equal(stream({ ...ack, creditBytes: 1048577 }), false);
  const missingAck = { ...ack };
  delete missingAck.ackSequence;
  assert.equal(stream(missingAck), false);
  const reference = validate(
    'plugin/shell-business.schema.json',
    'ShellAuthUploadReference',
  );
  assert.equal(
    reference({
      uploadId: id,
      sizeBytes: input.sizeBytes,
      sha256: input.sha256,
    }),
    true,
  );
  assert.equal(
    reference({ uploadId: id, sizeBytes: input.sizeBytes, sha256: 'invalid' }),
    false,
  );
  assert.doesNotMatch(
    read('plugin/shell-business.schema.json'),
    /"format": "binary"/,
  );
});

test('upstream business failures keep status, safe code and retryability without raw text', () => {
  const fixtures = JSON.parse(read('test/auth-k-transport-fixtures.json'));
  for (const vector of fixtures.businessErrors) {
    assert.equal(
      businessResult(vector),
      true,
      JSON.stringify(businessResult.errors),
    );
    for (const field of [
      'message',
      'body',
      'headers',
      'token',
      'cookie',
      'rawResponse',
    ])
      assert.equal(
        businessResult({
          ...vector,
          error: { ...vector.error, [field]: 'synthetic' },
        }),
        false,
      );
    assert.equal(
      businessResult({
        ...vector,
        error: { ...vector.error, code: 'upstream.untrusted' },
      }),
      false,
    );
    assert.equal(
      businessResult({ ...vector, error: { code: vector.error.code } }),
      false,
    );
  }
});

test('skill compatibility vectors accept original requests and distinguish upstream from local failure', () => {
  const fixtures = JSON.parse(read('test/auth-k-transport-fixtures.json'));
  const request = validate(
    'plugin/shell-auth-control.schema.json',
    'ShellAuthOutboundRequest',
  );
  const forwarded = validate(
    'plugin/shell-auth-control.schema.json',
    'ShellAuthOutboundForwardedHeaders',
  );
  const result = validate(
    'plugin/shell-auth-control.schema.json',
    'ShellAuthOutboundResult',
  );
  const local = validate(
    'plugin/shell-auth-control.schema.json',
    'ShellAuthAgentError',
  );
  for (const vector of fixtures.outbound) {
    assert.equal(request(vector.input), true, vector.name);
    assert.equal(
      forwarded(vector.expected.forwardedHeaders),
      true,
      vector.name,
    );
    assert.ok(vector.expected.timeoutMs <= 60000);
    assert.equal(
      forwarded({
        ...vector.expected.forwardedHeaders,
        Authorization: 'synthetic',
      }),
      false,
    );
  }
  for (const vector of fixtures.outboundResults) {
    assert.equal(result(vector.result), true);
    assert.equal(vector.httpStatus, vector.result.statusCode);
    assert.equal(local(vector.result), false);
  }
  for (const vector of fixtures.localErrors) {
    assert.equal(local(vector.result), true);
    assert.equal(result(vector.result), false);
  }
  const spec = YAML.parse(read('openapi/shell-auth.yaml'));
  const response = spec.paths['/dsh-team/outbound'].post.responses.default;
  assert.equal(response.content['application/json'].schema.oneOf.length, 2);
  assert.ok(response.headers['X-Dsh-Team-Outbound-Status']);
  const config = JSON.parse(read('plugin/shell-auth-control.schema.json')).$defs
    .ShellAuthEnterpriseConfig;
  assert.deepEqual(config.properties.outboundAllowedHosts.default, []);
});

test('SSO proof issuance is an internal edge to IAM port with evidence and a 120 second result', () => {
  const spec = YAML.parse(read('openapi/iam.yaml'));
  const op = spec.paths['/internal/uac/navigation-proofs'].post;
  assert.equal(op['x-internal-only'], true);
  assert.equal(op['x-service-caller'], 'svc:edge');
  assert.deepEqual(op.security, [{ internalServiceAuth: [] }]);
  assert.equal(
    op.parameters.find((p) => p.name === 'auriga-tenant-id').required,
    true,
  );
  assert.deepEqual(op.responses['201'].headers['Cache-Control'].schema.enum, [
    'no-store',
  ]);
  const request = validate(
    'auth/uac-control.schema.json',
    'UacNavigationIssueRequest',
  );
  const good = {
    requestId: id,
    providerId: id,
    origin: 'https://portal.example.com',
    continuation: 'portal',
    fetchMetadata: {
      dest: 'document',
      mode: 'navigate',
      site: 'same-origin',
      user: '?1',
    },
  };
  assert.equal(request(good), true);
  assert.equal(request({ ...good, fetchMetadata: {} }), true); // Missing evidence disables auto at IAM.
  assert.equal(request({ ...good, continuation: 'device' }), false);
  assert.equal(
    request({ ...good, continuation: 'device', deviceGrantId: id }),
    false,
  );
  assert.equal(request({ ...good, deviceGrantId: id }), false);
  for (const key of [
    'autoEligible',
    'tenantId',
    'proofHash',
    'navigationProof',
  ])
    assert.equal(request({ ...good, [key]: 'synthetic' }), false);
  const issued = validate(
    'auth/uac-control.schema.json',
    'UacNavigationIssued',
  );
  const value = {
    context: {
      navigationProof: 'A'.repeat(43),
      expiresAt: base.expiresAt,
      transactionId: id,
      purpose: 'portal',
      autoEligible: true,
      autoSuppressed: false,
    },
    transactionCookie: 'B'.repeat(43),
    ttlSeconds: 120,
  };
  assert.equal(issued(value), true);
  assert.equal(issued({ ...value, ttlSeconds: 121 }), false);
  assert.equal(
    spec.paths['/admin/providers/{providerId}/uac'].put.parameters.find(
      (p) => p.name === 'If-Match',
    ).required,
    true,
  );
});

test('loopback page schema exposes only its four bounded commands including skip login', () => {
  const command = validate(
    'plugin/shell-auth-control.schema.json',
    'ShellAuthPageCommand',
  );
  const envelope = {
    protocolVersion: 1,
    requestId: id,
    shellEpoch: id,
    expectedRevision: 0,
  };
  for (const action of ['snapshot', 'open_login', 'cancel_login', 'skip_login'])
    assert.equal(command({ ...envelope, action }), true);
  for (const action of ['logout', 'subscribe', 'arbitrary_http'])
    assert.equal(command({ ...envelope, action }), false);
  assert.equal(
    command({
      ...envelope,
      action: 'open_login',
      url: 'https://evil.example.com',
    }),
    false,
  );
  const spec = YAML.parse(read('openapi/shell-auth.yaml'));
  assert.match(
    spec.paths['/loopback/messages'].post.requestBody.content[
      'application/json'
    ].schema.$ref,
    /ShellAuthPageCommand$/,
  );
});

test('maximal compact network key publication fits the existing 64 KiB transport budget', (t) => {
  const kid = 'k'.repeat(128),
    time = '2026-11-13T00:00:00Z',
    signature = 'A'.repeat(86),
    version = '18446744073709551615';
  const keyset = {
    keyset: {
      schema_version: 1,
      realm: version,
      keyset_version: version,
      issued_at: time,
      valid_until: '2026-11-14T00:00:00Z',
      keys: Array.from({ length: 128 }, (_, i) => ({
        kid: String(i).padStart(3, '0') + kid.slice(3),
        alg: 'Ed25519',
        purpose: 'business_delivery',
        state: 'retired',
        not_before: time,
        not_after: '2026-11-14T00:00:00Z',
        public_key_spki: 'A'.repeat(59),
      })),
    },
    root_auth: { alg: 'Ed25519', kid, signature },
  };
  const announcement = {
    keyset_version: version,
    manifest_digest: 'sha256:' + 'a'.repeat(64),
    valid_until: time,
    root_kid: kid,
    root_signature: signature,
  };
  for (const [name, data, file, definition] of [
    ['keyset', keyset, 'network/keyset.schema.json', 'SignedKeyset'],
    [
      'announcement',
      announcement,
      'network/protocol.schema.json',
      'KeysetAnnouncement',
    ],
  ]) {
    const valid = validate(file, definition);
    assert.equal(valid(data), true, JSON.stringify(valid.errors));
    const bytes = Buffer.byteLength(JSON.stringify(data));
    assert.ok(bytes <= 65536, name + ':' + bytes);
    t.diagnostic(
      name +
        ' compact UTF-8 bytes: ' +
        bytes +
        ' / 65536; synthetic signature, size validation only',
    );
  }
});

test('K2 device navigation resolves only public user code at the internal IAM port', () => {
  const valid = validate(
    'auth/uac-control.schema.json',
    'UacDeviceNavigationIssueRequest',
  );
  const good = {
    requestId: id,
    providerId: id,
    origin: 'https://portal.example.com',
    continuation: 'device',
    userCode: 'BCDF-GHJK',
    fetchMetadata: {},
  };
  assert.equal(valid(good), true);
  for (const patch of [
    { userCode: 'INVALID' },
    { userCode: '' },
    { deviceGrantId: id },
    { continuation: 'portal' },
    { token: 'secret' },
    { tenantId: id },
  ])
    assert.equal(valid({ ...good, ...patch }), false);
  const missing = { ...good };
  delete missing.userCode;
  assert.equal(valid(missing), false);
  const spec = YAML.parse(read('openapi/iam.yaml'));
  const op = spec.paths['/internal/uac/device-navigation-proofs'].post;
  assert.equal(op['x-internal-only'], true);
  assert.equal(op['x-service-caller'], 'svc:edge');
  assert.deepEqual(op.security, [{ internalServiceAuth: [] }]);
  assert.ok(op.responses['404']);
  for (const item of Object.values(spec.paths))
    for (const op of Object.values(item))
      for (const response of Object.values(op.responses ?? {})) {
        if (response.headers?.['X-Auriga-Correlation-Id'])
          assert.equal(
            response.headers['X-Auriga-Correlation-Id'].$ref,
            '#/components/headers/UacCorrelationId',
          );
      }
  for (const route of [
    '/login/uac/sso',
    '/internal/uac/device-navigation-proofs',
  ])
    for (const response of Object.values(spec.paths[route].post.responses))
      assert.ok(response.headers['X-Auriga-Correlation-Id']);
  const header = ajv.compile(spec.components.headers.UacCorrelationId.schema);
  assert.equal(header(id), true);
  assert.equal(header('token'), false);
});

test('K2 device attempt has bounded public projection and is forbidden in terminal snapshots', () => {
  const value = {
    attemptId: id,
    pageOwner: id,
    userCode: 'BCDF-GHJK',
    expiresAt: base.expiresAt,
    verificationRequired: true,
  };
  const attempt = validate(
    'plugin/shell-auth.schema.json',
    'ShellAuthDeviceAttempt',
  );
  assert.equal(attempt(value), true);
  for (const patch of [
    { verificationRequired: false },
    { expiresAt: 'tomorrow' },
    { userCode: 'bad' },
    { pageOwner: 'bad' },
    { device_code: 'secret' },
    { token: 'secret' },
    { url: 'https://evil.example.com' },
  ])
    assert.equal(attempt({ ...value, ...patch }), false);
  for (const key of Object.keys(value)) {
    const missing = { ...value };
    delete missing[key];
    assert.equal(attempt(missing), false);
  }
  assert.equal(
    snapshot({ ...base, state: 'authenticating', deviceAttempt: value }),
    true,
  );
  for (const state of [
    'signed_out',
    'authenticated',
    'revoked',
    'unreachable',
    'reauth_required',
  ])
    assert.equal(snapshot({ ...base, state, deviceAttempt: value }), false);
});

test('K2 migration gate allows unrelated edits and removal but rejects new or remaining direct transport', () => {
  const legacy = {
    migrationState: 'legacy',
    calls: [{ file: 'x.js', evidence: "fetch('/api/iam/v1/me')" }],
  };
  assert.doesNotThrow(() =>
    checkPlatformMigration(legacy, operations, {
      'x.js': "// changed comment\nfetch('/api/iam/v1/me')",
    }),
  );
  assert.doesNotThrow(() =>
    checkPlatformMigration(legacy, operations, { 'x.js': '// removed' }),
  );
  assert.throws(() =>
    checkPlatformMigration(legacy, operations, {
      'x.js': "fetch('/api/ai/v1/new')",
    }),
  );
  const broker = { migrationState: 'broker', calls: [] };
  assert.doesNotThrow(() =>
    checkPlatformMigration(broker, operations, {
      'x.js': "broker.call('profile_read', {})",
    }),
  );
  for (const source of [
    'fetch(dynamicUrl)',
    'new WebSocket(url)',
    "import http from 'node:http'",
    'broker.call(operation, {})',
    "broker.call('invented', {})",
    "broker.call('profile_read' + suffix, {})",
  ])
    assert.throws(
      () => checkPlatformMigration(broker, operations, { 'x.js': source }),
      source,
    );
  assert.throws(() =>
    checkPlatformMigration({ ...broker, calls: legacy.calls }, operations, {}),
  );
});

test('K2 broker migration preserves actual documented local, managed and personal channels', () => {
  const inventory = JSON.parse(read('plugin/shell-auth-call-sites.json'));
  const sources = readPluginSources();
  for (const file of [
    'managed/heartbeat.js',
    'managed/host-auth.js',
    'managed/keys.js',
    'identity/oidc-http.js',
    'local/host.js',
    'client.js',
    'settings/view.js',
    'ai/transport.js',
  ])
    assert.ok(
      inventory.retainedChannels.some((channel) => channel.file === file),
      file,
    );
  const retained = Object.fromEntries(
    inventory.retainedChannels.map((channel) => {
      assert.ok(
        channel.reason &&
          channel.design.includes('docs/design/auth-shell.zh.md'),
        channel.file,
      );
      assert.ok(Object.hasOwn(sources, channel.file), channel.file);
      return [channel.file, sources[channel.file]];
    }),
  );
  const broker = { ...inventory, migrationState: 'broker', calls: [] };
  assert.doesNotThrow(() =>
    checkPlatformMigration(broker, operations, retained),
  );
  // 迁移后结构夹具：仅以broker替身替换D待迁的旧平台文件及NATS传输，
  // 其它真实插件文件完整保留，证明合法原代码不会令最终状态不可达。
  const migrated = { ...sources };
  const retainedFiles = new Set(
    inventory.retainedChannels.map((channel) => channel.file),
  );
  for (const call of inventory.calls)
    if (!retainedFiles.has(call.file))
      migrated[call.file] = '// platform branches migrated';
  migrated['network/transport/ws-socket.js'] = '// broker-owned NATS transport';
  assert.doesNotThrow(() =>
    checkPlatformMigration(broker, operations, migrated),
  );
  // 纯协议字段、文案、类型注释和个人流处理不再因为出现fetch/WS字样被误拦。
  assert.doesNotThrow(() =>
    checkPlatformMigration(broker, operations, {
      ...retained,
      'network/protocol.js':
        "const frame = {type:'fetch'}; // WebSocket\n/** @type {import('node:http').Server} */",
    }),
  );
  for (const source of [
    "fetch('/api/iam/v1/me')",
    'fetch(computedUrl)',
    "import http from 'node:https'",
    'new WebSocket(url)',
  ])
    assert.throws(
      () =>
        checkPlatformMigration(broker, operations, {
          ...retained,
          'unlisted.js': source,
        }),
      /direct/,
      source,
    );
  // 白名单不是整文件豁免：心跳文件不能新增成员API，也不能新增其它传输。
  assert.throws(
    () =>
      checkPlatformMigration(broker, operations, {
        'managed/heartbeat.js': "fetch('/api/iam/v1/me')",
      }),
    /direct platform route/,
  );
  assert.throws(
    () =>
      checkPlatformMigration(broker, operations, {
        'managed/heartbeat.js': 'new WebSocket(url)',
      }),
    /direct transport/,
  );
  assert.throws(
    () =>
      checkPlatformMigration(broker, operations, {
        'client.js': "fetch('/api/org/v1/members')",
      }),
    /direct platform route/,
  );
  assert.throws(
    () =>
      checkPlatformMigration(
        { ...broker, retainedChannels: [{ file: 'unlisted.js' }] },
        operations,
        {},
      ),
    /invalid retained channel/,
  );
});

test('K2 legacy normalizes literal route formatting and documents dynamic-analysis limits', () => {
  const legacy = {
    migrationState: 'legacy',
    calls: [{ file: 'x.js', evidence: "fetch('/api/iam/v1/me')" }],
  };
  assert.doesNotThrow(() =>
    checkPlatformMigration(legacy, operations, {
      'x.js': '// comment\nfetch (\n  "/api/iam/v1/me",\n {}\n)',
    }),
  );
  assert.throws(
    () =>
      checkPlatformMigration(legacy, operations, {
        'x.js': "fetch('/api/iam/v1/new')",
      }),
    /new direct/,
  );
  const dynamic = "fetch('/api/' + family + '/v1/new')";
  // legacy不执行常量折叠或方法/调用数分析；必须在文档中明示，不宣称它证明迁移完成。
  assert.doesNotThrow(() =>
    checkPlatformMigration(legacy, operations, { 'x.js': dynamic }),
  );
  assert.throws(
    () =>
      checkPlatformMigration(
        { migrationState: 'broker', calls: [] },
        operations,
        { 'x.js': dynamic },
      ),
    /direct transport/,
  );
  const policy = read('plugin/shell-auth-migration.md');
  for (const term of ['动态拼接', '方法', 'broker', '固定接缝'])
    assert.ok(policy.includes(term));
});

test('K2 fixed broker seam forbids common renaming and checks the operation whitelist', () => {
  const broker = { migrationState: 'broker', calls: [] };
  assert.doesNotThrow(() =>
    checkPlatformMigration(broker, operations, {
      'x.js': "const result = broker . call ( 'profile_read', {} )",
    }),
  );
  for (const source of [
    "const bridge=broker; bridge.call('invented',{})",
    "import {broker as bridge} from './adapter.js'",
    'const {call}=broker',
    "broker.call('invented',{})",
  ])
    assert.throws(() =>
      checkPlatformMigration(broker, operations, { 'x.js': source }),
    );
  // 无来源信息的任意对象不是可识别的broker；该静态局限不冒充类型/导入溯源。
  assert.doesNotThrow(() =>
    checkPlatformMigration(broker, operations, {
      'x.js': "other.call('not-a-broker-method')",
    }),
  );
});

test('K2 anonymous CSRF bootstrap remains bounded and internal only', () => {
  const issued = validate(
    'auth/uac-control.schema.json',
    'UacNavigationIssued',
  );
  const response = {
    context: {
      navigationProof: 'A'.repeat(43),
      expiresAt: base.expiresAt,
      transactionId: id,
      purpose: 'portal',
      autoEligible: false,
      autoSuppressed: true,
    },
    transactionCookie: 'B'.repeat(43),
    ttlSeconds: 120,
    csrfCookie: 'C'.repeat(43),
  };
  assert.equal(issued(response), true);
  for (const csrfCookie of ['', 'short', '+'.repeat(43), 'D'.repeat(44)])
    assert.equal(issued({ ...response, csrfCookie }), false);
  assert.equal(issued({ ...response, accessToken: 'secret' }), false);
  const policy = read('auth/navigation-cookie-policy.md');
  for (const value of [
    '__Host-auriga_portal_sso_suppression',
    'HttpOnly',
    'SameSite=Lax',
    'X-Auriga-CSRF',
    '120 秒',
    '终态',
  ])
    assert.ok(policy.includes(value));
});

test('K2 host registration hands back broker-owned handles without accepting caller ownership', () => {
  const command = validate(
    'plugin/shell-auth-control.schema.json',
    'ShellAuthHostContextCommand',
  );
  const envelope = {
    protocolVersion: 1,
    requestId: id,
    shellEpoch: id,
    expectedRevision: 0,
    expiresAt: base.expiresAt,
  };
  const context = {
    kind: 'session',
    hostSessionId: 'synthetic-host-session',
    expiresAt: base.expiresAt,
  };
  const register = { ...envelope, action: 'register', context };
  assert.equal(command(register), true);
  assert.equal(
    command({
      ...register,
      context: {
        ...context,
        kind: 'tool',
        parentExecutionContextId: id,
        toolCallId: 'call',
        rootCallId: 'root',
      },
    }),
    true,
  );
  for (const patch of [
    { executionContextId: id },
    { ownerId: id },
    { peerRole: 'plugin-backend' },
    { token: 'synthetic' },
    { url: 'https://example.com' },
  ])
    assert.equal(command({ ...register, ...patch }), false);
  assert.equal(
    command({ ...register, context: { ...context, kind: 'tool' } }),
    false,
  );
  assert.equal(
    command({ ...register, context: { ...context, endpointId: id } }),
    false,
  );
  assert.equal(
    command({ ...envelope, action: 'revoke', executionContextId: id }),
    true,
  );
  assert.equal(
    command({ ...register, action: 'revoke', executionContextId: id }),
    false,
  );
  const result = validate(
    'plugin/shell-auth-control.schema.json',
    'ShellAuthHostContextResult',
  );
  const response = {
    protocolVersion: 1,
    requestId: id,
    shellEpoch: id,
    revision: 0,
    action: 'register',
    context: {
      executionContextId: id,
      ownerId: id,
      hostSessionId: context.hostSessionId,
      kind: 'session',
      expiresAt: base.expiresAt,
    },
  };
  assert.equal(result(response), true);
  assert.equal(
    result({
      ...response,
      error: {
        code: 'common.forbidden',
        reason: 'owner_mismatch',
        retryable: false,
      },
    }),
    false,
  );
  const spec = YAML.parse(read('openapi/shell-auth.yaml'));
  for (const path of ['/host/contexts', '/host/credentials']) {
    assert.equal(spec.paths[path].post['x-local-ipc-only'], true);
    assert.equal(spec.paths[path].post['x-peer-role'], 'plugin-backend');
  }
  const page = validate(
    'plugin/shell-auth-control.schema.json',
    'ShellAuthPageCommand',
  );
  for (const action of ['register', 'revoke', 'read_credentials'])
    assert.equal(page({ ...envelope, action }), false);
});

test('K2 host enterprise reads are identity-bound, purpose-limited and never expose platform credentials', () => {
  const request = validate(
    'plugin/shell-auth-control.schema.json',
    'ShellAuthHostCredentialsRequest',
  );
  const input = {
    protocolVersion: 1,
    requestId: id,
    shellEpoch: id,
    expectedRevision: 0,
    expiresAt: base.expiresAt,
    executionContextId: id,
    purpose: 'raw',
  };
  assert.equal(request(input), true);
  assert.equal(request({ ...input, purpose: 'outbound' }), true);
  for (const patch of [
    { purpose: 'model' },
    { empNo: 'other' },
    { tenantId: id },
    { sourceSessionId: id },
    { ownerId: id },
    { accessToken: 'synthetic' },
  ])
    assert.equal(request({ ...input, ...patch }), false);
  const result = validate(
    'plugin/shell-auth-control.schema.json',
    'ShellAuthHostCredentialsResult',
  );
  const credentials = {
    binding: {
      tenantId: id,
      memberId: id,
      deviceId: id,
      sourceSessionId: id,
      providerId: id,
      authGeneration: 3,
      expiresAt: base.expiresAt,
    },
    credentialVersion: 1,
    empNo: 'synthetic-employee',
    token: 'synthetic-enterprise-value',
    updatedAt: base.expiresAt,
  };
  const response = {
    protocolVersion: 1,
    requestId: id,
    shellEpoch: id,
    revision: 0,
    executionContextId: id,
    purpose: 'raw',
    credentials,
  };
  assert.equal(result(response), true);
  assert.equal(
    result({
      ...response,
      credentials: {
        ...credentials,
        credentialVersion: 2,
        token: 'synthetic-rotated-value',
      },
    }),
    true,
  );
  for (const patch of [
    { access_token: 'synthetic' },
    { refresh_token: 'synthetic' },
    { cookies: { x: 3 } },
    { binding: { ...credentials.binding, authGeneration: 0 } },
  ])
    assert.equal(
      result({ ...response, credentials: { ...credentials, ...patch } }),
      false,
    );
  const missing = { ...credentials };
  delete missing.binding;
  assert.equal(result({ ...response, credentials: missing }), false);
  const error = {
    code: 'common.forbidden',
    reason: 'raw_token_disabled',
    retryable: false,
  };
  assert.equal(result({ ...response, credentials: undefined, error }), true);
  assert.equal(result({ ...response, error }), false);
  assert.equal(
    result({
      ...response,
      credentials: undefined,
      error: { ...error, retryable: true },
    }),
    false,
  );
  assert.equal(
    result({
      ...response,
      credentials: undefined,
      error: { ...error, token: 'synthetic' },
    }),
    false,
  );
});

test('K2 authority vectors cover owner, epoch, expiry, logout, rotation and raw shutdown', () => {
  const fixture = JSON.parse(read('test/auth-k2-host-fixtures.json'));
  const accepts = ajv.compile(fixture.acceptanceSchema);
  for (const vector of fixture.vectors)
    assert.equal(accepts(vector.input), vector.accepted, vector.name);
});

test('K2 standard devices have a fixed AI resource, token-possession revocation and device-only identity', () => {
  const refresh = validate(
    'auth/standard-device.schema.json',
    'StandardDeviceRefreshRequest',
  );
  const input = {
    grant_type: 'refresh_token',
    client_id: 'auriga-dsh',
    refresh_token: 'synthetic-refresh',
  };
  assert.equal(refresh(input), true);
  assert.equal(refresh({ ...input, resource: 'urn:auriga:ai-gateway' }), true);
  for (const patch of [
    { resource: 'auriga.ai-gateway' },
    { resource: ['urn:auriga:ai-gateway'] },
    { audience: 'auriga' },
    { deviceId: id },
    { uacSessionBinding: {} },
    { grant_type: 'token-exchange' },
  ])
    assert.equal(refresh({ ...input, ...patch }), false);
  const revoke = validate(
    'auth/standard-device.schema.json',
    'StandardDeviceRevokeRequest',
  );
  assert.equal(
    revoke({
      client_id: 'auriga-dsh',
      token: 'synthetic-access',
      token_type_hint: 'access_token',
    }),
    true,
  );
  assert.equal(
    revoke({
      client_id: 'auriga-dsh',
      token: 'synthetic-refresh',
      token_type_hint: 'refresh_token',
    }),
    true,
  );
  assert.equal(
    revoke({
      client_id: 'auriga-dsh',
      token: 'synthetic-access',
      deviceId: id,
    }),
    false,
  );
  const claims = validate(
    'auth/standard-device.schema.json',
    'StandardDeviceAiClaims',
  );
  const jwt = {
    iss: 'https://portal.example.com',
    sub: id,
    tid: id,
    did: id,
    jti: id,
    aud: 'auriga.ai-gateway',
    ptyp: 'device',
    ctx: 'device',
    scope: 'auriga.connect',
    iat: 1,
    exp: 301,
    perms: ['auriga.connect.collab.use'],
    mtyp: 'member',
  };
  assert.equal(claims(jwt), true);
  assert.equal(
    claims({ ...jwt, perms: ['auriga.connect.models.invoke'] }),
    true,
  );
  for (const patch of [
    { aud: 'auriga' },
    { aud: ['auriga', 'auriga.ai-gateway'] },
    { perms: ['iam.accounts.write'] },
    { perms: [] },
    { sid: id },
    { ctx: 'portal' },
  ])
    assert.equal(claims({ ...jwt, ...patch }), false);
  const spec = YAML.parse(read('openapi/iam.yaml'));
  assert.ok(spec.paths['/me/device-session'].get.responses['200']);
  const deviceIdentity = validate(
    'auth/standard-device.schema.json',
    'StandardDeviceIdentity',
  );
  const identity = {
    accountId: id,
    tenantId: id,
    memberId: id,
    deviceId: id,
    providerId: id,
    clientId: 'auriga-dsh',
    authorizationMode: 'standard_device',
    expiresAt: base.expiresAt,
  };
  assert.equal(deviceIdentity(identity), true);
  for (const patch of [
    { authorizationMode: 'native_tx' },
    { access_token: 'synthetic' },
    { session: { id } },
    { uacSessionBinding: { sourceSessionId: id } },
  ])
    assert.equal(deviceIdentity({ ...identity, ...patch }), false);
  assert.equal(
    spec.paths['/me'].get.responses['200'].content['application/json'].schema
      .$ref,
    '#/components/schemas/Me',
  );
  const proto = read('proto/auriga/iam/v1/iam.proto');
  assert.match(proto, /rpc VerifyStandardDeviceAiAccess\(/);
});
