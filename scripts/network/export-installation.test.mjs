import assert from 'node:assert/strict';
import { Buffer } from 'node:buffer';
import { createHash, generateKeyPairSync, verify } from 'node:crypto';
import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { test } from 'node:test';
import { createV2Signer } from '../../clients/dsh-plugin/test/network/trust/v2-keyset-fixtures.js';
import {
  exportControlPackage,
  exportInstallation,
} from './export-installation.mjs';
import { digest, jcs } from '../../clients/dsh-plugin/src/network/trust/v2.js';

const script = fileURLToPath(
  new URL('./export-installation.mjs', import.meta.url),
);
const der = (pair) => pair.publicKey.export({ format: 'der', type: 'spki' });
const fingerprint = (bytes) =>
  'sha256:' + createHash('sha256').update(bytes).digest('hex');

async function fixture(t) {
  const directory = await mkdtemp(
    path.join(os.tmpdir(), 'auriga-install-export-'),
  );
  t.after(() => rm(directory, { recursive: true, force: true }));
  const root = generateKeyPairSync('ed25519');
  const gateway = generateKeyPairSync('ed25519');
  const now = Math.floor(Date.now() / 1000) * 1000;
  const iso = (offset) =>
    new Date(now + offset).toISOString().replace('.000Z', 'Z');
  const registration = {
    tenant: '019f0140-0000-7000-8000-000000000001',
    realm: '1',
    node: '64',
    device: '019f0140-0000-7000-8000-000000000002',
    systemAddress: 'aur1:1.40.0',
    created: true,
  };
  const payload = {
    schema_version: 1,
    realm: '1',
    keyset_version: '7',
    issued_at: iso(-60000),
    valid_until: iso(3600000),
    keys: [
      {
        kid: 'gateway-1',
        alg: 'Ed25519',
        purpose: 'protocol_response',
        state: 'active',
        not_before: iso(-60000),
        not_after: iso(3600000),
        public_key_spki: der(gateway).toString('base64url'),
      },
    ],
  };
  const manifest = {
    connect: {
      platformOrigin: 'https://portal.example.com',
      issuer: 'https://portal.example.com',
      clientId: 'auriga-dsh',
      providerId: 'team',
    },
    expectedIdentity: {
      tenant: registration.tenant,
      device: registration.device,
    },
    registrationFile: 'register.json',
    minimumVersion: '7',
    roots: [
      {
        kid: 'offline-1',
        publicKeyFile: 'root.der',
        fingerprintFile: 'fingerprint.txt',
      },
    ],
    signedKeysetFile: 'signed.json',
    announcementFile: 'announcement.json',
    gateway: { kid: 'gateway-1', publicKeyFile: 'gateway.der' },
    wssUrl: 'wss://gateway.example.com:9443/custom/ws',
  };
  const put = (name, value) =>
    writeFile(
      path.join(directory, name),
      typeof value === 'string' || Buffer.isBuffer(value)
        ? value
        : JSON.stringify(value),
    );
  async function publish(signer = root, overrides = {}) {
    const v2 = createV2Signer(signer, 'offline-1');
    await put('signed.json', v2.publication(payload, registration.tenant));
    const fields = { ...overrides };
    if (fields.manifest_digest) {
      fields.keyset_digest = fields.manifest_digest;
      delete fields.manifest_digest;
    }
    await put(
      'announcement.json',
      v2.announcement(payload, fields, registration.tenant),
    );
  }

  await put('root.der', der(root));
  await put('gateway.der', der(gateway));
  await put('fingerprint.txt', fingerprint(der(root)) + '\n');
  await put('register.json', registration);
  await publish();
  const manifestFile = path.join(directory, 'manifest.json');
  async function run() {
    await put('manifest.json', manifest);
    return exportInstallation(manifestFile, () => now);
  }
  return {
    directory,
    root,
    gateway,
    now,
    iso,
    manifest,
    registration,
    payload,
    put,
    publish,
    run,
    manifestFile,
  };
}

test('exports only public connect from realm 1 and generated offline root', async (t) => {
  const f = await fixture(t);
  const result = await f.run();
  assert.deepEqual(Object.keys(result), ['connect']);
  assert.deepEqual(result.connect.network, {
    realm: '1',
    wssUrl: f.manifest.wssUrl,
    roots: [
      { kid: 'offline-1', public_key_spki: der(f.root).toString('base64url') },
    ],
  });
  assert.deepEqual(Object.keys(result.connect).sort(), [
    'clientId',
    'issuer',
    'network',
    'platformOrigin',
    'providerId',
  ]);
  assert.doesNotMatch(
    JSON.stringify(result),
    /tenant|device|private|signature|minimumVersion|File|fingerprint/,
  );
  for (const value of [
    f.registration.tenant,
    f.registration.device,
    f.root.privateKey
      .export({ format: 'der', type: 'pkcs8' })
      .toString('base64url'),
  ])
    assert.equal(JSON.stringify(result).includes(value), false);
});

