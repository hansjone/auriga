import assert from 'node:assert/strict';
import {
  createPrivateKey,
  generateKeyPairSync,
  randomUUID,
  sign,
} from 'node:crypto';
import { readFileSync } from 'node:fs';

export function assertRuntimeIsolation(
  info,
  network,
  edge,
  instanceId,
  expectedAlias = 'portal.example.com',
) {
  const [uid, gid] = String(info.Config?.User).split(':').map(Number);
  assert.ok(
    Number.isInteger(uid) && uid >= 200000 && uid === gid,
    'instance uid isolation missing',
  );
  const hc = info.HostConfig || {};
  assert.equal(hc.Privileged, false);
  assert.equal(hc.ReadonlyRootfs, true);
  assert.ok(hc.CapDrop?.includes('ALL'), 'cap-drop ALL missing');
  assert.ok(
    hc.SecurityOpt?.some((value) =>
      /^no-new-privileges(?::true)?$/.test(value),
    ),
    'no-new-privileges missing',
  );
  assert.ok(
    hc.Memory > 0 && hc.NanoCpus > 0 && hc.PidsLimit > 0,
    'resource limits missing',
  );
  assert.equal(
    info.Mounts?.find((mount) => mount.Destination === '/run/auriga')?.RW,
    false,
    'meta mount is not read-only',
  );
  assert.equal(
    info.Mounts?.find((mount) => mount.Destination === '/home/dsh')?.RW,
    true,
    'home is not writable',
  );
  assert.deepEqual(
    Object.keys(info.NetworkSettings?.Networks || {}),
    [`auriga-net-${instanceId}`],
    'instance joined another network',
  );
  assert.equal(network.Name, `auriga-net-${instanceId}`);
  assert.equal(network.Internal, true);
  assert.equal(network.Driver, 'bridge');
  assert.equal(network.Labels?.['auriga.instance'], instanceId);
  assert.equal(network.Labels?.['auriga.managed'], 'true');
  assert.equal(edge.Config?.Labels?.['auriga.role'], 'edge');
  assert.ok(info.Id && edge.Id && info.Id !== edge.Id);
  assert.deepEqual(
    Object.keys(network.Containers || {}).sort(),
    [info.Id, edge.Id].sort(),
    'foreign network member or missing trusted edge',
  );
  assert.equal(
    edge.NetworkSettings?.Networks?.[network.Name]?.NetworkID,
    network.Id,
  );
  assert.ok(
    edge.NetworkSettings.Networks[network.Name].Aliases?.includes(
      expectedAlias,
    ),
    'platform DNS alias missing',
  );
}

export function realDshPageOk(page) {
  return (
    page.status === 200 &&
    /<html[\s>]/i.test(page.text || '') &&
    /__DSH_BOOT__/.test(page.text || '') &&
    /type=["']module["']/.test(page.text || '')
  );
}

export function assertRealHealth(response, pluginVersion) {
  assert.equal(response.status, 200, 'real plugin health status');
  assert.deepEqual(Object.keys(response.body || {}).sort(), [
    'pluginVersion',
    'protocol',
    'ready',
    'status',
  ]);
  assert.deepEqual(response.body, {
    status: 'ok',
    ready: true,
    protocol: 1,
    pluginVersion,
  });
}

export function assertDoorRejected(response) {
  assert.equal(response.status, 401, 'door must reject with HTTP 401');
  assert.equal(response.body?.code, 'common.unauthenticated');
}

/** All signing credentials stay in memory, never in command lines or evidence. */
export function identitySigner({ keysFile, instanceId, ownerId, tenantId }) {
  const key = JSON.parse(readFileSync(keysFile, 'utf8')).keys.find(
    (entry) => entry.d,
  );
  if (!key) throw new Error('Missing local e2e signing key');
  const privateKey = createPrivateKey({ key, format: 'jwk' });
  return (patch = {}, badSignature = false) => {
    const now = Math.floor(Date.now() / 1000);
    const header = Buffer.from(
      JSON.stringify({ alg: 'EdDSA', kid: key.kid, typ: 'auriga-id+jwt' }),
    ).toString('base64url');
    const payload = Buffer.from(
      JSON.stringify({
        iss: 'auriga-edge',
        aud: instanceId,
        sub: ownerId,
        tid: tenantId,
        sid: randomUUID(),
        iat: now,
        exp: now + 60,
        jti: randomUUID(),
        pv: 1,
        htm: 'GET',
        htu: '/',
        ...patch,
      }),
    ).toString('base64url');
    const input = `${header}.${payload}`;
    return `${input}.${sign(
      null,
      Buffer.from(input),
      badSignature ? generateKeyPairSync('ed25519').privateKey : privateKey,
    ).toString('base64url')}`;
  };
}

/** Direct-to-container requests bypass edge so its own rejection cannot mask a missing door. */
export async function probeRealDoor(request, signer, pluginVersion) {
  assertRealHealth(await request('/_auriga/health'), pluginVersion);
  assertDoorRejected(await request('/'));
  assertDoorRejected(await request('/', { identity: signer({}, true) }));
  assertDoorRejected(
    await request('/', { identity: signer({ sub: randomUUID() }) }),
  );
  const past = Math.floor(Date.now() / 1000) - 120;
  assertDoorRejected(
    await request('/', { identity: signer({ iat: past, exp: past + 60 }) }),
  );
  const token = signer();
  assert.ok(
    realDshPageOk(await request('/', { identity: token })),
    'signed request must reach real DSH HTML',
  );
  assertDoorRejected(await request('/', { identity: token }));
  assertDoorRejected(await request('/_auriga/health', { method: 'POST' }));
  return {
    missing: 401,
    signature: 401,
    owner: 401,
    expired: 401,
    replay: 401,
    healthWrite: 401,
  };
}
