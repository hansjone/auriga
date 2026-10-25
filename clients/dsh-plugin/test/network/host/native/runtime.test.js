import assert from 'node:assert/strict';
import test from 'node:test';
import { createHash } from 'node:crypto';
import { mkdtemp, writeFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import {
  PINNED_HOST,
  readRuntimeFingerprint,
  verifyRuntimeFingerprint,
} from '../../../../src/network/host/native/index.js';

test('unpacked runtime fingerprints hash actual bytes and refuse missing files without path leakage', async (t) => {
  const dir = await mkdtemp(join(tmpdir(), 'auriga-native-'));
  t.after(() => rm(dir, { recursive: true, force: true }));
  const controllerPath = join(dir, 'controller.js');
  const persistencePath = join(dir, 'persistence.js');
  await writeFile(controllerPath, 'controller');
  await writeFile(persistencePath, 'persistence');
  const request = { ...PINNED_HOST, controllerPath, persistencePath };
  const fingerprint = await readRuntimeFingerprint(request);
  assert.equal(
    fingerprint.controllerSha256,
    createHash('sha256').update('controller').digest('hex'),
  );
  assert.equal(
    fingerprint.persistenceSha256,
    createHash('sha256').update('persistence').digest('hex'),
  );
  assert.throws(() => verifyRuntimeFingerprint(fingerprint), {
    code: 'network.host_incompatible',
  });
  await assert.rejects(
    readRuntimeFingerprint({
      ...request,
      controllerPath: join(dir, 'missing'),
    }),
    (error) =>
      error.code === 'network.host_incompatible' &&
      !error.message.includes(dir),
  );
});
