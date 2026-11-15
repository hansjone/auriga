import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { createHash } from 'node:crypto';
import { mapInventory } from './map-inventory.mjs';
import { validate } from './download.mjs';

const read = (name) => JSON.parse(readFileSync(new URL(name, import.meta.url)));

test('the frozen discovery retains all rows and resolves every Linux build input', () => {
  const inventory = read('forgejo-deps-inventory.json');
  const closure = read('closure-index.json');
  const mapped = mapInventory(inventory, closure);
  assert.equal(mapped.entries.length, 6980);
  assert.equal(mapped.counts.missing, 0);
  assert.equal(
    mapped.acceptanceSourceCommit,
    '84dca4a66e6a09f6c6e00628d833fce41920d855',
  );
  assert.deepEqual(mapped.counts, inventory.counts);
  assert.deepEqual(mapped.supplyCounts, inventory.supplyCounts);
  assert.equal(
    mapped.supplyCounts.existingOnly + mapped.supplyCounts.includesSupplement,
    mapped.counts.present,
  );
  assert.equal(
    Object.values(mapped.counts).reduce((sum, value) => sum + value, 0),
    6980,
  );
  for (const [position, entry] of mapped.entries.entries())
    assert.equal(entry.discoveryPosition, position);
});

test('closure members, image evidence and input groups refer to downloadable archives', () => {
  const closure = read('closure-index.json');
  const archives = new Map(
    closure.archives.map((archive) => [archive.name, archive]),
  );
  assert.equal(archives.size, closure.archives.length);
  const parts = new Map();
  for (const archive of archives.values()) {
    assert.match(archive.sha256, /^[a-f0-9]{64}$/);
    assert.ok(archive.parts.length);
    for (const part of archive.parts) parts.set(part.path, part);
  }
  for (const member of [...closure.entries, ...closure.images]) {
    assert.ok(archives.has(member.archive), member.archive);
    assert.match(member.sha256, /^[a-f0-9]{64}$/);
    assert.ok(member.member);
    assert.ok(member.version);
  }
  validate(
    { forgejo: 'https://forgejo.example.com', owner: 'alice' },
    { artifacts: [...parts.values()] },
  );
  for (const resolution of closure.referenceResolutions) {
    for (const evidence of resolution.forgejo.evidence ?? []) {
      assert.ok(archives.has(evidence.archive), evidence.archive);
      assert.match(evidence.sha256, /^[a-f0-9]{64}$/);
      assert.ok(evidence.version);
    }
  }
  for (const image of closure.builtImages)
    assert.match(image.configDigest, /^sha256:[a-f0-9]{64}$/);
});

test('the Java overlay binds the new POM to unchanged verified dependency bytes', () => {
  const overlay = read('java-overlay-84dca4a6.json');
  const release = read('java-release.json');
  assert.equal(overlay.baseArchive, release.archive.name);
  assert.equal(overlay.baseArchiveSha256, release.archive.sha256);
  assert.equal(
    overlay.baseSourceLocksSha256,
    release.files.find((file) => file.name === 'java-source-locks.sha256')
      .sha256,
  );
  const files = new Map();
  for (const file of overlay.files) {
    const bytes = readFileSync(new URL(file.file, import.meta.url));
    assert.equal(createHash('sha256').update(bytes).digest('hex'), file.sha256);
    files.set(file.destination, bytes);
  }
  const manifest = new Map(
    files
      .get('SHA256SUMS')
      .toString()
      .trim()
      .split('\n')
      .map((line) => {
        const [sha256, file] = line.trim().split(/\s+/);
        return [file, sha256];
      }),
  );
  assert.equal(
    manifest.get('source-locks.sha256'),
    createHash('sha256').update(files.get('source-locks.sha256')).digest('hex'),
  );
  assert.equal(
    manifest.get(
      'repository/org/bouncycastle/bcprov-jdk18on/1.83/bcprov-jdk18on-1.83.jar',
    ),
    overlay.dependencyChange.jarSha256,
  );
  assert.ok(
    files
      .get('source-locks.sha256')
      .toString()
      .includes(
        overlay.dependencyChange.sha256 + '  ' + overlay.dependencyChange.file,
      ),
  );
});
