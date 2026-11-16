import test from 'node:test';
import { Buffer } from 'node:buffer';
import assert from 'node:assert/strict';
import { mkdir, mkdtemp, readFile, writeFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import { join } from 'node:path';
import { gunzipSync } from 'node:zlib';
import { packPlugin, prepare } from './prepare.mjs';

test('strict archive uses only the explicit file map and rejects escape paths', () => {
  const files = new Map([
    ['package/example.txt', Buffer.from('public fixture')],
  ]);
  assert.deepEqual(packPlugin(files), packPlugin(files));
  assert.match(gunzipSync(packPlugin(files)).toString(), /public fixture/);
  assert.throws(() =>
    packPlugin(new Map([['package/../escape', Buffer.from('x')]])),
  );
});

test(
  'clean dependency installs reproduce archives and ignore stale output files',
  {
    skip: !process.env.RESOURCE_SEED_SOURCE,
  },
  async () => {
    const root = fileURLToPath(
      new URL('../../seed-resources/', import.meta.url),
    );
    await mkdir(root, { recursive: true });
    const output = await mkdtemp(join(root, 'repack-test-'));
    const stale = join(output, 'memwiki/package');
    await mkdir(stale, { recursive: true });
    await writeFile(
      join(stale, 'private-stale.txt'),
      'do-not-publish-stale-fixture',
    );
    const first = await prepare(process.env.RESOURCE_SEED_SOURCE, output);
    const firstBytes = await readFile(join(output, first.summary[0].filename));
    const second = await prepare(process.env.RESOURCE_SEED_SOURCE, output);
    const secondBytes = await readFile(
      join(output, second.summary[0].filename),
    );
    assert.notEqual(first.run, second.run);
    assert.deepEqual(firstBytes, secondBytes);
    assert.deepEqual(first.files, second.files);
    assert.ok(
      !gunzipSync(secondBytes).includes(
        Buffer.from('do-not-publish-stale-fixture'),
      ),
    );
    const provenance = JSON.parse(
      await readFile(join(output, 'build-source.json'), 'utf8'),
    );
    assert.match(provenance.sourceCommit, /^[a-f0-9]{40}$/);
    assert.ok(provenance.bundledDependencies.length > 0);
    assert.ok(
      provenance.bundledDependencies.every((item) => item.name && item.version),
    );
    const manifest = JSON.parse(
      await readFile(join(output, 'dsh-memwiki.auriga-resource.json'), 'utf8'),
    );
    assert.ok(manifest.permissions.tools.includes('memwiki_secret_get'));
    assert.ok(manifest.permissions.tools.includes('memwiki_secret_delete'));
    assert.match(manifest.description, /push.*user-configured.*plaintext/);
    assert.deepEqual(manifest.permissions.outboundHosts, []);
  },
);
