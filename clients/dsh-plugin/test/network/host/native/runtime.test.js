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
  const sessionPath = join(dir, 'session.js');
  const agentLoopPath = join(dir, 'agent-loop.js');
  const manifestPath = join(dir, 'package.json');
  await writeFile(controllerPath, 'controller');
  await writeFile(persistencePath, 'persistence');
  await writeFile(sessionPath, 'session');
  await writeFile(agentLoopPath, 'agent-loop');
  await writeFile(
    manifestPath,
    JSON.stringify({
      version: PINNED_HOST.version,
    }),
  );
  const request = {
    manifestPath,
    controllerPath,
    persistencePath,
    sessionPath,
    agentLoopPath,
  };
  const fingerprint = await readRuntimeFingerprint({
    ...request,
    version: 'forged',
    build: 'forged',
    dirty: true,
  });
  assert.equal(fingerprint.version, PINNED_HOST.version);
  assert.equal('build' in fingerprint, false);
  assert.equal('dirty' in fingerprint, false);
  assert.equal(
    fingerprint.controllerSha256,
    createHash('sha256').update('controller').digest('hex'),
  );
  assert.equal(
    fingerprint.persistenceSha256,
    createHash('sha256').update('persistence').digest('hex'),
  );
  assert.equal(
    fingerprint.sessionSha256,
    createHash('sha256').update('session').digest('hex'),
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
  await writeFile(manifestPath, JSON.stringify({ version: null }));
  await assert.rejects(readRuntimeFingerprint(request), {
    code: 'network.host_incompatible',
  });
});