const negatives = {
  'tenant mismatch': async (f) => {
    f.manifest.expectedIdentity.tenant = f.registration.device;
  },
  'device mismatch': async (f) => {
    f.manifest.expectedIdentity.device = f.registration.tenant;
  },
  'noncanonical realm': async (f) => {
    f.registration.realm = '01';
    await f.put('register.json', f.registration);
  },
  'realm over uint64': async (f) => {
    f.registration.realm = '18446744073709551616';
    await f.put('register.json', f.registration);
  },
  'duplicate root kid': async (f) => {
    f.manifest.roots.push({ ...f.manifest.roots[0] });
  },
  'issuer origin mismatch': async (f) => {
    f.manifest.connect.issuer = 'https://other.example.com';
  },
  'registration address mismatch': async (f) => {
    f.registration.systemAddress = 'aur1:1.41.0';
    await f.put('register.json', f.registration);
  },
  'signed realm mismatch': async (f) => {
    f.payload.realm = '2';
    await f.publish();
  },
  'fingerprint mismatch': async (f) => {
    await f.put('fingerprint.txt', 'sha256:' + '0'.repeat(64));
  },
  'wrong root signature': async (f) => {
    await f.publish(generateKeyPairSync('ed25519'));
  },
  'expired publication': async (f) => {
    f.payload.issued_at = f.iso(-3600000);
    f.payload.valid_until = f.iso(-1000);
    await f.publish();
  },
  'future publication': async (f) => {
    f.payload.issued_at = f.iso(61000);
    await f.publish();
  },
  'publication exceeds 24h': async (f) => {
    f.payload.valid_until = f.iso(86400000);
    await f.publish();
  },
  'lower version': async (f) => {
    f.manifest.minimumVersion = '8';
  },
  'missing minimum version': async (f) => {
    delete f.manifest.minimumVersion;
  },
  'wrong purpose': async (f) => {
    f.payload.keys[0].purpose = 'business_delivery';
    await f.publish();
  },
  'retired gateway': async (f) => {
    f.payload.keys[0].state = 'retired';
    await f.publish();
  },
  'gateway material mismatch': async (f) => {
    await f.put('gateway.der', der(generateKeyPairSync('ed25519')));
  },
  'missing gateway kid': async (f) => {
    f.manifest.gateway.kid = 'absent';
  },
  'expired gateway': async (f) => {
    f.payload.keys[0].not_after = f.iso(-1000);
    await f.publish();
  },
  'next gateway': async (f) => {
    f.payload.keys[0].state = 'next';
    await f.publish();
  },
  'private root input': async (f) => {
    await f.put(
      'root.der',
      f.root.privateKey.export({ format: 'der', type: 'pkcs8' }),
    );
  },
  'TLS public key input': async (f) => {
    await f.put(
      'root.der',
      der(generateKeyPairSync('ec', { namedCurve: 'P-256' })),
    );
  },
  'business key as root': async (f) => {
    f.payload.keys[0].public_key_spki = der(f.root).toString('base64url');
    await f.publish();
  },
  'unknown secret field': async (f) => {
    f.manifest.accessToken = 'never-output-this-secret';
  },
  'duplicate JSON key': async (f) => {
    await f.put('register.json', '{"realm":"1","realm":"2"}');
  },
  'mismatched announcement': async (f) => {
    await f.publish(f.root, { manifest_digest: 'sha256:' + '0'.repeat(64) });
  },
  'wrong announcement signature': async (f) => {
    const a = JSON.parse(
      await readFile(path.join(f.directory, 'announcement.json')),
    );
    a.manifest_digest = 'sha256:' + '0'.repeat(64);
    await f.put('announcement.json', a);
  },
};
for (const [name, mutate] of Object.entries(negatives)) {
  test('rejects ' + name, async (t) => {
    const f = await fixture(t);
    await mutate(f);
    await assert.rejects(
      f.run(),
      name === 'wrong purpose'
        ? { code: 'network.signature_invalid' }
        : undefined,
    );
  });
}

for (const url of [
  'http://portal.example.com',
  'https://user:pass@portal.example.com',
  'https://portal.example.com/path',
  'https://portal.example.com/?x=1',
]) {
  test('rejects platform ' + url, async (t) => {
    const f = await fixture(t);
    f.manifest.connect.platformOrigin = url;
    await assert.rejects(f.run());
  });
}
for (const url of [
  'ws://gateway.example.com/ws',
  'wss://user:pass@gateway.example.com/ws',
  'wss://gateway.example.com/ws?x=1',
  'wss://gateway.example.com/ws#fragment',
]) {
  test('rejects WSS ' + url, async (t) => {
    const f = await fixture(t);
    f.manifest.wssUrl = url;
    await assert.rejects(f.run());
  });
}

