import test from 'node:test';
import assert from 'node:assert/strict';
import {
  writeFileSync,
  readFileSync,
  existsSync,
  mkdirSync,
  cpSync,
} from 'node:fs';
import { fileURLToPath } from 'node:url';
import { join } from 'node:path';
import { spawnSync } from 'node:child_process';
import { generateKeyPairSync } from 'node:crypto';
import { systemTool } from '../runtime/system-tools.mjs';
import { makeRelease } from '../scripts/make-release.mjs';
import { verifyRelease } from '../runtime/release.mjs';
import { tempBase, buildPayload } from './helpers.mjs';

test('CI 制品记录与离线审阅确认绑定，变更后必须重新审阅', (t) => {
  const key = generateKeyPairSync('ed25519');
  const base = tempBase(t),
    payload = buildPayload(base, {
      releasePublicKey: key.publicKey.export({ type: 'spki', format: 'pem' }),
    }),
    archive = join(base, 'desktop.zip');
  const stage = join(base, 'stage');
  mkdirSync(stage);
  cpSync(payload.dir, join(stage, 'payload'), { recursive: true });
  assert.equal(
    spawnSync(systemTool('tar'), ['-a', '-cf', archive, '-C', stage, '.'])
      .status,
    0,
  );
  const record = makeRelease({
    payload: payload.dir,
    archive,
    output: base,
    sequence: 3,
    event: 'push',
    ref: 'refs/tags/desktop-v1.0.0',
    sourceOnMain: true,
    workflowRun: '1234',
    minimumVersion: '0.0.0',
    notAfter: new Date(Date.now() + 86400000).toISOString(),
  });
  assert.equal(record.sourceCommit.length, 40);
  assert.equal(record.components.pluginVersion, '1.0.0');
  const manifest = join(base, 'desktop-release.unsigned.json'),
    buildRecord = join(base, 'desktop-build.json'),
    output = join(base, 'signed.json');
  const args = [
    `manifest=${manifest}`,
    `buildRecord=${buildRecord}`,
    `archive=${archive}`,
    `output=${output}`,
  ];
  const script = fileURLToPath(
    new URL('../scripts/sign-release.mjs', import.meta.url),
  );
  const call = (extra) =>
    spawnSync(process.execPath, [script, ...args, ...extra], {
      encoding: 'utf8',
    });
  const first = call(['--review-only']);
  assert.equal(first.status, 0, first.stderr);
  const review = JSON.parse(first.stdout);
  assert.equal(review.sha256, record.sha256);
  assert.equal(existsSync(output), false);
  assert.equal(review.ref, 'refs/tags/desktop-v1.0.0');
  assert.match(review.publicKeyFingerprint, /^[a-f0-9]{64}$/);
  for (const patch of [
    { event: 'pull_request' },
    { ref: 'refs/heads/main' },
    { sourceOnMain: false },
    { workflow: 'other.yml' },
    { publicKeyFingerprint: '0'.repeat(64) },
  ]) {
    writeFileSync(buildRecord, JSON.stringify({ ...record, ...patch }));
    assert.equal(call(['--review-only']).status, 1);
  }
  writeFileSync(buildRecord, JSON.stringify(record));
  assert.equal(call([]).status, 1);
  const body = JSON.parse(readFileSync(manifest, 'utf8'));
  body.notes = 'changed';
  writeFileSync(manifest, JSON.stringify(body));
  assert.equal(call([`--confirm-sha256=${review.reviewSha256}`]).status, 1);
  const second = call(['--review-only']);
  assert.equal(second.status, 0, second.stderr);
  const keyFile = join(base, 'private.pem');
  writeFileSync(
    keyFile,
    key.privateKey.export({ type: 'pkcs8', format: 'pem' }),
    { mode: 0o600 },
  );
  const wrongKeyFile = join(base, 'wrong.pem');
  writeFileSync(
    wrongKeyFile,
    generateKeyPairSync('ed25519').privateKey.export({
      type: 'pkcs8',
      format: 'pem',
    }),
  );
  assert.equal(
    call([
      `--confirm-sha256=${JSON.parse(second.stdout).reviewSha256}`,
      `keyFile=${wrongKeyFile}`,
    ]).status,
    1,
  );
  const signed = call([
    `--confirm-sha256=${JSON.parse(second.stdout).reviewSha256}`,
    `keyFile=${keyFile}`,
  ]);
  assert.equal(signed.status, 0, signed.stderr);
  assert.equal((signed.stdout + signed.stderr).includes(keyFile), false);
  assert.equal(
    verifyRelease(JSON.parse(readFileSync(output, 'utf8')), {
      publicKey: key.publicKey,
    }).version,
    '1.0.0',
  );
});
