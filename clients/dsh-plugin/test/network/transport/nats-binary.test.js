import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFile, mkdtemp, writeFile } from 'node:fs/promises';
import { join, dirname } from 'node:path';
import { tmpdir } from 'node:os';
import { createHash } from 'node:crypto';

test('test NATS pins exactly match existing Java release and binary locks', async () => {
  const { releaseFor } = await import('./support/nats-binary.js');
  const java = await readFile(
    new URL(
      '../../../../../services/test-support/src/main/java/auriga/testsupport/NatsServer.java',
      import.meta.url,
    ),
    'utf8',
  );
  const pins = (name) =>
    new Map(
      [
        ...java
          .match(
            new RegExp(
              'private static final Map<String, String> ' +
                name +
                '\\s*=([\\s\\S]*?);',
            ),
          )[1]
          .matchAll(/"([^"]+)"\s*,\s*"([0-9a-f]{64})"/g),
      ].map((match) => [match[1], match[2]]),
    );
  const archives = pins('CHECKSUMS'),
    binaries = pins('BINARY_CHECKSUMS');
  for (const platform of ['win32', 'linux', 'darwin'])
    for (const arch of ['x64', 'arm64']) {
      const release = releaseFor(platform, arch);
      assert.match(
        java,
        new RegExp(
          'VERSION = "' + release.version.replaceAll('.', '\\.') + '"',
        ),
      );
      const key = release.name.slice(
        ('nats-server-v' + release.version + '-').length,
      );
      assert.equal(archives.get(key), release.archiveHash);
      assert.equal(binaries.get(key), release.binaryHash);
      assert.equal(
        release.url,
        `https://github.com/nats-io/nats-server/releases/download/v${release.version}/${release.name}`,
      );
    }
  assert.throws(() => releaseFor('linux', 'ia32'), /Unsupported/);
});

test('bad archive is retained and rejected before extraction or executable publication', async () => {
  const { createNatsBinaryPreparer } = await import('./support/nats-binary.js');
  const stageRoot = await mkdtemp(join(tmpdir(), 'auriga-nats-bad-archive-'));
  let extracts = 0;
  const ensure = createNatsBinaryPreparer({
    find: () => null,
    stageRoot,
    fetchImpl: async () => new Response('corrupt archive'),
    extract: async () => {
      extracts++;
      throw new Error('must not extract');
    },
  });
  await assert.rejects(ensure(), /archive SHA-256/);
  assert.equal(extracts, 0);
});

test('bad installed binary fails its pinned SHA before a caller can spawn it', async () => {
  const { createNatsBinaryPreparer } = await import('./support/nats-binary.js');
  const stageRoot = await mkdtemp(join(tmpdir(), 'auriga-nats-bad-binary-'));
  const binary = join(stageRoot, 'nats-server');
  await writeFile(binary, 'untrusted binary');
  let fetches = 0,
    spawns = 0;
  const ensure = createNatsBinaryPreparer({
    find: () => binary,
    stageRoot,
    fetchImpl: async () => {
      fetches++;
      throw new Error('must not fetch');
    },
  });
  await assert.rejects(
    ensure().then(() => {
      spawns++;
    }),
    /binary SHA-256/,
  );
  assert.equal(spawns, 0);
  assert.equal(fetches, 0);
});

test('download failure and oversized response fail explicitly without extraction', async () => {
  const { createNatsBinaryPreparer } = await import('./support/nats-binary.js');
  for (const fetchImpl of [
    async () => {
      throw new Error('fixture network unavailable');
    },
    async () => new Response('', { status: 503 }),
    async () =>
      new Response('', {
        headers: { 'content-length': String(33 * 1024 * 1024) },
      }),
  ]) {
    let extracts = 0;
    const stageRoot = await mkdtemp(join(tmpdir(), 'auriga-nats-fetch-fail-'));
    const ensure = createNatsBinaryPreparer({
      find: () => null,
      stageRoot,
      fetchImpl,
      extract: async () => {
        extracts++;
      },
    });
    await assert.rejects(ensure(), /unavailable|HTTP 503|too large/);
    assert.equal(extracts, 0);
  }
});

test('empty cache really downloads and verifies the fixed release with concurrent callers sharing one complete binary', async () => {
  const { createNatsBinaryPreparer, releaseFor } =
    await import('./support/nats-binary.js');
  const stageRoot = await mkdtemp(
    join(tmpdir(), 'auriga-nats-clean-download-'),
  );
  const prepare = createNatsBinaryPreparer({ find: () => null, stageRoot });
  const [one, two] = await Promise.all([prepare(), prepare()]);
  assert.equal(one, two);
  const bytes = await readFile(one);
  assert.equal(
    createHash('sha256').update(bytes).digest('hex'),
    releaseFor().binaryHash,
  );
  const archive = await readFile(join(dirname(one), releaseFor().name));
  let spawns = 0;
  const corruptExtract = createNatsBinaryPreparer({
    find: () => null,
    stageRoot,
    fetchImpl: async () => new Response(archive),
    extract: async (_archive, _member, target) =>
      writeFile(target, 'bad extracted binary'),
  });
  await assert.rejects(
    corruptExtract().then(() => {
      spawns++;
    }),
    /binary SHA-256/,
  );
  assert.equal(spawns, 0);
  await writeFile(one, 'changed after first use');
  await assert.rejects(prepare(), /binary SHA-256/);
});
