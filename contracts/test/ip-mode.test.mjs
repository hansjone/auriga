import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync, mkdirSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { Buffer } from 'node:buffer';
import { fileURLToPath } from 'node:url';
import path from 'node:path';
import Ajv2020 from '../../services/contracts/node_modules/ajv/dist/2020.js';
import addFormats from '../../services/contracts/node_modules/ajv-formats/dist/index.js';

const root = fileURLToPath(new URL('../../', import.meta.url));
const read = (p) => JSON.parse(readFileSync(path.join(root, p), 'utf8'));
const fixture = (p) => read('contracts/deployment/fixtures/' + p);
const ajv = new Ajv2020({ strict: true, allErrors: true });
addFormats(ajv);
const access = ajv.compile(read('contracts/deployment/access.schema.json'));
const session = ajv.compile(read('contracts/kv/session.schema.json'));
function changed(c) {
  const value = fixture(c.base);
  for (const patch of [c, ...(c.additionalChanges ?? [])]) {
    const names = patch.pointer.split('.');
    const leaf = names.pop();
    const target = names.reduce((v, name) => v[name], value);
    if (patch.remove) delete target[leaf];
    else target[leaf] = JSON.parse(JSON.stringify(patch.value));
  }
  return value;
}

for (const file of [
  'ip-http.json',
  'domain-proxy.json',
  'domain-root-portal.json',
  'domain-nested-parent.json',
]) {
  test('受信入口结构正例：' + file, () => {
    assert.equal(access(fixture(file)), true, JSON.stringify(access.errors));
  });
}
for (const field of read('contracts/deployment/access.schema.json').required) {
  test('受信入口必填字段：' + field, () => {
    const value = fixture('ip-http.json');
    delete value[field];
    assert.equal(access(value), false, field);
  });
}
for (const c of fixture('schema-negative.json')) {
  test('schema拒绝：' + c.name, () => {
    assert.equal(access(changed(c)), false, c.name);
  });
}
test('合法嵌套域名样本保持不同Origin，不被派生实例host命名空间捕获', () => {
  // Fixture topology only: this is not a production startup validator or a
  // browser cookie/SameSite test. Runtime acceptance remains an IP1/IP3 gate.
  const id = '019a0000-0000-7000-8000-000000000001';
  for (const file of ['domain-root-portal.json', 'domain-nested-parent.json']) {
    const value = fixture(file);
    const parent = value.instances.parentDomain;
    assert(parent.endsWith('.' + value.portalOrigin.host));
    const origin = ({ scheme, host, port }) =>
      new URL(scheme + '://' + host + ':' + port).origin;
    const instanceOrigin = origin({
      ...value.instances,
      host: id + '.' + parent,
    });
    assert.notEqual(origin(value.portalOrigin), instanceOrigin);
    assert.notEqual(origin(value.adminOrigin), instanceOrigin);
    assert.notEqual(origin(value.portalOrigin), origin(value.adminOrigin));
    for (const fixed of [value.portalOrigin.host, value.adminOrigin.host]) {
      assert.notEqual(fixed, parent);
      assert.equal(fixed.endsWith('.' + parent), false);
    }
  }
  for (const name of [
    'portal-captured-by-instance-host',
    'admin-captured-by-instance-host',
  ]) {
    const c = fixture('startup-negative.json').find(
      (item) => item.name === name,
    );
    const value = changed(c);
    assert.deepEqual(value.issuerOrigin, value.portalOrigin);
    const fixed = c.pointer.startsWith('portal')
      ? value.portalOrigin.host
      : value.adminOrigin.host;
    assert.equal(fixed, id + '.' + value.instances.parentDomain);
  }
});
test('明确schema无法比较的12项启动负例，留给IP1验证器执行', () => {
  const cases = fixture('startup-negative.json');
  assert.equal(cases.length, 12);
  for (const c of cases) {
    // This is intentionally NOT a runtime rejection test. These values pass
    // structural validation and MUST fail the documented startup checks.
    assert.equal(access(changed(c)), true, JSON.stringify(access.errors));
    assert(Number.isInteger(c.rule) && c.rule >= 1 && c.rule <= 6);
  }
});

