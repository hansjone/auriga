import test from 'node:test';
import assert from 'node:assert/strict';
import { Buffer } from 'node:buffer';
import {
  generateKeyPairSync,
  randomBytes,
  sign,
  verify,
  createHash,
} from 'node:crypto';
import { mkdtemp, writeFile, readFile, rm, link } from 'node:fs/promises';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import {
  createControlInstallation,
  installationIdentity,
} from '../../../src/network/trust/installation-control.js';
import { createFileLedgerStore } from '../../../src/network/host/store.js';
import { openV2TrustStore } from '../../../src/network/trust/v2-store.js';
import { createDeviceProvider } from '../../../src/network/device-provider.js';
import { digest, jcs } from '../../../src/network/trust/v2.js';

const spki = (pair) =>
  pair.publicKey.export({ format: 'der', type: 'spki' }).toString('base64url');
async function fixture(t) {
  const dir = await mkdtemp(join(tmpdir(), 'auriga-installation-pin-'));
  t.after(() => rm(dir, { recursive: true, force: true }));
  const root = generateKeyPairSync('ed25519'),
    identity = generateKeyPairSync('ed25519'),
    sealing = generateKeyPairSync('x25519');
  const tenant_id = '11111111-1111-4111-8111-111111111111',
    target_id = '33333333-3333-4333-8333-333333333333';
  const pin = {
    target_id,
    identity_id: 'test-installation',
    signing_spki: spki(identity),
    component: 'network',
    gateway_id: 'test-gateway',
    sealing_spki: spki(sealing),
    subject: 'CN=test-installation',
  };
  const pins = { schema_version: 2, tenant_id, realm: '1', pins: [pin] };
  const payload = {
    schema_version: 2,
    tenant_id,
    realm: '1',
    control_revision: '2',
    issued_at: '2026-11-14T00:00:00Z',
    allowed_authorities: [],
    revoked_authority_ids: [],
    revoked_installation_ids: [],
    minimum_generation: '1',
    minimum_keyset_version: '1',
  };
  const control = {
    payload,
    root_auth: {
      kid: 'root',
      alg: 'Ed25519',
      signature: sign(
        null,
        Buffer.from('Auriga.NetworkRootControl.v2\0' + jcs(payload)),
        root.privateKey,
      ).toString('base64url'),
    },
  };
  const pack = {
    package_id: 'test-package',
    tenant_id,
    realm: '1',
    target_id,
    challenge: randomBytes(32).toString('base64url'),
    control,
    control_digest: digest(control),
  };
  const privateDer = identity.privateKey.export({
    format: 'der',
    type: 'pkcs8',
  });
  const pinsFile = join(dir, 'pins.json'),
    keyFile = join(dir, 'identity.der');
  await writeFile(pinsFile, JSON.stringify(pins), { mode: 0o600 });
  await writeFile(keyFile, privateDer, { mode: 0o600 });
  const env = {
    AURIGA_NETWORK_INSTALLATION_PINS_FILE: pinsFile,
    AURIGA_NETWORK_INSTALLATION_KEY_FILE: keyFile,
  };
  const config = {
    platformOrigin: 'https://portal.example.com',
    issuer: 'https://portal.example.com',
    clientId: 'auriga-dsh',
    providerId: 'team',
    network: {
      realm: '1',
      roots: [{ kid: 'root', public_key_spki: spki(root) }],
      wssUrl: 'wss://network.example.com/',
    },
  };
  const directory = join(dir, 'network');
  const service = createControlInstallation({ config, directory, env });
  const storageKey = createHash('sha256')
    .update(JSON.stringify([config.issuer, tenant_id, target_id]))
    .digest('hex');
  const cacheDir = join(directory, 'devices', storageKey, 'trust');
  return {
    dir,
    pin,
    pins,
    identity,
    tenant_id,
    target_id,
    pack,
    config,
    privateDer,
    pinsFile,
    keyFile,
    service,
    cacheDir,
  };
}
test('offline public import persists a device-signed receipt; normal provider retries confirmation without message connection', async (t) => {
  const f = await fixture(t);
  const receipt = await f.service.importPackage(JSON.stringify(f.pack));
  const { identity_auth, ...body } = receipt;
  assert.equal(identity_auth.kid, f.pin.identity_id);
  assert.ok(
    verify(
      null,
      Buffer.from('Auriga.NetworkControlReceipt.v2\0' + jcs(body)),
      f.identity.publicKey,
      Buffer.from(identity_auth.signature, 'base64url'),
    ),
  );
  const persisted = await readFile(join(f.cacheDir, 'ledger.json'), 'utf8');
  assert.ok(!persisted.includes(f.privateDer.toString('base64url')));
  let confirmed = 0,
    wrongBinding = true;
  t.mock.method(globalThis, 'fetch', async (url, request) => {
    assert.equal(url.origin, f.config.platformOrigin);
    if (url.pathname.endsWith('/bootstrap/controls'))
      return Response.json(f.pack.control);
    if (url.pathname.endsWith('/controls/receipts')) {
      assert.equal(JSON.parse(request.body).package_id, f.pack.package_id);
      confirmed++;
      const receipt = JSON.parse(request.body);
      return Response.json({
        tenant_id: receipt.tenant_id,
        realm: receipt.realm,
        target_id: receipt.target_id,
        minimum_control_revision: receipt.control_revision,
        control_digest: wrongBinding
          ? 'sha256:' + '0'.repeat(64)
          : receipt.control_digest,
        state: 'confirmed',
        identity_compromised: false,
        challenge: receipt.challenge,
        new_chain_ready: false,
      });
    }
    return Response.json({}, { status: 503 });
  });
  const provider = createDeviceProvider({
    config: f.config,
    loadInstallation: f.service.prepare,
    getDevice: async () => ({
      accessToken: 'test-device-token',
      claims: { tid: f.tenant_id, did: f.target_id, sub: 'test-member' },
    }),
  });
  const prepared = await provider.prepare({
    signal: new AbortController().signal,
  });
  assert.equal(prepared.controlIdentity.identity_id, f.pin.identity_id);
  const cache = await createFileLedgerStore({ dir: f.cacheDir }).open();
  try {
    const trust = await openV2TrustStore({
      cache,
      roots: f.config.network.roots,
      realm: '1',
      tenant_id: f.tenant_id,
      identity_id: f.pin.identity_id,
    });
    await assert.rejects(prepared.refresh(trust, new AbortController().signal));
    assert.equal(confirmed, 1);
    assert.equal(trust.pendingControlReceipts().length, 1);
    wrongBinding = false;
    await assert.rejects(prepared.refresh(trust, new AbortController().signal));
    assert.equal(confirmed, 2);
    assert.equal(trust.pendingControlReceipts().length, 0);
  } finally {
    await cache.close();
  }
});
test('installation pins enforce both algorithms, material match, uniqueness and actual device scope', async (t) => {
  const f = await fixture(t);
  const expected = {
    tenant_id: f.tenant_id,
    realm: '1',
    target_id: f.target_id,
  };
  assert.ok(installationIdentity(f.pins, undefined, f.privateDer, expected));
  for (const pins of [
    { ...f.pins, tenant_id: 'other' },
    { ...f.pins, pins: [f.pin, { ...f.pin, identity_id: 'other' }] },
    { ...f.pins, pins: [{ ...f.pin, signing_spki: f.pin.sealing_spki }] },
    { ...f.pins, pins: [{ ...f.pin, sealing_spki: f.pin.signing_spki }] },
    { ...f.pins, pins: [{ ...f.pin, unexpected: true }] },
  ])
    assert.throws(() =>
      installationIdentity(pins, undefined, f.privateDer, expected),
    );
  assert.throws(() =>
    installationIdentity(
      f.pins,
      undefined,
      generateKeyPairSync('ed25519').privateKey.export({
        type: 'pkcs8',
        format: 'der',
      }),
      expected,
    ),
  );
  const signer = installationIdentity(
    f.pins,
    undefined,
    f.privateDer,
    expected,
  ).controlIdentity;
  await assert.rejects(
    signer.sign(Buffer.from('arbitrary bytes')),
    /scope_denied/,
  );
  await assert.rejects(
    f.service.prepare({ ...expected, target_id: 'other' }),
    /scope_denied/,
  );
  await link(f.keyFile, join(f.dir, 'key-alias.der'));
  await assert.rejects(f.service.prepare(expected), /scope_denied/);
});
test('an imported control cannot select file paths, self-provision a pin or change its target', async (t) => {
  const f = await fixture(t);
  for (const mutation of [
    { target_id: 'other' },
    { privateKeyFile: f.keyFile },
    { control_digest: 'sha256:' + '0'.repeat(64) },
  ])
    await assert.rejects(f.service.importPackage({ ...f.pack, ...mutation }));
  const noIdentity = createControlInstallation({
    config: f.config,
    directory: f.dir,
    env: {},
  });
  assert.equal(noIdentity.available, false);
  await assert.rejects(noIdentity.importPackage(f.pack), /unavailable/);
});
