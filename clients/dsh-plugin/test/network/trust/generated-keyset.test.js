import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { Buffer } from 'node:buffer';
import { generatedKeysetFiles } from '../../../scripts/gen-network-keyset.mjs';

for (const [name, content] of Object.entries(generatedKeysetFiles)) {
  test(`generated network keyset is fresh byte-for-byte: ${name}`, async () => {
    const committed = await readFile(
      new URL('../../../src/generated/network/' + name, import.meta.url),
    );
    assert.deepEqual(committed, Buffer.from(content, 'utf8'));
  });
}
