/** PR373 wire format: raw SHA256SUMS signed with Ed25519, standard base64. */
import { Buffer } from 'node:buffer';
import { createHash, createPublicKey, verify } from 'node:crypto';
import { readFileSync, statSync, lstatSync } from 'node:fs';
import { join } from 'node:path';
import { physical } from './platform.mjs';
import { sha256File } from '../../clients/installer/runtime/verify.mjs';
import { parseStrictJson } from '../../clients/installer/runtime/json.mjs';

export const hash = (bytes) => createHash('sha256').update(bytes).digest('hex');
export function smallFile(path, limit) {
  if (!lstatSync(path).isFile() || statSync(path).size > limit)
    throw new Error('签名材料类型或大小无效');
  const bytes = readFileSync(path);
  if (bytes.length > limit) throw new Error('签名材料超限');
  return bytes;
}
export function verifySignedInventory(
  root,
  { verifyKey, trustFingerprint } = {},
) {
  if (!verifyKey && !/^[a-f0-9]{64}$/.test(trustFingerprint ?? ''))
    throw new Error(
      '必须提供独立批准发行公钥 --verify-key 或 --trust-fingerprint',
    );
  physical(root);
  const keyFile = verifyKey ?? join(root, 'release-public.pem');
  physical(keyFile);
  const publicKey = createPublicKey(smallFile(keyFile, 64 * 1024));
  if (publicKey.asymmetricKeyType !== 'ed25519')
    throw new Error('发行公钥必须为 Ed25519');
  const fingerprint = hash(publicKey.export({ type: 'spki', format: 'der' }));
  if (trustFingerprint && trustFingerprint !== fingerprint)
    throw new Error('发行公钥指纹不匹配');
  const sums = smallFile(join(root, 'SHA256SUMS'), 4 * 1024 ** 2);
  const encoded = smallFile(join(root, 'SHA256SUMS.sig'), 1024)
    .toString('ascii')
    .trim();
  if (
    !/^[A-Za-z0-9+/]{86}==$/.test(encoded) ||
    !verify(null, sums, publicKey, Buffer.from(encoded, 'base64'))
  )
    throw new Error('发行 SHA256SUMS 签名不匹配');
  const entries = new Map();
  for (const line of sums.toString('utf8').trimEnd().split('\n')) {
    if (!/^[a-f0-9]{64} {2}[^\r\n]+$/.test(line))
      throw new Error('发行摘要清单格式无效');
    const name = line.slice(66);
    if (entries.has(name)) throw new Error('发行摘要清单重复路径');
    entries.set(name, line.slice(0, 64));
  }
  return {
    fingerprint,
    verifyMetadata(name) {
      if (!/^[a-z0-9.-]+\.json$/.test(name) || !entries.has(name))
        throw new Error('缺少签名元数据摘要');
      const file = join(root, name);
      const bytes = smallFile(file, 32 * 1024 ** 2);
      if (sha256File(file) !== entries.get(name))
        throw new Error(`签名元数据摘要不匹配：${name}`);
      return parseStrictJson(bytes);
    },
  };
}
