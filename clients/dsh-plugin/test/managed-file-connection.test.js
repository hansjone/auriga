import test from 'node:test';
import assert from 'node:assert/strict';
import {
  randomUUID,
  randomBytes,
  generateKeyPairSync,
  sign,
} from 'node:crypto';
import { cp, mkdtemp, writeFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';
import { createManagedFileConnection } from '../src/managed/file-connection.js';
import { createBroker } from '../src/local/broker.js';
import { verifyBrokerPackage } from '../src/local/broker-package.js';
import { packageBroker } from '../scripts/package-broker.mjs';
import { buildBroker } from '../../shell-auth/build.mjs';
import { managedBrokerChecks } from '../../../deploy/compose/real-dsh/runtime.mjs';
import { testRoots } from './network/trust/keyset-fixtures.js';

async function fixture(t, patch = {}, connect = createManagedFileConnection) {
  const directory = await mkdtemp(join(tmpdir(), 'auriga-managed-fixed-'));
  const key = generateKeyPairSync('ec', { namedCurve: 'P-256' });
  const material = {
    tenantId: randomUUID(),
    ownerId: randomUUID(),
    instanceId: randomUUID(),
    deviceId: randomUUID(),
    credential: randomBytes(32).toString('base64url'),
    expiresAt: new Date(Date.now() + 3600000).toISOString(),
    platformOrigin: 'https://platform.example.com',
    issuer: 'https://platform.example.com',
    network: {
      realm: '2',
      roots: testRoots,
      wssUrl: 'wss://network.example.com/',
    },
  };
  const file = join(directory, 'device.json');
  const modelFile = join(directory, 'model-credential.json');
  await writeFile(file, JSON.stringify(material), { mode: 0o600 });
  const calls = [];
  t.mock.method(globalThis, 'fetch', async (url, init = {}) => {
    calls.push({ path: new URL(url).pathname, init });
    assert.equal(init.redirect, 'error');
    assert.equal(init.credentials, 'omit');
    if (new URL(url).pathname === '/oauth2/token') {
      const now = Math.floor(Date.now() / 1000);
      const header = Buffer.from(
        JSON.stringify({ alg: 'ES256', kid: 'fixture' }),
      ).toString('base64url');
      const claims = {
        iss: material.issuer,
        aud: 'auriga',
        ptyp: 'device',
        ctx: 'device',
        scope: 'auriga.connect',
        tid: material.tenantId,
        sub: material.ownerId,
        did: material.deviceId,
        iat: now,
        exp: now + 180,
        ...patch,
      };
      const input =
        header +
        '.' +
        Buffer.from(JSON.stringify(claims)).toString('base64url');
      const access_token =
        input +
        '.' +
        sign('sha256', Buffer.from(input), {
          key: key.privateKey,
          dsaEncoding: 'ieee-p1363',
        }).toString('base64url');
      return Response.json({
        access_token,
        token_type: 'Bearer',
        expires_in: 180,
        scope: 'auriga.connect',
      });
    }
    if (new URL(url).pathname === '/oauth2/jwks')
      return Response.json({
        keys: [
          {
            ...key.publicKey.export({ format: 'jwk' }),
            kid: 'fixture',
            alg: 'ES256',
            use: 'sig',
          },
        ],
      });
    return Response.json({
      account: {
        id: material.ownerId,
        tenantId: material.tenantId,
        displayName: 'File owner',
      },
    });
  });
  const connection = await connect({
    file,
    modelFile,
    directory,
    ...material,
  });
  const broker = await createBroker(connection);
  t.after(async () => {
    broker.close();
    await rm(directory, { recursive: true, force: true });
  });
  return { broker, connection, material, file, modelFile, calls };
}

test('verified managed-linux artifact runs the fixed executor from an isolated plugin layout', async (t) => {
  const root = await mkdtemp(join(tmpdir(), 'auriga-managed-package-'));
  t.after(() => rm(root, { recursive: true, force: true }));
  const plugin = join(root, 'plugin');
  await cp(new URL('../src', import.meta.url), join(plugin, 'src'), {
    recursive: true,
  });
  await cp(
    new URL('../package.json', import.meta.url),
    join(plugin, 'package.json'),
  );
  const artifact = join(root, 'artifact');
  const manifest = buildBroker(artifact, { managedLinux: true });
  assert.equal(manifest.target, 'managed-linux');
  await assert.rejects(verifyBrokerPackage(artifact), /manifest invalid/);
  const packaged = join(plugin, 'shell-auth');
  assert.deepEqual(
    await packageBroker(artifact, packaged, 'managed-linux'),
    manifest,
  );
  await managedBrokerChecks(plugin);

  const { createManagedFileConnection: connect } = await import(
    pathToFileURL(join(plugin, 'src/managed/file-connection.js')).href
  );
  const f = await fixture(t, {}, connect);
  assert.equal(f.broker.state.current().state, 'authenticated');
  assert.equal(
    (await f.broker.call('profile_read', {})).body.memberId,
    f.material.ownerId,
  );
  await writeFile(join(packaged, 'network-core.bundle.mjs'), 'changed');
  await assert.rejects(connect({}), /digest mismatch/);
});

test('managed file authority validates both fixed DTO directions and rejects dynamic targets before HTTP', async (t) => {
  const f = await fixture(t);
  assert.equal(f.broker.state.current().state, 'authenticated');
  assert.equal(f.broker.state.current().identity.providerId, undefined);
  assert.equal(
    JSON.stringify(f.broker.state.current()).includes(f.material.credential),
    false,
  );
  assert.equal(
    (await f.broker.call('profile_read', {})).body.memberId,
    f.material.ownerId,
  );
  const before = f.calls.length;
  await assert.rejects(f.broker.call('unknown', {}));
  await assert.rejects(
    f.broker.call('profile_read', { url: 'https://other.example.com' }),
  );
  assert.equal(f.calls.length, before);
});
for (const patch of [
  { aud: 'other' },
  { sub: randomUUID() },
  { scope: 'admin' },
  { exp: Math.floor(Date.now() / 1000) - 1 },
]) {
  test(
    'managed file rejects a signed device JWT with foreign or expired binding ' +
      Object.keys(patch)[0],
    async (t) => {
      const f = await fixture(t, patch);
      assert.notEqual(f.broker.state.current().state, 'authenticated');
      await assert.rejects(f.broker.call('profile_read', {}), {
        code: 'common.unauthenticated',
      });
      assert.equal(
        f.calls.some((c) => c.path === '/api/iam/v1/me/device-profile'),
        false,
      );
    },
  );
}
test('atomic credential replacement immediately fences previous handles before periodic refresh', async (t) => {
  const f = await fixture(t);
  const check = f.broker.state.fence();
  await writeFile(
    f.file,
    JSON.stringify({ ...f.material, ownerId: randomUUID() }),
  );
  assert.throws(check, { code: 'common.unauthenticated' });
  assert.equal(f.connection.stale, true);
  assert.equal(f.broker.state.current().state, 'revoked');
});

test('model credential rotation fences buffered results while the device identity remains authenticated', async (t) => {
  const f = await fixture(t);
  const model = {
    tenantId: f.material.tenantId,
    ownerId: f.material.ownerId,
    instanceId: f.material.instanceId,
    credentialVersion: 1,
    credential: [
      'amc',
      f.material.tenantId,
      f.material.ownerId,
      f.material.instanceId,
      f.material.deviceId,
      randomBytes(32).toString('base64url'),
    ].join('.'),
    baseUrl: f.material.platformOrigin + '/ai/v1',
    models: [{ id: 'fixture-model' }],
  };
  await writeFile(f.modelFile, JSON.stringify(model), { mode: 0o600 });
  const before = f.broker.state.current();
  const check = f.broker.state.fence();
  await writeFile(
    f.modelFile,
    JSON.stringify({ ...model, credentialVersion: 2 }),
  );
  assert.throws(check);
  const after = f.broker.state.current();
  assert.equal(after.state, 'authenticated');
  assert.deepEqual(after.identity, before.identity);
  assert.ok(after.revision > before.revision);
});
