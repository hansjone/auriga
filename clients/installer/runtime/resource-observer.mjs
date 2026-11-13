import { skillFiles, verifySkillDirectory } from './skill-bytes.mjs';
import { createHash } from 'node:crypto';
import { existsSync, lstatSync, readdirSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { assertLocalPath } from './host-lock.mjs';
import { parseStrictJson, canonicalize } from './json.mjs';
import { requireSchema, schemaValid } from './schema.mjs';
import {
  verifyTrust,
  signedRevocationFact,
  signedRevocations,
} from './resource-trust.mjs';

const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
export const sha = (b) =>
  'sha256:' + createHash('sha256').update(b).digest('hex');
export function readBounded(file, limit = 2097152) {
  assertLocalPath(file);
  const info = lstatSync(file);
  if (!info.isFile() || info.size > limit)
    throw new Error('invalid resource file');
  return readFileSync(file);
}
export function readRecord(file, schema) {
  return requireSchema(schema, parseStrictJson(readBounded(file), true));
}
const dirs = (path) => {
  assertLocalPath(path);
  if (!existsSync(path)) return [];
  const entries = readdirSync(path, { withFileTypes: true });
  if (entries.length > 8192) throw new Error('resource directory limit');
  return entries.map((e) => {
    assertLocalPath(join(path, e.name));
    return e;
  });
};

export function observeLedgers(home) {
  const base = join(home, 'auriga', 'resources');
  const result = [];
  for (const tenant of dirs(base))
    for (const owner of dirs(join(base, tenant.name)))
      for (const host of dirs(join(base, tenant.name, owner.name))) {
        if (
          ![tenant, owner, host].every(
            (x) => x.isDirectory() && uuid.test(x.name),
          )
        )
          throw new Error('invalid resource identity directory');
        const directory = join(
          base,
          tenant.name,
          owner.name,
          host.name,
          'skills',
        );
        if (!existsSync(join(directory, 'ledger.json'))) continue;
        const ledger = readRecord(
          join(directory, 'ledger.json'),
          'UnifiedLedgerFile',
        );
        if (
          ledger.tenantId !== tenant.name ||
          ledger.ownerId !== owner.name ||
          ledger.hostId !== host.name
        )
          throw new Error('ledger identity mismatch');
        const transactions = dirs(join(directory, 'transactions')).map((e) => {
          if (!e.isFile() || !/^[0-9a-f-]{36}\.json$/.test(e.name))
            throw new Error('invalid transaction filename');
          const tx = readRecord(
            join(directory, 'transactions', e.name),
            'UnifiedTransactionFile',
          );
          if (
            tx.transactionId + '.json' !== e.name ||
            ['tenantId', 'ownerId', 'hostId'].some((k) => tx[k] !== ledger[k])
          )
            throw new Error('transaction identity mismatch');
          return tx;
        });
        result.push({ directory, ledger, transactions });
      }
  return result;
}

function cached(directory, digest, max = 536870912, checked = []) {
  if (!/^sha256:[0-9a-f]{64}$/.test(digest))
    throw new Error('invalid cache digest');
  const path = join(directory, 'cache', digest.slice(7));
  const files = dirs(path).filter((e) => e.isFile());
  // Cache filenames are not authority: verify the original bytes in the digest directory.
  const matches = files
    .map((e) => {
      const file = join(path, e.name);
      const bytes = readBounded(file, max);
      checked.push({ file, digest: sha(bytes) });
      return bytes;
    })
    .filter((b) => sha(b) === digest);
  if (matches.length !== 1)
    throw new Error('missing or ambiguous cached bytes');
  return matches[0];
}

function revocations(snapshot, trust, now, release, checkedFiles) {
  const lists = [];
  const factsDirectory = join(
    snapshot.directory,
    '..',
    '..',
    '..',
    '..',
    '..',
    'guardian',
    'trusted-revocations',
    snapshot.ledger.tenantId,
  );
  for (const file of dirs(factsDirectory)) {
    if (!file.isFile() || !/^[0-9a-f]{64}\.json$/.test(file.name))
      throw new Error('invalid persisted revocation path');
    const bytes = readBounded(join(factsDirectory, file.name));
    const list = parseStrictJson(bytes, true);
    if (!signedRevocations(list, snapshot.ledger.tenantId, trust))
      throw new Error('invalid persisted revocations');
    if (signedRevocationFact(list, release.envelope, trust))
      throw Object.assign(new Error('revoked'), { revocationList: list });
  }

  for (const digest of dirs(join(snapshot.directory, 'cache'))) {
    if (!digest.isDirectory() || !/^[0-9a-f]{64}$/.test(digest.name))
      throw new Error('invalid cache directory');
    for (const file of dirs(join(snapshot.directory, 'cache', digest.name))) {
      if (!file.isFile()) continue;
      const path = join(snapshot.directory, 'cache', digest.name, file.name);
      if (lstatSync(path).size > 2097152) continue;
      const bytes = readBounded(path);
      checkedFiles.push({ file: path, digest: sha(bytes) });
      if (sha(bytes) !== 'sha256:' + digest.name) continue;
      let list;
      try {
        list = parseStrictJson(bytes, true);
      } catch {
        continue;
      }
      if (
        !schemaValid('signatures', 'RevocationList', list) ||
        list.tenantId !== snapshot.ledger.tenantId
      )
        continue;
      if (signedRevocationFact(list, release.envelope, trust))
        throw Object.assign(new Error('revoked'), { revocationList: list });
      // Any valid signed revocation of this version must prevail over cached older lists.
      try {
        verifyTrust(
          'revocation',
          { release: { ...release, now }, list },
          trust,
        );
      } catch (error) {
        if (error.message === 'revoked') throw error;
        continue;
      }
      lists.push(list);
    }
  }
  lists.sort((a, b) => b.revision - a.revision);
  if (!lists.length) throw new Error('no valid signed revocations');
  if (
    lists[1]?.revision === lists[0].revision &&
    canonicalize(lists[0]) !== canonicalize(lists[1])
  )
    throw new Error('revocation equivocation');
  return lists[0];
}

export function installationEnabled(i) {
  const r = i.resource;
  return (
    (r.desiredState === undefined ? r.enabled : r.desiredState === 'ENABLED') &&
    ['applying', 'applied', 'pending_restart'].includes(i.state)
  );
}
export function acceptedView(snapshot, trust, now) {
  const expected = new Map();
  for (const i of snapshot.ledger.installations.filter(installationEnabled)) {
    const r = i.resource,
      rawManifest = cached(
        snapshot.directory,
        r.identity.manifestDigest,
        2097152,
      ).toString('utf8');
    const artifactBytes = {};
    for (const a of r.artifacts)
      artifactBytes[a.artifactId] = cached(
        snapshot.directory,
        a.digest,
      ).toString('base64');
    const release = {
      protocol: 2,
      envelope: r.envelope,
      rawManifest,
      artifactBytes,
      expected: r.identity,
      now,
    };
    verifyTrust('release', release, trust);
    revocations(snapshot, trust, now, release, []);
    const manifest = parseStrictJson(Buffer.from(rawManifest));
    const payload = manifest.artifacts.find((a) => a.role === 'payload');
    expected.set(
      manifest.name,
      skillFiles(Buffer.from(artifactBytes[payload.id], 'base64')),
    );
  }
  return expected;
}

export function verifySnapshot(
  snapshot,
  trust,
  watermarks,
  now,
  previous = new Map(),
) {
  const { ledger, directory, transactions } = snapshot;
  const installations = ledger.installations.filter(installationEnabled);
  const expected = new Map();
  const releases = [];
  const checkedFiles = [];
  const leases = [];
  const materializedNames = [];
  const views = [];
  const cacheRoot = join(directory, 'cache');
  const cacheListing = () =>
    canonicalize(
      dirs(cacheRoot)
        .map((e) => [
          e.name,
          dirs(join(cacheRoot, e.name))
            .map((f) => f.name)
            .sort(),
        ])
        .sort(),
    );
  const originalListing = cacheListing();
  for (const installation of installations) {
    const r = installation.resource;
    if (
      installation.reportSource !== 'member' ||
      installation.clientId !== ledger.hostId ||
      installation.selectionRevision !== ledger.selectionRevision ||
      installation.generation !== ledger.generation ||
      r.identity.tenantId !== ledger.tenantId ||
      r.identity.kind !== 'skill' ||
      r.releaseSequence !== r.envelope.releaseSequence
    )
      throw new Error('installation identity mismatch');
    if (installation.state === 'applying') {
      const prepared = transactions.filter(
        (t) =>
          ['install', 'activate'].includes(t.operation) &&
          t.phase === 'prepared' &&
          t.expectedLedgerRevision === ledger.revision &&
          t.selectionRevision === ledger.selectionRevision &&
          t.generation === ledger.generation,
      );
      if (prepared.length !== 1)
        throw new Error('missing prepared transaction');
      assertLocalPath(join(directory, 'staging', prepared[0].transactionId));
      if (!existsSync(join(directory, 'staging', prepared[0].transactionId)))
        throw new Error('missing staging');
    } else {
      const receipt = readRecord(
        join(
          directory,
          'receipts',
          r.identity.packageId,
          r.identity.versionId + '.json',
        ),
        'UnifiedReceiptFile',
      );
      if (
        receipt.recordType !== 'installation' ||
        ['tenantId', 'ownerId', 'hostId'].some(
          (k) => receipt[k] !== ledger[k],
        ) ||
        ['installationRevision', 'selectionRevision', 'generation'].some(
          (k) => receipt[k] !== installation[k],
        ) ||
        canonicalize(receipt.resource) !== canonicalize(r)
      )
        throw new Error('installation receipt mismatch');
    }
    const rawManifest = cached(
      directory,
      r.identity.manifestDigest,
      2097152,
      checkedFiles,
    ).toString('utf8');
    const artifactBytes = {};
    for (const a of r.artifacts)
      artifactBytes[a.artifactId] = cached(
        directory,
        a.digest,
        536870912,
        checkedFiles,
      ).toString('base64');
    const release = {
      protocol: 2,
      envelope: r.envelope,
      rawManifest,
      artifactBytes,
      expected: r.identity,
      now,
    };
    verifyTrust('release', release, trust);
    const manifest = parseStrictJson(Buffer.from(rawManifest));
    if (
      r.identity.version !== manifest.version ||
      r.identity.manifestDigest !== r.envelope.statement.manifestDigest ||
      canonicalize(
        r.artifacts.map((a) => [a.artifactId, a.digest, a.sizeBytes]).sort(),
      ) !==
        canonicalize(
          manifest.artifacts.map((a) => [a.id, a.digest, a.sizeBytes]).sort(),
        )
    )
      throw new Error('resource ref binding');
    const payload = manifest.artifacts.find((a) => a.role === 'payload');
    if (!payload) throw new Error('Skill payload missing');
    const expectedFiles = skillFiles(
      Buffer.from(artifactBytes[payload.id], 'base64'),
    );
    expected.set(manifest.name, expectedFiles);
    const materialized =
      installation.state === 'applying'
        ? join(
            directory,
            'staging',
            transactions.find(
              (t) =>
                t.phase === 'prepared' &&
                t.expectedLedgerRevision === ledger.revision,
            ).transactionId,
            manifest.name,
          )
        : join(
            directory,
            '..',
            '..',
            '..',
            '..',
            '..',
            'skills-active',
            'current',
            manifest.name,
          );
    verifySkillDirectory(materialized, expectedFiles, checkedFiles);
    views.push({ directory: materialized, expected: expectedFiles });
    if (installation.state !== 'applying')
      materializedNames.push(manifest.name);
    const list = revocations(snapshot, trust, now, release, checkedFiles);
    const expires = Math.min(
      Date.parse(r.envelope.notAfter),
      Date.parse(list.expiresAt),
    );
    if (
      Date.parse(r.lease.expiresAt) !== expires ||
      r.lease.envelopeNotAfter !== r.envelope.notAfter ||
      r.lease.revocationsExpiresAt !== list.expiresAt ||
      r.lease.revocationRevision !== list.revision
    )
      throw new Error('lease binding');
    const old = watermarks.entries.find(
      (w) =>
        w.tenantId === ledger.tenantId && w.packageId === r.identity.packageId,
    );
    if (old) verifyTrust('watermark', { release, watermark: old }, trust);
    releases.push({
      tenantId: ledger.tenantId,
      packageId: r.identity.packageId,
      releaseSequence: r.releaseSequence,
      versionId: r.identity.versionId,
      verifiedDigestAliases: [r.identity.manifestDigest],
    });
    leases.push(r.lease);
  }
  const activeRoot = join(
    directory,
    '..',
    '..',
    '..',
    '..',
    '..',
    'skills-active',
    'current',
  );
  const actualNames = dirs(activeRoot)
    .map((e) => e.name)
    .sort();
  for (const name of actualNames) {
    if (materializedNames.includes(name)) continue;
    if (!previous.has(name))
      throw new Error('unexpected active Skill directories');
    const directory = join(activeRoot, name);
    verifySkillDirectory(directory, previous.get(name), checkedFiles);
    views.push({ directory, expected: previous.get(name) });
  }
  if (!installations.length) return null;
  leases.sort((a, b) => Date.parse(a.expiresAt) - Date.parse(b.expiresAt));
  return {
    expected,
    lease: leases[0],
    releases,
    ledger,
    checkedFiles,
    views,
    cacheListing,
    originalListing,
    activeRoot,
    actualNames,
    fingerprint: canonicalize(snapshot),
  };
}

export function recheckBytes(verification) {
  if (
    verification.cacheListing() !== verification.originalListing ||
    canonicalize(
      dirs(verification.activeRoot)
        .map((e) => e.name)
        .sort(),
    ) !== canonicalize(verification.actualNames)
  )
    throw new Error('resource directory changed');
  for (const view of verification.views)
    verifySkillDirectory(view.directory, view.expected, []);
  for (const item of verification.checkedFiles)
    if (sha(readBounded(item.file, 536870912)) !== item.digest)
      throw new Error('verified bytes changed');
}
