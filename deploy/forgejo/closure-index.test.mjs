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

test('the Web overlay binds script-only source changes without changing cached dependencies', () => {
  const overlay = read('web-overlay-b7359410.json');
  const release = read('web-overlay-release.json');
  for (const artifact of release.artifacts) {
    const bytes = readFileSync(new URL(artifact.file, import.meta.url));
    assert.equal(
      bytes.includes(13),
      false,
      'published overlays must retain Git LF bytes',
    );
    assert.equal(bytes.length, artifact.bytes);
    assert.equal(
      createHash('sha256').update(bytes).digest('hex'),
      artifact.sha256,
    );
  }
  const closure = read('closure-index.json');
  const archive = closure.archives.find(
    (item) => item.name === overlay.baseArchive,
  );
  assert.equal(archive.sha256, overlay.baseArchiveSha256);
  const contents = new Map();
  for (const file of overlay.files) {
    const bytes = readFileSync(new URL(file.file, import.meta.url));
    const digest = createHash('sha256').update(bytes).digest('hex');
    assert.equal(digest, file.sha256);
    assert.equal(
      release.artifacts.find((item) => item.file === file.file).sha256,
      digest,
    );
    contents.set(file.destination, bytes);
  }
  const sums = new Map(
    contents
      .get('SHA256SUMS')
      .toString()
      .trim()
      .split('\n')
      .map((line) => {
        const [sha256, file] = line.trim().split(/\s+/);
        return [file, sha256];
      }),
  );
  assert.deepEqual([...sums.keys()].sort(), [
    'corepack.tgz',
    'source-locks.sha256',
    'web-cache.tar.gz',
  ]);
  assert.equal(
    sums.get('source-locks.sha256'),
    createHash('sha256')
      .update(contents.get('source-locks.sha256'))
      .digest('hex'),
  );
  assert.equal(
    sums.get('corepack.tgz'),
    closure.entries.find(
      (item) => item.archive === archive.name && item.member === 'corepack.tgz',
    ).sha256,
  );
  assert.equal(
    sums.get('web-cache.tar.gz'),
    '7618ca4a2186f4494e3a29691f0fcb797172a2e9a20207637699d142c319f9ab',
  );
  const manifest = JSON.parse(
    readFileSync(
      new URL('../../' + overlay.sourceChange.file, import.meta.url),
    ),
  );
  delete manifest.scripts;
  const canonical = JSON.stringify(
    Object.fromEntries(
      Object.entries(manifest).sort(([a], [b]) => a.localeCompare(b)),
    ),
  );
  assert.equal(
    createHash('sha256').update(canonical).digest('hex'),
    overlay.sourceChange.manifestWithoutScriptsSha256,
  );
});

test('the NAS builder tool is fixed to a verified Forgejo archive and image identity', () => {
  const release = read('nas-builder-release.json');
  assert.match(release.upstreamDigest, /^sha256:[a-f0-9]{64}$/);
  assert.match(release.configDigest, /^sha256:[a-f0-9]{64}$/);
  assert.match(release.archive.sha256, /^[a-f0-9]{64}$/);
  validate(
    { forgejo: 'https://forgejo.example.com', owner: 'alice' },
    { artifacts: release.archive.parts },
  );
  assert.ok(release.archive.parts.length);
});
