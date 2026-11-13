import { test } from 'node:test';
import assert from 'node:assert/strict';
import { generateKeyPairSync, randomBytes } from 'node:crypto';
import { readFileSync, writeFileSync, readdirSync } from 'node:fs';
import { join } from 'node:path';
import { performance } from 'node:perf_hooks';
import { catalogFixture } from './catalog-fixture.js';
import { desktopSkills } from '../../src/resources/desktop-skills.js';
import { clockBootId, hostTime } from '../../src/resources/host-lock.js';
import { verifyTrust } from '../../src/resources/unified-trust.js';

test('100 signed text skills prepare and switch under separate real 30-second host leases, with batched Windows commits', async (t) => {
  const shared = {
    release: generateKeyPairSync('ed25519'),
    revocation: generateKeyPairSync('ed25519'),
    nowMillis: Date.now(),
    files: { 'references/notes.md': randomBytes(8192).toString('hex') },
  };
  const fixtures = Array.from({ length: 100 }, (_, i) =>
    catalogFixture(t, undefined, {
      ...shared,
      name: 'scale-' + i,
      packageId:
        '019a0000-0000-7000-8000-' + (256 + i).toString(16).padStart(12, '0'),
      versionId:
        '019a0000-0000-7000-8000-' + (512 + i).toString(16).padStart(12, '0'),
    }),
  );
  const f = fixtures[0],
    desktop = desktopSkills({ home: f.home });
  const roots = {
    release: {
      kid: 'release-test',
      publicKeySpkiBase64: shared.release.publicKey
        .export({ format: 'der', type: 'spki' })
        .toString('base64'),
    },
    revocation: {
      kid: 'revoke-test',
      publicKeySpkiBase64: shared.revocation.publicKey
        .export({ format: 'der', type: 'spki' })
        .toString('base64'),
    },
  };
  for (const item of fixtures)
    verifyTrust(
      'revocation',
      {
        release: {
          protocol: 2,
          envelope: item.resource.envelope,
          rawManifest: item.raw.toString('utf8'),
          artifactBytes: { payload: item.archive.toString('base64') },
          expected: item.resource.identity,
          now: new Date().toISOString(),
        },
        list: item.revoked,
      },
      roots,
    );
  const rows = fixtures.map((item) => item.getRow()),
    downloads = fixtures.map((item) => ({
      manifest: item.raw,
      artifacts: [item.archive],
      revocations: Buffer.from(JSON.stringify(item.revoked)),
    }));
  const start = performance.now();
  desktop.prepare(f.who, rows, downloads);
  const prepareMillis = performance.now() - start;
  f.allow();
  const sample = hostTime();
  Object.assign(f.state.clock, {
    bootId: clockBootId(sample.bootId),
    lastMonotonicMillis: sample.monotonicMillis,
    leaseDeadlineMonotonicMillis: sample.monotonicMillis + 3600000,
  });
  writeFileSync(
    join(f.home, 'auriga/guardian/state.json'),
    JSON.stringify(f.state),
  );
  writeFileSync(
    join(f.home, 'auriga/guardian/high-watermarks.json'),
    JSON.stringify({
      schemaVersion: 1,
      revision: 1,
      entries: rows.map((row) => ({
        tenantId: f.who.tenantId,
        packageId: row.resource.identity.packageId,
        releaseSequence: row.resource.releaseSequence,
        versionId: row.resource.identity.versionId,
        verifiedDigestAliases: [row.resource.identity.manifestDigest],
      })),
      migrationMarkers: [],
      consumedDowngradeAuthorizationIds: [],
    }),
  );
  const switchStart = performance.now();
  desktop.finish(f.who, () => true);
  const finishMillis = performance.now() - switchStart;
  assert.equal(readdirSync(f.active).length, 100);
  assert.equal(
    JSON.parse(readFileSync(join(f.directory, 'ledger.json'))).installations
      .length,
    100,
  );
  assert.ok(prepareMillis < 30000, 'prepare exceeds the fixed host lease');
  assert.ok(finishMillis < 30000, 'finish exceeds the fixed host lease');
  t.diagnostic(
    JSON.stringify({
      platform: process.platform,
      skills: 100,
      files: 200,
      attachmentBytes: 16384,
      prepareMillis: Math.round(prepareMillis),
      finishMillis: Math.round(finishMillis),
      leaseMillis: 30000,
      clock: 'real host boot/uptime; no injected clock',
    }),
  );
});
