import { generateKeyPairSync, sign, verify } from 'node:crypto';
import { resolve } from 'node:path';
import { pathToFileURL } from 'node:url';
import { hashFile, options } from './lib.mjs';

export function verifyDigest(digest, signature, publicKey) {
  if (!/^[a-f0-9]{64}$/.test(digest)) throw new Error('Expected lowercase SHA-256 hex');
  return verify(null, Buffer.from(digest, 'hex'), publicKey, signature);
}
// Both keys stay in memory. Production injects a signer and resolves a trusted kid externally.
export async function testSignature(path) {
  const digest = await hashFile(path);
  const { privateKey, publicKey } = generateKeyPairSync('ed25519');
  const signature = sign(null, Buffer.from(digest, 'hex'), privateKey);
  const valid = verifyDigest(digest, signature, publicKey);
  const altered = (digest[0] === '0' ? '1' : '0') + digest.slice(1);
  const tamperRejected = !verifyDigest(altered, signature, publicKey);
  const wrongKeyRejected = !verifyDigest(digest, signature, generateKeyPairSync('ed25519').publicKey);
  if (!valid || !tamperRejected || !wrongKeyRejected) throw new Error('Ed25519 verification failed');
  return { digest, algorithm: 'Ed25519', messageEncoding: 'raw-sha256-32-bytes', valid, tamperRejected, wrongKeyRejected, keysPersisted: false };
}
if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  const args = options(process.argv.slice(2), ['archive', 'sha256']);
  if (!args.archive || !args.sha256) throw new Error('Usage: node verify.mjs --archive <file> --sha256 <trusted digest>');
  const actual = await hashFile(resolve(args.archive));
  if (actual !== args.sha256.replace(/^sha256:/, '')) throw new Error('Archive digest mismatch');
  console.log(JSON.stringify(await testSignature(resolve(args.archive)), null, 2));
}
