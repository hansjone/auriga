import { spawnSync } from 'node:child_process';
import { packagePlugin } from '../../../scripts/plugin/package.mjs';
import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  mkdtempSync,
  readFileSync,
  writeFileSync,
  rmSync,
  mkdirSync,
} from 'node:fs';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import { buildBroker } from '../build.mjs';
import { copyBrokerArtifact, verifyBrokerArtifact } from '../artifact.mjs';
test(
  'one build supplies byte-identical Desktop and plugin broker closures; missing and tampered artifacts fail',
  { skip: process.platform !== 'win32' },
  async (t) => {
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
    const pluginSource = join(directory, 'clients', 'dsh-plugin');
    mkdirSync(join(pluginSource, 'src'), { recursive: true });
    mkdirSync(join(pluginSource, 'ui'));
    writeFileSync(
      join(pluginSource, 'package.json'),
      JSON.stringify({
        name: 'auriga-dsh',
        version: '1.2.3',
        files: ['src', 'ui'],
      }),
    );
    for (const file of ['ui/auriga-team-panel.js', 'ui/metadata.json'])
      writeFileSync(join(pluginSource, file), 'fixture');
    const packaged = await packagePlugin({
      root: directory,
      version: '1.2.3',
      outDir: join(directory, 'tgz'),
      build: false,
      brokerArtifact: source,
    });
    const unpacked = join(directory, 'unpacked');
    mkdirSync(unpacked);
    const tar = join(process.env.SystemRoot, 'System32', 'tar.exe');
    const result = spawnSync(tar, ['-xzf', packaged.tarball, '-C', unpacked], {
      encoding: 'utf8',
      windowsHide: true,
    });
    assert.equal(result.status, 0, result.stderr);
    assert.deepEqual(
      verifyBrokerArtifact(join(unpacked, 'package', 'shell-auth')),
      manifest,
    );
    assert.throws(
      () => copyBrokerArtifact(undefined, join(directory, 'missing')),
      /required/,
    );
    // Mutating a non-helper file must fail the plugin consumer's full check.
    writeFileSync(join(directory, 'plugin', 'broker.mjs'), 'tampered');
    assert.throws(
      () => verifyBrokerArtifact(join(directory, 'plugin')),
      /digest/,
    );
    writeFileSync(join(source, 'windows/shell-helper.exe'), 'tampered');
    assert.throws(
      () => copyBrokerArtifact(source, join(directory, 'bad')),
      /digest/,
    );
  },
);
