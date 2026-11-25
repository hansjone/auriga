/** PR373 wire format: raw SHA256SUMS signed with Ed25519, standard base64. */
import { Buffer, isUtf8 } from 'node:buffer';
import { createHash, createPublicKey, verify } from 'node:crypto';
import { readFileSync, statSync, lstatSync } from 'node:fs';
import { join } from 'node:path';
import { physical } from './platform.mjs';

// Source manifests contain thousands of file keys; the resource statement
// parser's 512-key object limit is intentionally smaller. Keep a separate,
// bounded duplicate-key scan, and let JSON.parse validate the full grammar.
export function parseMetadataJson(bytes) {
  if (!bytes.length || bytes.length > 32 * 1024 ** 2 || !isUtf8(bytes))
    throw new Error('发行元数据大小或UTF-8无效');
  const text = bytes.toString('utf8');
  const parsed = JSON.parse(text, (_key, value) => {
    if (typeof value === 'number' && !Number.isSafeInteger(value))
      throw new Error('发行元数据含不安全数值');
    return value;
  });
  let index = 0,
    count = 0;
  const ws = () => {
    while (index < text.length && /\s/.test(text[index])) index++;
  };
  const string = () => {
    const start = index++;
    while (index < text.length) {
      if (text[index] === '\\') {
        index += 2;
        continue;
      }
      if (text[index++] === '"') break;
    }
    const value = JSON.parse(text.slice(start, index));
    if (!value.isWellFormed() || value.length > 65536)
      throw new Error('发行元数据字符串无效');
    return value;
  };
  const scan = (depth) => {
    if (depth > 16 || ++count > 500000) throw new Error('发行元数据结构超限');
    ws();
    if (text[index] === '"') {
      string();
      return;
    }
    const open = text[index];
    if (open === '{' || open === '[') {
      const close = open === '{' ? '}' : ']',
        keys = new Set();
      index++;
      ws();
      while (text[index] !== close) {
        if (open === '{') {
          const key = string();
          if (keys.has(key)) throw new Error('发行元数据重复键');
          keys.add(key);
          ws();
          index++;
        }
        scan(depth + 1);
        ws();
        if (text[index] === close) break;
        index++;
        ws();
      }
      index++;
      return;
    }
    while (index < text.length && !/[\s,}\]]/.test(text[index])) index++;
  };
  scan(0);
  ws();
  if (index !== text.length) throw new Error('发行元数据格式无效');
  return parsed;
}

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
  if (
    trustFingerprint !== undefined &&
    trustFingerprint !== null &&
    !/^[a-f0-9]{64}$/.test(trustFingerprint)
  )
    throw new Error(
      '--trust-fingerprint 必须是独立批准的 64 位小写 SPKI SHA-256',
    );
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
    throw new Error(
      `发行公钥指纹不匹配：实际 SPKI SHA-256=${fingerprint}，独立批准期望=${trustFingerprint}；请核对套件/源码验签公钥 --verify-key，勿使用站点 --release-public-key`,
    );
  const sums = smallFile(join(root, 'SHA256SUMS'), 4 * 1024 ** 2);
  const encoded = smallFile(join(root, 'SHA256SUMS.sig'), 1024)
    .toString('ascii')
    .trim();
  if (
    !/^[A-Za-z0-9+/]{86}==$/.test(encoded) ||
    !verify(null, sums, publicKey, Buffer.from(encoded, 'base64'))
  )
    throw new Error(
      `发行 SHA256SUMS 签名不匹配：实际验签公钥 SPKI SHA-256=${fingerprint}；${trustFingerprint ? `独立批准期望=${trustFingerprint}` : '未提供独立批准指纹，不从未验材料推断期望值'}；请核对 --verify-key 为套件/源码发行验签公钥，站点 --release-public-key 用于安装包桌面发行`,
    );
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
      if (hash(bytes) !== entries.get(name))
        throw new Error(`签名元数据摘要不匹配：${name}`);
      return parseMetadataJson(bytes);
    },
  };
}