const output = path.join(root, 'services/contracts/target/ip0-iam.json');
mkdirSync(path.dirname(output), { recursive: true });
const bundled = spawnSync(
  process.execPath,
  [
    path.join(root, 'services/contracts/node_modules/@redocly/cli/bin/cli.js'),
    'bundle',
    path.join(root, 'contracts/openapi/iam.yaml'),
    '--config',
    path.join(root, 'contracts/redocly.yaml'),
    '--output',
    output,
  ],
  { encoding: 'utf8' },
);
if (bundled.error) throw bundled.error;
assert.equal(bundled.status, 0, bundled.stdout + bundled.stderr);
const iam = JSON.parse(readFileSync(output, 'utf8'));
function refs(value) {
  if (Array.isArray(value)) return value.map(refs);
  if (!value || typeof value !== 'object') return value;
  return Object.fromEntries(
    Object.entries(value).map(([name, child]) => [
      name,
      name === '$ref'
        ? child.replace('#/components/schemas/', '#/$defs/')
        : refs(child),
    ]),
  );
}
const csrfSchema = {
  $id: 'urn:auriga:ip0:csrf',
  $defs: Object.fromEntries(
    ['AdminCsrfTokenRegistration', 'AdminCsrfToken'].map((name) => [
      name,
      refs(iam.components.schemas[name]),
    ]),
  ),
};
ajv.addSchema(csrfSchema);
const request = ajv.getSchema(
  csrfSchema.$id + '#/$defs/AdminCsrfTokenRegistration',
);
const response = ajv.getSchema(csrfSchema.$id + '#/$defs/AdminCsrfToken');
const csrf = fixture('admin-csrf.json');
test('实际CSRF JSON正负例：32字节规范token与到期必填，无目标会话/额外字段', () => {
  assert.equal(request(csrf.request), true, JSON.stringify(request.errors));
  assert.equal(response(csrf.response), true, JSON.stringify(response.errors));
  assert.equal(Buffer.from(csrf.request.token, 'base64url').length, 32);
  for (const invalid of [
    {},
    { token: '' },
    { token: csrf.request.token + '=' },
    { token: csrf.request.token.slice(1) },
    { token: csrf.request.token.slice(0, -1) + 'B' },
    { token: 1 },
    { token: csrf.request.token, sessionId: 'other-session' },
    { token: csrf.request.token, audience: 'admin' },
  ])
    assert.equal(request(invalid), false, JSON.stringify(invalid));
  for (const invalid of [
    { token: csrf.response.token },
    { expiresAt: csrf.response.expiresAt },
    { ...csrf.response, expiresAt: 'not-a-date' },
    { ...csrf.response, csrfHash: 'not-response-data' },
  ])
    assert.equal(response(invalid), false, JSON.stringify(invalid));
});
test('精确bootstrap仅POST/当前admin cookie/AuthenticatedOnly+AdminOnly，JSON与非简单头不可缺', () => {
  const operations = iam.paths['/admin/session/csrf-tokens'];
  assert.deepEqual(Object.keys(operations), ['post']);
  const op = operations.post;
  assert.equal(op.operationId, 'registerAdminSessionCsrfToken');
  for (const flag of [
    'x-admin-only',
    'x-authenticated-only',
    'x-browser-session-only',
    'x-csrf-bootstrap',
  ])
    assert.equal(op[flag], true);
  assert.equal(op['x-permission'], undefined);
  assert.deepEqual(op.security, [{ adminSessionCookie: [] }]);
  assert.equal(iam.components.securitySchemes.adminSessionCookie.in, 'cookie');
  assert.equal(
    iam.components.securitySchemes.adminSessionCookie.name,
    'auriga_admin_session',
  );
  const header = op.parameters.find(
    (p) => p.name === 'X-Auriga-CSRF-Bootstrap',
  );
  assert.equal(header.in, 'header');
  assert.equal(header.required, true);
  assert.deepEqual(header.schema.enum, ['admin-session-v1']);
  assert.equal(op.requestBody.required, true);
  assert.deepEqual(Object.keys(op.requestBody.content), ['application/json']);
  assert.deepEqual(Object.keys(op.responses['200'].content), [
    'application/json',
  ]);
  assert.deepEqual(op.responses['200'].headers['Cache-Control'].schema.enum, [
    'no-store',
  ]);
  assert.deepEqual(
    op.responses['200'].headers['X-Content-Type-Options'].schema.enum,
    ['nosniff'],
  );
  assert.equal(op.responses['200'].headers['Set-Cookie'], undefined);
  assert(
    !Object.keys(op.responses['200'].headers).some((h) =>
      h.toLowerCase().startsWith('access-control-'),
    ),
  );
  assert.deepEqual(Object.keys(op.responses).sort(), [
    '200',
    '400',
    '401',
    '403',
    '429',
    '503',
  ]);
  assert(op['x-error-codes'].every((code) => code.startsWith('common.')));
  let bootstrapCount = 0;
  for (const item of Object.values(iam.paths)) {
    for (const operation of Object.values(item)) {
      if (operation?.['x-csrf-bootstrap']) bootstrapCount++;
    }
  }
  assert.equal(bootstrapCount, 1);
});

const uuid = '019a0000-0000-7000-8000-000000000001';
const kv = {
  sessionId: uuid,
  accountId: uuid,
  tenantId: uuid,
  audience: 'admin',
  accessToken: 'test-only',
  accessTokenExpiresAt: '2026-10-22T01:05:00Z',
  expiresAt: '2026-10-22T09:00:00Z',
  idleExpiresAt: '2026-10-22T01:30:00Z',
  csrfHash: 'unused-in-ip-admin',
  mfa: true,
  authTime: '2026-10-22T01:00:00Z',
};
const hash = createHash('sha256')
  .update(csrf.request.token, 'utf8')
  .digest('hex');
const slot = { hash, expiresAt: csrf.response.expiresAt };
test('KV附加投影只存hash/到期，最多32条，仅admin；不改变原门户必填契约', () => {
  assert.equal(session({ ...kv, audience: 'portal' }), true);
  assert.equal(session({ ...kv, adminCsrfTokens: [] }), true);
  assert.equal(session({ ...kv, adminCsrfTokens: [slot] }), true);
  const slots = Array.from({ length: 32 }, (_, i) => ({
    ...slot,
    hash: createHash('sha256')
      .update('test-slot-' + i)
      .digest('hex'),
  }));
  assert.equal(session({ ...kv, adminCsrfTokens: slots }), true);
  for (const invalid of [
    { ...kv, audience: 'portal', adminCsrfTokens: [slot] },
    { ...kv, adminCsrfTokens: [...slots, slot] },
    { ...kv, adminCsrfTokens: [{ ...slot, hash: csrf.request.token }] },
    { ...kv, adminCsrfTokens: [{ ...slot, token: csrf.request.token }] },
    { ...kv, adminCsrfTokens: [{ hash }] },
    { ...kv, adminCsrfTokens: [{ ...slot, expiresAt: 900 }] },
  ])
    assert.equal(session(invalid), false, JSON.stringify(invalid));
});
