import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, readFileSync, writeFileSync, rmSync } from 'node:fs';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import { buildBroker } from '../build.mjs';
import { copyBrokerArtifact } from '../artifact.mjs';
test(
  'one build supplies byte-identical Desktop and plugin broker closures; missing and tampered artifacts fail',
  { skip: process.platform !== 'win32' },
  (t) => {
    const directory = mkdtempSync(join(tmpdir(), 'auriga-auth-artifact-'));
    t.after(() => rmSync(directory, { recursive: true, force: true }));
    const source = join(directory, 'source');
    const manifest = buildBroker(source);
    for (const name of ['desktop', 'plugin']) {
      const output = join(directory, name);
      copyBrokerArtifact(source, output);
      for (const path of [
        'broker-manifest.json',
        ...manifest.files.map((f) => f.path),
      ])
        assert.deepEqual(
          readFileSync(join(output, path)),
          readFileSync(join(source, path)),
        );
      assert.throws(() => copyBrokerArtifact(source, output), /empty/);
    }
    assert.throws(
      () => copyBrokerArtifact(undefined, join(directory, 'missing')),
      /required/,
    );
    writeFileSync(join(source, 'windows/shell-helper.exe'), 'tampered');
    assert.throws(
      () => copyBrokerArtifact(source, join(directory, 'bad')),
      /digest/,
    );
  },
);
