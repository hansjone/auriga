import test from 'node:test';
import assert from 'node:assert/strict';
import {
  assertReady,
  assertEntry,
  assertManaged,
  totp,
  validateAccess,
  sharedNodeReady,
  loginOutcome,
  totpNotBefore,
  otpField,
  browserPortalOrigin,
  assertAdminInvitation,
} from './checks.mjs';

test('formal NAS node selection uses the explicitly configured template label', () => {
  const node = {
    status: 'online',
    schedulable: true,
    drivers: ['docker'],
    labels: { template: 'real-dsh' },
  };
  assert.equal(sharedNodeReady(node, 'real-dsh'), true);
  assert.equal(sharedNodeReady(node), false);
});

test('invitations created from the direct admin entry use the default portal, then public login is independent', () => {
  const ip = {
    mode: 'ip',
    portalOrigin: { scheme: 'http', host: '192.0.2.10', port: 8080 },
  };
  const invitation = 'http://192.0.2.10:8080/invite/example-code';
  assert.equal(assertAdminInvitation(invitation, ip), 'http://192.0.2.10:8080');
  for (const url of [
    'https://portal.example.com/invite/example-code',
    'http://attacker.example.com/invite/example-code',
    invitation + '?token=private',
    invitation + '#private',
    'http://alice@192.0.2.10:8080/invite/example-code',
  ])
    assert.throws(() => assertAdminInvitation(url, ip));
});

test('authenticator enrollment and team login use their declared OTP fields', () => {
  assert.equal(otpField({ fields: [{ name: 'otp', kind: 'otp' }] }), 'otp');
  assert.equal(otpField({ fields: [{ name: 'code', kind: 'otp' }] }), 'code');
  assert.throws(() =>
    otpField({ fields: [{ name: 'password', kind: 'password' }] }),
  );
  assert.throws(() =>
    otpField({
      fields: [
        { name: 'code', kind: 'otp' },
        { name: 'otp', kind: 'otp' },
      ],
    }),
  );
});

test('TOTP scheduling avoids an expired window and counters consumed by enrollment', () => {
  assert.equal(totpNotBefore(61_000), 61_000);
  assert.equal(totpNotBefore(89_501), 90_100);
  assert.equal(totpNotBefore(61_000, 2), 90_100);
  assert.equal(totpNotBefore(61_000, 3), 120_100);
});

test('login outcome evidence retains only safe state, error code and trace id', () => {
  const trace = 'a'.repeat(32);
  const tx = {
    status: 'pending',
    token: 'secret-token',
    step: {
      type: 'form',
      error: { code: 'iam.invalid_credentials', detail: 'secret-detail' },
      fields: [{ value: 'secret-password' }],
    },
  };
  assert.deepEqual(loginOutcome(tx, trace), {
    status: 'pending',
    step: 'form',
    code: 'iam.invalid_credentials',
    traceId: trace,
  });
  assert.deepEqual(
    loginOutcome(
      {
        status: 'secret-status',
        step: { type: 'secret-step', code: 'secret-code' },
      },
      'secret-trace',
    ),
    { status: 'unknown', step: 'unknown' },
  );
});

test('shared runs require an online schedulable Docker node with the real template', () => {
  const node = {
    status: 'online',
    schedulable: true,
    drivers: ['docker'],
    labels: { template: 'real-dsh-e2e' },
  };
  assert.equal(sharedNodeReady(node), true);
  for (const changed of [
    { status: 'offline' },
    { schedulable: false },
    { drivers: ['linux_process'] },
    { labels: { template: 'fake-dsh-e2e' } },
  ])
    assert.equal(sharedNodeReady({ ...node, ...changed }), false);
});

const instance = {
  id: '01900000-0000-7000-8000-000000000001',
  ownerId: 'alice',
  ready: true,
  phase: 'running',
};
const access = {
  mode: 'domain',
  portalOrigin: { scheme: 'http', host: 'portal.example.com', port: 8080 },
  adminOrigin: { scheme: 'http', host: 'admin.example.com', port: 8084 },
  instances: {
    mode: 'subdomain',
    scheme: 'http',
    parentDomain: 'i.example.com',
    port: 8080,
  },
};

