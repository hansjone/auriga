import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mapInventory } from './map-inventory.mjs';

const item = {
  kind: 'go-mod',
  name: 'example.com/module',
  version: 'v1.2.3',
  sha256: 'a'.repeat(64),
  integrity: 'h1:locked',
  archive: 'inputs.tar.gz',
  member: 'download/example.com/module/@v/v1.2.3.mod',
  packagePath: '/api/packages/{owner}/generic/inputs/v1/inputs.tar.gz',
};
test('only exact members map, preserving every discovery and unresolved item', () => {
  const entries = [
    {
      kind: 'go-sum',
      name: item.name,
      version: 'v1.2.3/go.mod',
      digest: 'h1:locked',
    },
    { kind: 'go-sum', name: item.name, version: 'v1.2.4/go.mod' },
    { kind: 'maven-plugin', name: 'example:tool', version: null },
  ];
  const result = mapInventory({ entries }, { entries: [item] });
  assert.deepEqual(result.counts, {
    present: 1,
    source: 0,
    'platform-excluded': 0,
    'non-input-reference': 0,
    missing: 2,
  });
  assert.equal(result.entries.length, entries.length);
  assert.equal(result.entries[0].digest, 'h1:locked');
  assert.equal(result.entries[0].forgejo.evidence[0].sha256, item.sha256);
  assert.equal(result.entries[1].forgejo.status, 'missing');
  assert.deepEqual(result.supplyCounts, {
    existingOnly: 1,
    includesSupplement: 0,
    missing: 2,
  });
  const supplemented = mapInventory(
    { entries },
    { entries: [item], supplementalArchives: [item.archive] },
  );
  assert.deepEqual(supplemented.supplyCounts, {
    existingOnly: 0,
    includesSupplement: 1,
    missing: 2,
  });
});
test('reject a closure with missing integrity evidence', () => {
  assert.throws(
    () =>
      mapInventory({ entries: [] }, { entries: [{ ...item, sha256: null }] }),
    /SHA-256/,
  );
});
test('require the original npm and Go lock integrity', () => {
  for (const digest of ['sha512-correct', 'h1:correct']) {
    const kind = digest.startsWith('h1:') ? 'go-sum' : 'npm';
    const entry = {
      kind,
      name: item.name,
      version: kind === 'go-sum' ? item.version + '/go.mod' : item.version,
      digest,
    };
    const member = { ...item, kind: kind === 'go-sum' ? 'go-mod' : 'npm' };
    assert.equal(
      mapInventory({ entries: [entry] }, { entries: [member] }).entries[0]
        .forgejo.status,
      'missing',
    );
    assert.equal(
      mapInventory(
        { entries: [entry] },
        { entries: [{ ...member, integrity: digest }] },
      ).entries[0].forgejo.status,
      'present',
    );
  }
});
test('resolve inherited Maven coordinates and retain platform exclusions', () => {
  const entries = [
    {
      file: 'module/pom.xml',
      kind: 'maven-plugin',
      name: 'inherited:plugin',
      version: null,
    },
    { kind: 'pnpm', name: 'platform-package', version: '1.0.0' },
  ];
  const closure = {
    entries: [
      { ...item, kind: 'maven', name: 'org.example:plugin', version: '3.0.0' },
    ],
    resolvedMavenVersions: [
      {
        file: entries[0].file,
        kind: entries[0].kind,
        declaredName: entries[0].name,
        name: 'org.example:plugin',
        version: '3.0.0',
      },
    ],
    platformExclusions: [
      {
        kind: 'npm',
        name: 'platform-package',
        version: '1.0.0',
        os: ['darwin'],
      },
    ],
  };
  const result = mapInventory({ entries }, closure);
  assert.equal(result.entries[0].resolvedVersion, '3.0.0');
  assert.equal(result.entries[0].forgejo.status, 'present');
  assert.equal(result.entries[1].forgejo.status, 'platform-excluded');
});
test('link duplicate npm URLs and distinguish lock metadata', () => {
  const entries = [
    {
      file: 'package-lock.json',
      kind: 'npm',
      name: 'package',
      version: '1.0.0',
      source: 'https://registry.npmjs.org/package.tgz',
    },
    {
      file: 'package-lock.json',
      kind: 'download-reference',
      source: '"resolved": "https://registry.npmjs.org/package.tgz",',
    },
    {
      file: 'package-lock.json',
      kind: 'download-reference',
      source: '"url": "https://github.com/sponsors/maintainer"',
    },
  ];
  const result = mapInventory(
    { entries },
    { entries: [{ ...item, kind: 'npm', name: 'package', version: '1.0.0' }] },
  );
  assert.equal(result.entries[1].forgejo.status, 'present');
  assert.equal(result.entries[1].forgejo.duplicateOfDiscoveryPosition, 0);
  assert.equal(result.entries[2].forgejo.status, 'non-input-reference');
});
test('resolve image build arguments and minimum Go toolchain versions', () => {
  const entries = [
    { kind: 'go-toolchain', name: 'go', version: '1.27.0' },
    { kind: 'container-from', name: '${BASE}', version: '${BASE}' },
  ];
  const image = {
    reference: 'alpine:3.22@sha256:' + 'b'.repeat(64),
    aliases: ['alpine:3.22'],
    version: '3.22',
    archive: item.archive,
    member: 'manifest.json',
    sha256: item.sha256,
  };
  const closure = {
    entries: [{ ...item, kind: 'go-toolchain', name: 'go', version: '1.27.3' }],
    images: [image],
    buildArguments: { BASE: 'alpine:3.22' },
  };
  const result = mapInventory({ entries }, closure);
  assert.equal(result.entries[0].resolvedVersion, '1.27.3');
  assert.equal(result.entries[1].resolvedName, image.reference);
  assert.equal(result.entries[1].forgejo.evidence[0].sha256, item.sha256);
  assert.equal(
    mapInventory({ entries: [{ ...entries[0], version: '1.28.0' }] }, closure)
      .entries[0].forgejo.status,
    'missing',
  );
});
test('apply a reviewed reference only to its exact source and require group evidence', () => {
  const entry = {
    file: 'Dockerfile',
    kind: 'download-reference',
    name: 'line:1',
    source: 'RUN npm ci',
  };
  const resolution = {
    ...entry,
    forgejo: { status: 'present', inputGroup: 'npm' },
  };
  const closure = { entries: [], referenceResolutions: [resolution] };
  assert.throws(
    () => mapInventory({ entries: [entry] }, closure),
    /input group/,
  );
  closure.inputGroups = {
    npm: {
      closureMembers: [
        {
          kind: 'npm',
          name: 'package',
          version: '1.0.0',
          archive: item.archive,
          member: item.member,
        },
      ],
    },
  };
  assert.throws(
    () => mapInventory({ entries: [entry] }, closure),
    /group member/,
  );
  closure.entries = [
    { ...item, kind: 'npm', name: 'package', version: '1.0.0' },
  ];
  assert.equal(
    mapInventory({ entries: [entry] }, closure).entries[0].forgejo.status,
    'present',
  );
  assert.equal(
    mapInventory(
      { entries: [{ ...entry, source: 'RUN npm ci changed' }] },
      closure,
    ).entries[0].forgejo.status,
    'missing',
  );
});
