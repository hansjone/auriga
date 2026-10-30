import test from 'node:test';
import assert from 'node:assert/strict';
import {
  assertReady,
  assertEntry,
  assertManaged,
  totp,
  validateAccess,
} from './checks.mjs';

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
  const secret = 'GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ';
  assert.equal(totp(secret, 59_000), '287082');
  assert.equal(totp(secret, 1_111_111_109_000), '081804');
  assert.throws(() => totp('invalid!'));
});
