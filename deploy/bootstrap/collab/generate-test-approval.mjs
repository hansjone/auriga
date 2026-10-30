#!/usr/bin/env node
// Disposable test installations only. Never invoked by the NAS installer.
import {
  generateKeyPairSync,
  createHash,
  randomBytes,
  sign,
} from 'node:crypto';
import { Buffer } from 'node:buffer';
import { readFile, writeFile, mkdir, lstat } from 'node:fs/promises';
import { resolve, dirname, join } from 'node:path';

const hash = (bytes) => createHash('sha256').update(bytes).digest('hex');
const canonical = (value) =>
  JSON.stringify(value, (_, row) =>
    row && typeof row === 'object' && !Array.isArray(row)
      ? Object.fromEntries(
          Object.entries(row).sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0)),
        )
      : row,
  );
const encode = (value) => Buffer.from(canonical(value));
async function main() {
  const [flag, request, destination, ...extra] = process.argv.slice(2);
  if (flag !== '--test-only' || !request || !destination || extra.length)
    throw Error();
  const raw = await readFile(request);
  if (raw.length > 65536) throw Error();
  const cfg = JSON.parse(raw);
  if (
    !cfg ||
    Object.keys(cfg).sort().join(',') !== 'approval,realm,tenantId' ||
    !/^[a-f0-9]{8}(?:-[a-f0-9]{4}){3}-[a-f0-9]{12}$/.test(cfg.tenantId) ||
    typeof cfg.realm !== 'string' ||
    !/^[1-9][0-9]{0,19}$/.test(cfg.realm) ||
    BigInt(cfg.realm) > 18446744073709551615n ||
    typeof cfg.approval !== 'string' ||
    !cfg.approval.trim() ||
    cfg.approval.length > 4096
  )
    throw Error();
  const out = resolve(destination);
  for (let p = dirname(out); ; p = dirname(p)) {
    const entry = await lstat(p);
    if (!entry.isDirectory() || entry.isSymbolicLink()) throw Error();
    if (p === dirname(p)) break;
  }
  // A new directory and exclusive file creation prevent silent root replacement.
  // On Windows the operator must first restrict the parent ACL to the current user.
  await mkdir(out, { mode: 0o700 });
  const put = async (name, bytes) =>
    writeFile(join(out, name), bytes, { flag: 'wx', mode: 0o600 });
  const json = async (name, value) => put(name, encode(value));
  const root = generateKeyPairSync('ed25519');
  const rootDer = root.publicKey.export({ type: 'spki', format: 'der' });
  const rootKid = 'test-only-' + randomBytes(8).toString('hex');
  await put('root.der', rootDer);
  await put(
    'offline-root-private.der',
    root.privateKey.export({ type: 'pkcs8', format: 'der' }),
  );
  const issued = new Date(Math.floor(Date.now() / 1000) * 1000 - 60000);
  const until = new Date(issued.getTime() + 24 * 3600000);
  const issuedAt = issued.toISOString().replace('.000Z', 'Z');
  const validUntil = until.toISOString().replace('.000Z', 'Z');
  const privateFiles = {};
  const keys = [];
  const stateKeys = [];
  for (const [name, purpose] of [
    ['business', 'business_delivery'],
    ['protocol', 'protocol_response'],
    ['management', 'management_state'],
  ]) {
    const pair = generateKeyPairSync('ed25519');
    const kid = 'test-only-' + name + '-' + randomBytes(8).toString('hex');
    await put(
      name + '.der',
      pair.privateKey.export({ type: 'pkcs8', format: 'der' }),
    );
    privateFiles[kid] = name + '.der';
    const key = {
      kid,
      purpose,
      public_key_spki: pair.publicKey
        .export({ type: 'spki', format: 'der' })
        .toString('base64url'),
      not_before: issuedAt,
      not_after: validUntil,
    };
    keys.push({ ...key, alg: 'Ed25519', state: 'active' });
    stateKeys.push({ ...key, realm: cfg.realm, state: 'ACTIVE' });
  }
  const keyset = {
    schema_version: 1,
    realm: cfg.realm,
    keyset_version: '1',
    issued_at: issuedAt,
    valid_until: validUntil,
    keys,
  };
  const signature = (domain, value) =>
    sign(
      null,
      Buffer.concat([Buffer.from(domain + '\0'), encode(value)]),
      root.privateKey,
    ).toString('base64url');
  await json('signed.json', {
    keyset,
    root_auth: {
      alg: 'Ed25519',
      kid: rootKid,
      signature: signature('Auriga.NetworkKeyset.v1', keyset),
    },
  });
  const announcement = {
    keyset_version: '1',
    valid_until: validUntil,
    manifest_digest: 'sha256:' + hash(encode(keyset)),
    root_kid: rootKid,
  };
  await json('announcement.json', {
    ...announcement,
    root_signature: signature(
      'Auriga.NetworkKeysetAnnouncement.v1',
      announcement,
    ),
  });
  await json('state.json', {
    version: '1',
    issued_at: issuedAt,
    keys: stateKeys,
  });
  const rootFingerprint = 'sha256:' + hash(rootDer);
  const input = {
    tenantId: cfg.tenantId,
    realm: cfg.realm,
    rootKid,
    rootFingerprint,
    minimumVersion: '1',
    rootPublicFile: 'root.der',
    signedFile: 'signed.json',
    announcementFile: 'announcement.json',
    stateFile: 'state.json',
    privateFiles,
  };
  await json('input.json', input);
  // Keep the strict shared Input schema unchanged; bind the separate audit record
  // to its exact bytes. This note is not a cryptographic approval authority.
  await json('approval.json', {
    testOnly: true,
    approval: cfg.approval,
    inputSha256: hash(encode(input)),
    rootFingerprint,
    tenantId: cfg.tenantId,
    realm: cfg.realm,
    validUntil,
  });
  console.log(
    JSON.stringify({ testOnly: true, rootFingerprint, rootKid, validUntil }),
  );
}
main().catch(() => {
  console.error(
    'Test approval generation refused or failed; existing output is never replaced.',
  );
  process.exitCode = 1;
});
