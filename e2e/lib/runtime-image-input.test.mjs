import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  buildArgs,
  collectorArgs,
  imageTag,
  localRepository,
  repositoryDigest,
  selectRepoDigest,
} from '../../deploy/compose/scripts/runtime-image-input.mjs';

const m = 'sha256:' + 'a'.repeat(64);
const other = 'sha256:' + 'b'.repeat(64);

test('builder defaults and ordered explicit options preserve the fixture', () => {
  assert.equal(
    buildArgs([]).image.fullRepository,
    '127.0.0.1:5000/auriga/real-dsh',
  );
  assert.equal(buildArgs([]).image.tag, 'w3');
  for (const args of [
    [
      '--metadata-out',
      'meta.json',
      '--image-tag',
      'localhost:12345/team/dsh:release_1',
    ],
    [
      '--image-tag',
      'localhost:12345/team/dsh:release_1',
      '--metadata-out',
      'meta.json',
    ],
  ]) {
    const parsed = buildArgs(args);
    assert.equal(parsed.metadataOut, 'meta.json');
    assert.equal(parsed.image.fullRepository, 'localhost:12345/team/dsh');
    assert.equal(parsed.image.tag, 'release_1');
  }
});

test('canonical local image and repository reject remote and ambiguous syntax', () => {
  for (const bad of [
    'registry.example.com:5000/team/dsh:one',
    '192.0.2.10:5000/team/dsh:one',
    '127.0.0.1:0/team/dsh:one',
    'localhost:65536/team/dsh:one',
    'localhost:05000/team/dsh:one',
    'LOCALHOST:5000/team/dsh:one',
    'localhost:5000/Team/dsh:one',
    'localhost:5000/team//dsh:one',
    'localhost:5000/team/../dsh:one',
    'localhost:5000/team/dsh@sha256:abc',
    'http://localhost:5000/team/dsh:one',
    'localhost:5000@registry.example.com/team/dsh:one',
    'localhost:5000/team/dsh:one?x=1',
    'localhost:5000/team/dsh:one#frag',
    'localhost:5000/team/dsh:one two',
    'localhost:5000/team/dsh:',
    'localhost:5000/team/dsh:é',
  ])
    assert.throws(() => imageTag(bad), undefined, bad);
  for (const bad of [
    'localhost:5000',
    'localhost:5000/team/..',
    'localhost:5000/Team',
    'localhost:5000/team?x=1',
  ]) {
    assert.throws(() => localRepository(bad), undefined, bad);
  }
  assert.equal(imageTag('127.0.0.1:1/a-b/c__d:Tag.1').tag, 'Tag.1');
});

test('CLI parsing rejects duplicate, missing, and unknown arguments', () => {
  for (const args of [
    ['--image-tag'],
    ['--metadata-out'],
    ['--image-tag', '--metadata-out'],
    ['--image-tag', 'localhost:5000/x:y', '--image-tag', 'localhost:5000/x:z'],
    ['--unknown', 'value'],
    ['plain'],
  ])
    assert.throws(() => buildArgs(args));
  const image = `localhost:50123/team/dsh@${m}`;
  assert.equal(
    collectorArgs([image, 'proof', '--repository', 'localhost:50123/team/dsh'])
      .repository,
    'localhost:50123/team/dsh',
  );
  for (const args of [
    [image],
    [image, 'proof', '--repository'],
    [image, 'proof', '--repository', '--repository'],
    [image, 'proof', '--unknown', 'x'],
    [
      image,
      'proof',
      '--repository',
      'localhost:50123/team/dsh',
      '--repository',
      'x',
    ],
    [image, 'proof', '--repository', 'localhost:50123/team/other'],
  ])
    assert.throws(() => collectorArgs(args));
});

test('digest selection requires one canonical digest for the exact repository', () => {
  const repo = 'localhost:50123/team/dsh';
  const ref = `${repo}@${m}`;
  assert.equal(repositoryDigest(ref, repo), m);
  assert.equal(
    selectRepoDigest(
      [`${repo}-other@${other}`, ref, ref, `127.0.0.1:5000/else@${other}`],
      repo,
    ),
    ref,
  );
  for (const refs of [
    undefined,
    {},
    [],
    [`${repo}-other@${m}`],
    [ref, `${repo}@${other}`],
    [`${repo}@sha256:${'A'.repeat(64)}`],
  ]) {
    assert.throws(() => selectRepoDigest(refs, repo));
  }
  for (const bad of [
    `${repo}-other@${m}`,
    `${repo}@sha256:${'A'.repeat(64)}`,
    `${repo}@sha256:${'g'.repeat(64)}`,
    `${repo}@${m}x`,
    `${repo}:tag@${m}`,
  ]) {
    assert.throws(() => repositoryDigest(bad, repo));
  }
});
