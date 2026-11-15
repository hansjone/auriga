import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { mkdtemp, readFile, rm, readdir } from 'node:fs/promises';
import { resolve } from 'node:path';
import { download, validate } from './download.mjs';

const data = Buffer.from('verified input');
const config = { forgejo: 'https://forgejo.example.com' };
const artifact = {
  file: 'go.tar.gz',
  path: '/api/packages/alice/generic/deps/v1/go.tar.gz',
  sha256: createHash('sha256').update(data).digest('hex'),
  bytes: data.length,
};
test('reject external locations, redirects, traversal, credentials and invalid hashes before fetching', async () => {
  for (const patch of [
    { path: '//evil.example/x' },
    { path: '/api/packages/a/generic/d/v/../x' },
    { path: '/api/packages/a/generic/d/v/%2e%2e' },
    { file: '../x' },
    { sha256: 'bad' },
  ]) {
    assert.throws(() =>
      validate(config, { artifacts: [{ ...artifact, ...patch }] }),
    );
  }
  assert.throws(() =>
    validate(
      { forgejo: 'https://alice:secret@forgejo.example.com' },
      { artifacts: [artifact] },
    ),
  );
  assert.throws(() => validate(config, { artifacts: [artifact, artifact] }));
});
test('verify streamed bytes; refuse checksum mismatch and existing output', async () => {
  const dir = await mkdtemp(resolve('.agent-forgejo/download-test-'));
  try {
    const request = async (url, options) => {
      assert.equal(url.origin, config.forgejo);
      assert.equal(options.redirect, 'error');
      return new Response(data);
    };
    await download(config, { artifacts: [artifact] }, dir, request);
    assert.deepEqual(await readFile(resolve(dir, artifact.file)), data);
    await assert.rejects(
      download(config, { artifacts: [artifact] }, dir, request),
      /EEXIST/,
    );
    const bad = { ...artifact, file: 'bad.tar.gz', sha256: '0'.repeat(64) };
    await assert.rejects(
      download(config, { artifacts: [bad] }, dir, request),
      /checksum/,
    );
    await assert.rejects(
      download(config, { artifacts: [{ ...bad, bytes: 1 }] }, dir, request),
      /size exceeded/,
    );
    assert.deepEqual(await readdir(dir), [artifact.file]);
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
});
