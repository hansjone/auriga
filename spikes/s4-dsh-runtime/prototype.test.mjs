import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdir, mkdtemp, readFile, writeFile, symlink } from 'node:fs/promises';
import { join } from 'node:path';
import { generateKeyPairSync, sign } from 'node:crypto';
import { HERE, checkManifest, hashFile, inventory, options, redact, run, within, writeJson } from './lib.mjs';
import { pack, header } from './archive.mjs';
import { testSignature, verifyDigest } from './verify.mjs';

async function fixture() {
  await mkdir(join(HERE, '.work'), { recursive: true });
  const temp = await mkdtemp(join(HERE, '.work', 'test-'));
  const root = join(temp, 'artifact'); await mkdir(root);
  await mkdir(join(root, 'deep', '资料'), { recursive: true });
  await writeFile(join(root, 'deep', '资料', 'file.txt'), 'artifact\n');
  return { temp, root };
}
test('deterministic gzip archive round-trips Unicode and long ustar paths', async () => {
  const { temp, root } = await fixture();
  const longDir = join(root, 'a'.repeat(80), 'b'.repeat(80));
  await mkdir(longDir, { recursive: true }); await writeFile(join(longDir, 'payload'), 'long path');
  await writeFile(join(root, 'c'.repeat(120)), 'long basename');
  await pack(root, 'fixture', join(temp, 'one.tar.gz'));
  await pack(root, 'fixture', join(temp, 'two.tar.gz'));
  assert.equal(await hashFile(join(temp, 'one.tar.gz')), await hashFile(join(temp, 'two.tar.gz')));
  const dest = join(temp, 'unpacked'); await mkdir(dest);
  await run('tar', ['-xzf', join(temp, 'one.tar.gz'), '-C', dest]);
  assert.equal(await readFile(join(dest, 'fixture/deep/资料/file.txt'), 'utf8'), 'artifact\n');
  assert.equal(await readFile(join(dest, 'fixture', 'a'.repeat(80), 'b'.repeat(80), 'payload'), 'utf8'), 'long path');
  assert.equal(await readFile(join(dest, 'fixture', 'c'.repeat(120)), 'utf8'), 'long basename');
});
test('manifest rejects edits and added files', async () => {
  const { root } = await fixture();
  await writeJson(join(root, 'manifest.json'), { files: await inventory(root) });
  await checkManifest(root);
  await writeFile(join(root, 'deep/资料/file.txt'), 'changed');
  await assert.rejects(checkManifest(root), /mismatch/);
  const other = await fixture();
  await writeJson(join(other.root, 'manifest.json'), { files: await inventory(other.root) });
  await writeFile(join(other.root, 'unexpected'), 'extra');
  await assert.rejects(checkManifest(other.root), /mismatch/);
});
test('Ed25519 proves signing, tamper rejection and untrusted-key rejection without exporting keys', async () => {
  const { temp } = await fixture();
  const archive = join(temp, 'file'); await writeFile(archive, 'runtime');
  const proof = await testSignature(archive);
  assert.equal(proof.valid && proof.tamperRejected && proof.wrongKeyRejected, true);
  assert.equal(proof.keysPersisted, false);
  const { privateKey, publicKey } = generateKeyPairSync('ed25519');
  const signature = sign(null, Buffer.from(proof.digest, 'hex'), privateKey);
  assert.equal(verifyDigest(proof.digest, signature, publicKey), true);
  signature[0] ^= 1;
  assert.equal(verifyDigest(proof.digest, signature, publicKey), false);
  assert.throws(() => verifyDigest('invalid', signature, publicKey), /SHA-256/);
});
test('argument and path guards reject mistakes and escaping destinations', () => {
  assert.deepEqual(options(['--target', 'win-x64'], ['target']), { target: 'win-x64' });
  for (const argv of [['--target'], ['target', 'x'], ['--target', 'x', '--target', 'y'], ['--other', 'x']]) assert.throws(() => options(argv, ['target']));
  assert.throws(() => within(HERE, HERE));
  assert.throws(() => within(HERE, join(HERE, '..', 'elsewhere')));
  assert.equal(within(HERE, join(HERE, '.work')), join(HERE, '.work'));
  assert.throws(() => header('x'.repeat(300), { mode: '0644', size: 0, type: 'file' }), /long/);
});
test('external symlinks are rejected', { skip: process.platform === 'win32' ? 'Windows developer symlink privilege is deliberately not requested' : false }, async () => {
  const { root, temp } = await fixture();
  await symlink(join(temp, 'outside'), join(root, 'escape'));
  await assert.rejects(inventory(root), /child|Absolute/);
});
test('startup authentication tokens are removed from persisted logs', () => {
  const secret = 's4-test-token_123';
  const log = `dsh web: http://127.0.0.1:12345/?token=${secret}\nsecond line`;
  const clean = redact(log);
  assert.ok(!clean.includes(secret));
  assert.ok(clean.includes('token=<redacted>\nsecond line'));
});
