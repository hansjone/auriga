import { createHash, generateKeyPairSync, sign } from 'node:crypto';
import { gzipSync } from 'node:zlib';
import { mkdirSync, writeFileSync, readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { canonicalize } from '../runtime/json.mjs';
import { durableJson } from '../runtime/host-lock.mjs';
const digest = (b) => 'sha256:' + createHash('sha256').update(b).digest('hex');
export const id = (n) =>
  `019a0000-0000-7000-8000-${String(n).padStart(12, '0')}`;
const put = (file, bytes) => {
  mkdirSync(dirname(file), { recursive: true });
  writeFileSync(file, bytes);
};
function tar(body) {
  const header = Buffer.alloc(512);
  header.write('SKILL.md');
  header.write('0000644\0', 100);
  header.write('0000000\0', 108);
  header.write('0000000\0', 116);
  header.write(
    Buffer.byteLength(body).toString(8).padStart(11, '0') + '\0',
    124,
  );
  header.write('00000000000\0', 136);
  header.fill(32, 148, 156);
  header[156] = 48;
  const sum = header.reduce((a, b) => a + b, 0);
  header.write(sum.toString(8).padStart(6, '0') + '\0 ', 148);
  return gzipSync(
    Buffer.concat([
      header,
      Buffer.from(body),
      Buffer.alloc(((512 - (Buffer.byteLength(body) % 512)) % 512) + 1024),
    ]),
  );
}
export function resourceFixture(home, now = Date.now(), duration = 3600000) {
  const keys = {};
  const trust = {};
  for (const role of ['release', 'revocation', 'downgrade']) {
    keys[role] = generateKeyPairSync('ed25519');
    trust[role] = {
      kid: 'fixture-' + role,
      publicKeySpkiBase64: keys[role].publicKey
        .export({ type: 'spki', format: 'der' })
        .toString('base64'),
    };
  }
  const signed = (input, role) => {
    const { signature: ignored, ...body } = input;
    void ignored;
    return {
      ...body,
      signature: {
        alg: 'Ed25519',
        kid: trust[role].kid,
        value: sign(
          null,
          Buffer.from(canonicalize(body)),
          keys[role].privateKey,
        ).toString('base64url'),
      },
    };
  };
  const vector = JSON.parse(
    readFileSync(
      new URL(
        '../../../contracts/resource/unified-vectors.json',
        import.meta.url,
      ),
    ),
  ).cases[0].input;
  const body =
    '---\nname: example-resource\ndescription: Isolated Desktop guardian fixture.\n---\n\nFixture body.\n';
  const archive = tar(body);
  const manifest = JSON.parse(vector.rawManifest);
  manifest.artifacts[0].digest = digest(archive);
  manifest.artifacts[0].sizeBytes = archive.length;
  const rawManifest = JSON.stringify(manifest);
  const issuedAt = new Date(now - 1000).toISOString(),
    expiresAt = new Date(now + duration).toISOString();
  const envelope = signed(
    {
      ...vector.envelope,
      notAfter: expiresAt,
      statement: {
        ...vector.envelope.statement,
        issuedAt,
        manifestDigest: digest(rawManifest),
        artifactDigests: [digest(archive)],
      },
    },
    'release',
  );
  const revocations = (revision = 1, revokedArtifacts = []) =>
    signed(
      {
        schemaVersion: 1,
        tenantId: id(1),
        revision,
        issuedAt,
        expiresAt,
        revokedKids: [],
        revokedArtifacts,
      },
      'revocation',
    );
  const ref = {
    identity: {
      tenantId: id(1),
      packageId: id(2),
      versionId: id(4),
      kind: 'skill',
      version: '1.2.3',
      manifestDigest: digest(rawManifest),
    },
    envelope,
    manifestDownloadPath: `/api/resource/v1/catalog/packages/${id(2)}/versions/${id(4)}/manifest`,
    artifacts: [
      {
        artifactId: 'payload',
        digest: digest(archive),
        sizeBytes: archive.length,
        downloadPath: `/api/resource/v1/catalog/packages/${id(2)}/versions/${id(4)}/artifacts/payload`,
      },
    ],
    lease: {
      envelopeNotAfter: expiresAt,
      revocationsExpiresAt: expiresAt,
      expiresAt,
      revocationRevision: 1,
    },
    releaseSequence: 7,
    enabled: true,
  };
  const installation = {
    clientId: id(3),
    resource: ref,
    installationRevision: 1,
    selectionRevision: 1,
    generation: 1,
    state: 'applied',
    reportSource: 'member',
  };
  const ledger = {
    schemaVersion: 1,
    tenantId: id(1),
    ownerId: id(5),
    hostId: id(3),
    revision: 1,
    selectionRevision: 1,
    generation: 1,
    installations: [installation],
    processedDisableReceiptIds: [],
  };
  const directory = join(
    home,
    'auriga',
    'resources',
    id(1),
    id(5),
    id(3),
    'skills',
  );
  const cache = (b) =>
    put(join(directory, 'cache', digest(b).slice(7), 'content'), b);
  cache(rawManifest);
  cache(archive);
  cache(JSON.stringify(revocations()));
  durableJson(join(directory, 'ledger.json'), ledger);
  const receipt = {
    schemaVersion: 1,
    tenantId: id(1),
    ownerId: id(5),
    hostId: id(3),
    recordType: 'installation',
    writer: 'plugin',
    receiptId: id(6),
    resource: ref,
    installationRevision: 1,
    selectionRevision: 1,
    generation: 1,
    recordedAt: issuedAt,
  };
  const receiptPath = join(directory, 'receipts', id(2), id(4) + '.json');
  mkdirSync(dirname(receiptPath), { recursive: true });
  durableJson(receiptPath, receipt);
  put(
    join(
      home,
      'auriga',
      'skills-active',
      'current',
      'example-resource',
      'SKILL.md',
    ),
    body,
  );
  return { home, trust, ledger, directory, cache, revocations, ref };
}