test('CLI writes a new public file, refuses overwrite and produces no output on rejection', async (t) => {
  const f = await fixture(t);
  await f.run();
  const output = path.join(f.directory, 'connect.json');
  const call = (...args) =>
    spawnSync(process.execPath, [script, ...args], { encoding: 'utf8' });
  const success = call(f.manifestFile, output);
  assert.equal(success.status, 0, success.stderr);
  assert.equal(success.stdout, '');
  const saved = await readFile(output, 'utf8');
  assert.deepEqual(JSON.parse(saved), await f.run());
  const stdout = call(f.manifestFile);
  assert.equal(stdout.status, 0, stdout.stderr);
  assert.deepEqual(JSON.parse(stdout.stdout), JSON.parse(saved));
  assert.equal(call(f.manifestFile, output).status, 1);
  assert.equal(await readFile(output, 'utf8'), saved);
  f.manifest.accessToken = 'never-output-this-secret';
  await f.put('manifest.json', f.manifest);
  const rejected = call(f.manifestFile);
  assert.equal(rejected.status, 1);
  assert.equal(rejected.stdout, '');
  assert.doesNotMatch(rejected.stderr, /never-output|BEGIN|private/);
  assert.equal(call('--help').status, 0);
});

test('public control export pins installation roots, scope, target, digest and challenge', async (t) => {
  const f = await fixture(t);
  await f.put('installation.json', await f.run());
  const publication = JSON.parse(
    await readFile(path.join(f.directory, 'signed.json'), 'utf8'),
  );
  const pack = {
    package_id: 'public-package',
    tenant_id: f.registration.tenant,
    realm: f.registration.realm,
    target_id: f.registration.device,
    control: publication.control,
    control_digest: digest(publication.control),
    challenge: Buffer.alloc(32, 7).toString('base64url'),
  };
  await f.put('control.json', pack);
  const manifest = {
    installationFile: 'installation.json',
    controlPackageFile: 'control.json',
    expectedIdentity: {
      tenant: f.registration.tenant,
      realm: f.registration.realm,
      target: f.registration.device,
    },
  };
  await f.put('control-manifest.json', manifest);
  const file = path.join(f.directory, 'control-manifest.json');
  assert.deepEqual(
    JSON.parse(JSON.stringify(await exportControlPackage(file))),
    pack,
  );
  const call = spawnSync(process.execPath, [script, '--control', file], {
    encoding: 'utf8',
  });
  assert.equal(call.status, 0, call.stderr);
  assert.deepEqual(JSON.parse(call.stdout), pack);
  const identity = generateKeyPairSync('ed25519'),
    sealing = generateKeyPairSync('x25519');
  await writeFile(
    path.join(f.directory, 'receipt-key.der'),
    identity.privateKey.export({ type: 'pkcs8', format: 'der' }),
    { mode: 0o600 },
  );
  await writeFile(
    path.join(f.directory, 'pins.json'),
    JSON.stringify({
      schema_version: 2,
      tenant_id: f.registration.tenant,
      realm: f.registration.realm,
      pins: [
        {
          target_id: f.registration.device,
          identity_id: 'receipt-identity',
          signing_spki: der(identity).toString('base64url'),
          component: 'network',
          gateway_id: 'test-gateway',
          sealing_spki: der(sealing).toString('base64url'),
          subject: 'CN=receipt-identity',
        },
      ],
    }),
    { mode: 0o600 },
  );
  await f.put('import.json', {
    controlManifestFile: 'control-manifest.json',
    networkDirectory: 'network',
  });
  const imported = spawnSync(
    process.execPath,
    [script, '--import-control', path.join(f.directory, 'import.json')],
    {
      encoding: 'utf8',
      env: {
        ...process.env,
        AURIGA_NETWORK_INSTALLATION_PINS_FILE: path.join(
          f.directory,
          'pins.json',
        ),
        AURIGA_NETWORK_INSTALLATION_KEY_FILE: path.join(
          f.directory,
          'receipt-key.der',
        ),
        AURIGA_NETWORK_INSTALLATION_TARGET_ID: f.registration.device,
        AURIGA_NETWORK_MIGRATION_PERMIT_FILE: '',
      },
    },
  );
  assert.equal(imported.status, 0, imported.stderr);
  const { identity_auth, ...receipt } = JSON.parse(imported.stdout);
  assert.equal(receipt.package_id, pack.package_id);
  assert.ok(
    verify(
      null,
      Buffer.from('Auriga.NetworkControlReceipt.v2\0' + jcs(receipt)),
      identity.publicKey,
      Buffer.from(identity_auth.signature, 'base64url'),
    ),
  );
  for (const mutation of [
    { realm: '2' },
    { target_id: 'wrong-device' },
    { control_digest: 'sha256:' + '0'.repeat(64) },
    { challenge: 'short' },
    { private_key: 'forbidden' },
  ]) {
    await f.put('control.json', { ...pack, ...mutation });
    await assert.rejects(exportControlPackage(file));
  }
});