test('IP reverse proxy presentation requires an explicit exact upstream and HTTPS public origin', () => {
  const ip = {
    ...access,
    mode: 'ip',
    portalOrigin: { scheme: 'http', host: '192.0.2.10', port: 8080 },
    instances: { mode: 'path' },
  };
  const proxy = {
    upstreamOrigin: 'http://192.0.2.10:8080',
    publicOrigin: 'https://portal.example.com',
  };
  assert.equal(
    browserPortalOrigin(ip, proxy.publicOrigin, proxy),
    proxy.publicOrigin,
  );
  assert.equal(
    browserPortalOrigin(ip, proxy.upstreamOrigin),
    proxy.upstreamOrigin,
  );
  assert.throws(() => browserPortalOrigin(ip, proxy.publicOrigin));
  for (const changed of [
    { upstreamOrigin: 'http://192.0.2.11:8080' },
    { publicOrigin: 'http://portal.example.com' },
    { publicOrigin: 'https://alice@portal.example.com' },
    { publicOrigin: proxy.publicOrigin + '/other' },
    { publicOrigin: proxy.publicOrigin + '?token=private' },
    { publicOrigin: proxy.publicOrigin + '#private' },
  ])
    assert.throws(() =>
      browserPortalOrigin(ip, proxy.publicOrigin, { ...proxy, ...changed }),
    );
  assert.throws(() => browserPortalOrigin(access, proxy.publicOrigin, proxy));
  assert.throws(() =>
    browserPortalOrigin(ip, 'https://other.example.com', proxy),
  );
  assertEntry(
    `${proxy.publicOrigin}/i/${instance.id}/`,
    ip,
    instance.id,
    proxy.publicOrigin,
  );
  assert.throws(() =>
    assertEntry(
      `${proxy.upstreamOrigin}/i/${instance.id}/`,
      ip,
      instance.id,
      proxy.publicOrigin,
    ),
  );
});

test('READY requires running, ready and the signed-in member as owner', () => {
  assertReady(instance, 'alice');
  assertReady({ ...instance, phase: 'pending_restart' }, 'alice');
  for (const changed of [
    { ready: false },
    { phase: 'starting' },
    { ownerId: 'bob' },
    { id: '' },
  ])
    assert.throws(() => assertReady({ ...instance, ...changed }, 'alice'));
});

test('entry is bound to the exact configured instance origin and path', () => {
  const url = `http://${instance.id}.i.example.com:8080/`;
  assertEntry(url, access, instance.id);
  for (const bad of [
    url.replace('8080', '8081'),
    url.replace('http:', 'https:'),
    url.replace(instance.id, 'other'),
    url + '?ticket=secret',
    url.replace('http://', 'http://alice@'),
  ])
    assert.throws(() => assertEntry(bad, access, instance.id));
  const ip = {
    ...access,
    mode: 'ip',
    portalOrigin: { scheme: 'http', host: '192.0.2.10', port: 8080 },
    instances: { mode: 'path' },
  };
  assertEntry(`http://192.0.2.10:8080/i/${instance.id}/`, ip, instance.id);
  assert.throws(() => assertEntry('http://192.0.2.10:8080/', ip, instance.id));
});

test('fake health, local/unconfigured bootstrap and incompatible protocol never pass as managed login', () => {
  const bootstrap = {
    mode: 'managed',
    protocol: 1,
    pluginVersion: '0.1.0',
    manifest: { protocol: 1, element: 'auriga-team-panel' },
  };
  assertManaged(bootstrap);
  for (const bad of [
    { status: 'ok', ready: true },
    { ...bootstrap, mode: 'connect' },
    { ...bootstrap, protocol: 2 },
    { ...bootstrap, manifest: null },
    { ...bootstrap, pluginVersion: '' },
  ])
    assert.throws(() => assertManaged(bad));
});

test('access preflight rejects unsupported modes, unsafe origins and mismatched instance mode', () => {
  validateAccess(access);
  for (const bad of [
    { ...access, mode: 'unknown' },
    {
      ...access,
      portalOrigin: {
        ...access.portalOrigin,
        host: 'alice@portal.example.com',
      },
    },
    { ...access, instances: { mode: 'path' } },
  ])
    assert.throws(() => validateAccess(bad));
});

test('TOTP matches RFC 6238 SHA-1 vectors (six digits)', () => {
  const secret = 'GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ'; // gitleaks:allow RFC 6238 public test vector
  assert.equal(totp(secret, 59_000), '287082');
  assert.equal(totp(secret, 1_111_111_109_000), '081804');
  assert.throws(() => totp('invalid!'));
});
