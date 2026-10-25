/** Adapted from dsh-team/lib/utils/secret-box.js (MIT; ../SOURCES.md). */
import { createCipheriv, createDecipheriv, randomBytes } from 'node:crypto';
import { Buffer } from 'node:buffer';
import { mkdir, readFile, open } from 'node:fs/promises';
import { join } from 'node:path';
const MAGIC = 'auriga-local-session-v1';
/** @param {string} directory @param {{hasEncryptedData?: boolean}} [options] */
export async function loadOrCreateSecretKey(
  directory,
  { hasEncryptedData = false } = {},
) {
  const path = join(directory, '.auriga-local-session-key');
  const read = async () => {
    const raw = (await readFile(path, 'utf8')).trim();
    if (!/^[a-f0-9]{64}$/.test(raw))
      throw new Error('local session key unavailable');
    return Buffer.from(raw, 'hex');
  };
  try {
    return await read();
  } catch (error) {
    if (
      /** @type {NodeJS.ErrnoException} */ (error).code !== 'ENOENT' ||
      hasEncryptedData
    )
      throw new Error('local session key unavailable');
  }
  await mkdir(directory, { recursive: true });
  const key = randomBytes(32);
  let handle;
  try {
    handle = await open(path, 'wx', 0o600);
    await handle.writeFile(key.toString('hex') + '\n', 'utf8');
    await handle.sync();
    return key;
  } catch (error) {
    if (/** @type {NodeJS.ErrnoException} */ (error).code === 'EEXIST')
      return await read();
    throw new Error('local session key unavailable');
  } finally {
    await handle?.close();
  }
}
/** @param {Buffer} key @param {string} plaintext */
export function sealSecret(key, plaintext) {
  const iv = randomBytes(12),
    cipher = createCipheriv('aes-256-gcm', key, iv);
  const ciphertext = Buffer.concat([
    cipher.update(plaintext, 'utf8'),
    cipher.final(),
  ]);
  return [
    MAGIC,
    iv.toString('base64url'),
    cipher.getAuthTag().toString('base64url'),
    ciphertext.toString('base64url'),
  ].join('.');
}
/** @param {Buffer} key @param {string} encoded */
export function openSecret(key, encoded) {
  try {
    const [magic, iv, tag, ciphertext, extra] = encoded.split('.');
    if (magic !== MAGIC || extra !== undefined) return null;
    const decipher = createDecipheriv(
      'aes-256-gcm',
      key,
      Buffer.from(iv, 'base64url'),
    );
    decipher.setAuthTag(Buffer.from(tag, 'base64url'));
    return Buffer.concat([
      decipher.update(Buffer.from(ciphertext, 'base64url')),
      decipher.final(),
    ]).toString('utf8');
  } catch {
    return null;
  }
}
