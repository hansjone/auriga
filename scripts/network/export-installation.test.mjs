import assert from 'node:assert/strict';
import { Buffer } from 'node:buffer';
import { createHash, generateKeyPairSync, sign } from 'node:crypto';
import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { test } from 'node:test';
import { canonicalize } from '../../clients/dsh-plugin/src/network/trust/jcs.js';
import { exportInstallation } from './export-installation.mjs';

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
    const announcement = {
      keyset_version: payload.keyset_version,
      manifest_digest: fingerprint(Buffer.from(canonicalize(payload))),
      valid_until: payload.valid_until,
      root_kid: 'offline-1',
      ...overrides,
    };
    await put('signed.json', {
      keyset: payload,
      root_auth: {
        alg: 'Ed25519',
        kid: 'offline-1',
        signature: sign(
          null,
          Buffer.from('Auriga.NetworkKeyset.v1\0' + canonicalize(payload)),
          signer.privateKey,
        ).toString('base64url'),
      },
    });
    await put('announcement.json', {
      ...announcement,
      root_signature: sign(
        null,
        Buffer.from(
          'Auriga.NetworkKeysetAnnouncement.v1\0' + canonicalize(announcement),
        ),
        signer.privateKey,
      ).toString('base64url'),
    });
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
    f.payload.issued_at = f.iso(1000);
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
