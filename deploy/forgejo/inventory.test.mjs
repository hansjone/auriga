import { test } from 'node:test';
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { readFileSync, rmSync } from 'node:fs';
import { validate } from './download.mjs';

test('discover locked inputs and retain unresolved closure without inventing checksums', () => {
  const output = '.agent-forgejo/inventory-test.json';
  try {
    execFileSync(process.execPath, ['deploy/forgejo/inventory.mjs', output]);
    const result = JSON.parse(readFileSync(output));
    const entries = result.entries;
    assert.equal(result.completeness, 'static-discovery-only');
    assert.ok(
      entries.some((e) => e.kind === 'npm' && e.digest?.startsWith('sha512-')),
    );
    assert.ok(
      entries.some((e) => e.kind === 'pnpm' && e.digest?.startsWith('sha512-')),
    );
    assert.ok(
      entries.some((e) => e.kind === 'go-sum' && e.digest?.startsWith('h1:')),
    );
    assert.ok(
      entries.some(
        (e) => e.kind === 'maven-wrapper' && /^[a-f0-9]{64}$/.test(e.digest),
      ),
    );
    assert.ok(entries.some((e) => e.kind === 'maven-plugin'));
    assert.ok(
      entries.some(
        (e) =>
          e.kind === 'python' &&
          e.name === 'cryptography' &&
          e.version === '46.0.7',
      ),
    );
    assert.ok(
      entries.some(
        (e) => e.kind === 'dsh-tool' && e.name === 'nodeImage' && e.digest,
      ),
    );
    assert.ok(
      entries.every(
        (e) => e.kind !== 'container-from' || e.file.includes('Dockerfile'),
      ),
    );
    assert.ok(entries.some((e) => e.version === null && e.digest === null));
    assert.ok(
      entries.every((e) => e.forgejo.status === 'missing-or-unverified'),
    );
  } finally {
    rmSync(output, { force: true });
  }
});
test('published artifact metadata is accepted without credentials or private hostnames', () => {
  const manifest = JSON.parse(readFileSync('deploy/forgejo/artifacts.json'));
  validate(
    { forgejo: 'https://forgejo.example.com', owner: 'alice' },
    manifest,
  );
  assert.ok(manifest.artifacts.length > 400);
});
