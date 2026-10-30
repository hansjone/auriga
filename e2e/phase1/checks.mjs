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

// Browser presentation only. Do not alter server access, issuer, requests or
// returned links: those still have to work through the operator's real proxy.
export function browserPortalOrigin(access, baseUrl, proxy) {
  const upstream = origin(access.portalOrigin);
  const target = new URL(baseUrl).origin;
  if (!proxy) {
    assert.equal(target, upstream);
    return upstream;
  }
  assert.equal(access.mode, 'ip');
  const exactOrigin = (value) => {
    const url = new URL(value);
    assert.ok(['http:', 'https:'].includes(url.protocol));
    assert.equal(url.username + url.password + url.search + url.hash, '');
    assert.equal(url.pathname, '/');
    return url;
  };
  assert.equal(exactOrigin(proxy.upstreamOrigin).origin, upstream);
  const presentation = exactOrigin(proxy.publicOrigin);
  assert.equal(presentation.protocol, 'https:');
  assert.equal(presentation.origin, target);
  assert.notEqual(target, origin(access.adminOrigin));
  return target;
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

export function assertEntry(
  value,
  access,
  id,
  portal = origin(access.portalOrigin),
) {
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
    assert.equal(url.origin, portal);
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

export function sharedNodeReady(node) {
  return (
    node?.status === 'online' &&
    node.schedulable === true &&
    node.drivers?.includes('docker') === true &&
    node.labels?.template === 'real-dsh-e2e'
  );
}

export function loginOutcome(tx, traceId) {
  const status = [
    'pending',
    'completed',
    'failed',
    'cancelled',
    'expired',
  ].includes(tx?.status)
    ? tx.status
    : 'unknown';
  const step = [
    'form',
    'complete',
    'fail',
    'redirect',
    'qr',
    'webauthn',
    'device',
  ].includes(tx?.step?.type)
    ? tx.step.type
    : 'unknown';
  const code = tx?.step?.error?.code ?? tx?.step?.code;
  return {
    status,
    step,
    ...(typeof code === 'string' &&
    /^[a-z][a-z0-9_]*\.[a-z][a-z0-9_]*$/.test(code)
      ? { code }
      : {}),
    ...(typeof traceId === 'string' && /^[0-9a-f]{32}$/.test(traceId)
      ? { traceId }
      : {}),
  };
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

export function totpNotBefore(now, lastCounter = -1) {
  const counter = Math.floor(now / 30_000);
  if (counter <= lastCounter || 30_000 - (now % 30_000) < 5_000)
    return Math.max(counter + 1, lastCounter + 1) * 30_000 + 100;
  return now;
}

export function otpField(step) {
  const fields = step?.fields?.filter((field) => field.kind === 'otp') ?? [];
  assert.equal(fields.length, 1);
  assert.ok(['otp', 'code'].includes(fields[0].name));
  return fields[0].name;
}
