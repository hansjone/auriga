import assert from 'node:assert/strict';
import { createHmac } from 'node:crypto';
import { Buffer } from 'node:buffer';

export function origin(value) {
  assert.ok(value && ['http', 'https'].includes(value.scheme));
  assert.ok(typeof value.host === 'string' && /^[a-z0-9.-]+$/.test(value.host));
  assert.ok(
    Number.isInteger(value.port) && value.port > 0 && value.port < 65536,
  );
  const url = new URL(`${value.scheme}://${value.host}:${value.port}`);
  assert.equal(url.hostname, value.host);
  return url.origin;
}

export function validateAccess(access) {
  assert.ok(['domain', 'ip'].includes(access?.mode));
  assert.notEqual(origin(access.portalOrigin), origin(access.adminOrigin));
  if (access.mode === 'domain') {
    assert.equal(access.instances?.mode, 'subdomain');
    origin({ ...access.instances, host: access.instances.parentDomain });
  } else assert.equal(access.instances?.mode, 'path');
  return access;
}

export function assertReady(instance, memberId) {
  assert.match(
    instance?.id ?? '',
    /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/,
  );
  assert.ok(memberId);
  assert.equal(instance.ownerId, memberId);
  assert.equal(instance.ready, true);
  assert.ok(['running', 'pending_restart'].includes(instance.phase));
}

export function assertEntry(value, access, id) {
  const url = new URL(value);
  assert.equal(url.username + url.password + url.search + url.hash, '');
  if (access.mode === 'domain') {
    assert.equal(
      url.origin,
      origin({
        ...access.instances,
        host: `${id}.${access.instances.parentDomain}`,
      }),
    );
    assert.equal(url.pathname, '/');
  } else {
    assert.equal(url.origin, origin(access.portalOrigin));
    assert.equal(url.pathname, `/i/${id}/`);
  }
}

export function assertManaged(bootstrap) {
  assert.equal(bootstrap?.mode, 'managed');
  assert.equal(bootstrap.protocol, 1);
  assert.ok(
    typeof bootstrap.pluginVersion === 'string' &&
      bootstrap.pluginVersion.length > 0,
  );
  assert.equal(bootstrap.manifest?.protocol, 1);
  assert.equal(bootstrap.manifest.element, 'auriga-team-panel');
}

export function totp(secret, now = Date.now()) {
  assert.match(secret, /^[A-Z2-7]+=*$/i);
  const alphabet = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567';
  const bits = [...secret.toUpperCase().replace(/=+$/, '')]
    .map((c) => alphabet.indexOf(c).toString(2).padStart(5, '0'))
    .join('');
  const bytes = [];
  for (let i = 0; i + 8 <= bits.length; i += 8)
    bytes.push(parseInt(bits.slice(i, i + 8), 2));
  const counter = Buffer.alloc(8);
  counter.writeBigUInt64BE(BigInt(Math.floor(now / 30_000)));
  const digest = createHmac('sha1', Buffer.from(bytes))
    .update(counter)
    .digest();
  const offset = digest.at(-1) & 15;
  return String(
    (digest.readUInt32BE(offset) & 0x7fffffff) % 1_000_000,
  ).padStart(6, '0');
}
