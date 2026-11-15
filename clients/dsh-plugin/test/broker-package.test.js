import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, mkdir, writeFile, readFile, rm } from 'node:fs/promises';
import { createHash } from 'node:crypto';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import {
  verifyBrokerPackage,
  writeBrokerInstallation,
} from '../src/local/broker-package.js';
import { packageBroker } from '../scripts/package-broker.mjs';

async function artifact(t) {
  const root = await mkdtemp(join(tmpdir(), 'auriga-broker-package-'));
  t.after(() => rm(root, { recursive: true, force: true }));
  const source = join(root, 'source');
  await mkdir(join(source, 'windows'), { recursive: true });
  const files = [];
  for (const path of ['ipc.mjs', 'schema.mjs', 'windows/shell-helper.exe']) {
    const bytes = Buffer.from('artifact fixture only; never executed');
    await writeFile(join(source, path), bytes);
    files.push({
      path,
      sha256: createHash('sha256').update(bytes).digest('hex'),
    });
  }
  await writeFile(
    join(source, 'broker-manifest.json'),
    JSON.stringify({ version: 1, files }),
  );
  return { root, source };
}
test('plugin packaging copies the identical C artifact and manifest', async (t) => {
  const { root, source } = await artifact(t),
    destination = join(root, 'package');
  const manifest = await packageBroker(source, destination);
  assert.deepEqual((await verifyBrokerPackage(destination)).manifest, manifest);
  assert.deepEqual(await packageBroker(source, destination), manifest);
});
test('modified or unlisted broker executable bytes are rejected', async (t) => {
  const { source } = await artifact(t);
  await writeFile(join(source, 'ipc.mjs'), 'modified');
  await assert.rejects(verifyBrokerPackage(source), /digest mismatch/);
});
test('manifest path traversal is rejected before importing any code', async (t) => {
  const { source } = await artifact(t);
  await writeFile(
    join(source, 'broker-manifest.json'),
    JSON.stringify({
      version: 1,
      files: [{ path: '../escape', sha256: '0'.repeat(64) }],
    }),
  );
  await assert.rejects(verifyBrokerPackage(source), /manifest invalid/);
});
test('unlisted executable and conflicting artifact versions are refused', async (t) => {
  const { root, source } = await artifact(t),
    destination = join(root, 'package');
  await packageBroker(source, destination);
  await writeFile(join(destination, 'extra.mjs'), 'extra');
  await assert.rejects(verifyBrokerPackage(destination), /unlisted/);
});
test('independent restart preserves an installation-level raw-token opt-out', async (t) => {
  const { root } = await artifact(t),
    installation = join(root, 'installation');
  const config = { platformOrigin: 'https://platform.example.com' };
  await writeBrokerInstallation(installation, config);
  const file = join(installation, 'desktop-origin.json');
  await writeFile(
    file,
    JSON.stringify({
      origin: config.platformOrigin,
      allowRawAgentToken: false,
    }),
  );
  await writeBrokerInstallation(installation, config);
  assert.equal(
    JSON.parse(await readFile(file, 'utf8')).allowRawAgentToken,
    false,
  );
});
test('invalid stored raw-token policy is not replaced with an enabled default', async (t) => {
  const { root } = await artifact(t),
    installation = join(root, 'installation');
  await mkdir(installation);
  const file = join(installation, 'desktop-origin.json'),
    prior = JSON.stringify({ allowRawAgentToken: 'false' });
  await writeFile(file, prior);
  await assert.rejects(
    writeBrokerInstallation(installation, {
      platformOrigin: 'https://platform.example.com',
    }),
    /installation invalid/,
  );
  assert.equal(await readFile(file, 'utf8'), prior);
});
