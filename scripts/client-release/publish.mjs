/** Normal edge admin session + CSRF, never direct storage/database access. */
/* global fetch, AbortSignal */
import { createReadStream, readFileSync, statSync } from 'node:fs';
import { createHash } from 'node:crypto';
import { Buffer } from 'node:buffer';
import {
  verifyRelease,
  canonical,
} from '../../clients/installer/runtime/release.mjs';
import { sha256File } from '../../clients/installer/runtime/verify.mjs';
import { normalizePlatformBinding } from '../../clients/installer/runtime/platform-origin.mjs';
import { fingerprint } from './keys.mjs';

function origin(value, confirmation) {
  const url = new URL(value);
  if (url.origin !== value)
    throw new Error('发布入口必须为规范 origin，不含路径、凭据或查询');
  return normalizePlatformBinding(value, {
    accessMode: url.protocol === 'http:' ? 'ip' : undefined,
    confirmedOrigin: confirmation,
  }).origin;
}
async function request(url, options = {}) {
  const response = await fetch(url, {
    ...options,
    redirect: 'error',
    signal: AbortSignal.timeout(30 * 60 * 1000),
  });
  if (!response.ok) {
    let code;
    try {
      // Only the bounded, recognized problem code is diagnostic evidence.
      // Response titles/details may contain credentials or private addresses.
      code = (await json(response)).code;
    } catch {
      await response.body?.cancel().catch(() => {});
    }
    let reason;
    if (response.status === 401)
      reason = '管理会话未认证或已失效；请在正常管理台重新登录后重试';
    else if (response.status === 403 && code === 'iam.step_up_required')
      reason =
        '平台要求近期身份验证（iam.step_up_required）；请在正常管理台完成近期身份验证后立即重试';
    else if (response.status === 403 && code === 'common.forbidden')
      reason =
        '平台拒绝访问（common.forbidden）；请核对管理员的发行权限与站点授权';
    else if (response.status === 403)
      reason =
        '平台拒绝访问，未返回可识别原因；请核对管理会话、近期身份验证及发行权限，不能据此判定会话过期';
    else reason = '请检查平台状态及管理台发行账本后重试';
    throw new Error(`平台请求失败 HTTP ${response.status}；${reason}`);
  }
  return response;
}
async function json(response) {
  let bytes = 0;
  const chunks = [];
  for await (const chunk of response.body) {
    bytes += chunk.length;
    if (bytes > 1024 * 1024) throw new Error('平台 JSON 超限');
    chunks.push(chunk);
  }
  return JSON.parse(Buffer.concat(chunks).toString('utf8'));
}
export async function publish(options) {
  const admin = origin(options.adminOrigin, options.confirmHttpOrigin);
  const portal = origin(
    options.portalOrigin,
    options.confirmPortalHttpOrigin ?? options.confirmHttpOrigin,
  );
  const auth = options.auth;
  if (
    !auth ||
    typeof auth.cookie !== 'string' ||
    !auth.cookie ||
    typeof auth.csrf !== 'string' ||
    !auth.csrf ||
    /[\r\n]/.test(auth.cookie + auth.csrf)
  )
    throw new Error('需正常管理员会话 cookie 和 csrf，仅通过环境变量传入');
  const publicKey = readFileSync(options.publicKey, 'utf8');
  const record = JSON.parse(readFileSync(options.buildRecord, 'utf8'));
  if (fingerprint(publicKey) !== record.publicKeyFingerprint)
    throw new Error('发布公钥指纹与构建记录 publicKeyFingerprint 不符');
  const envelope = JSON.parse(readFileSync(options.manifest, 'utf8'));
  verifyRelease(envelope, {
    publicKey,
    currentVersion: envelope.rollbackFrom ?? envelope.minimumVersion,
    channel: envelope.channel,
  });
  if (
    statSync(options.archive).size !== envelope.sizeBytes ||
    sha256File(options.archive) !== envelope.sha256
  )
    throw new Error('安装包摘要与已签清单不符');
  const encoded = Buffer.from(JSON.stringify(envelope)).toString('base64url');
  if (encoded.length > 43700) throw new Error('发行清单超限');
  const headers = {
    Cookie: auth.cookie,
    Origin: admin,
    'X-Auriga-CSRF': auth.csrf,
  };
  const endpoint = admin + '/api/resource/v1/admin/desktop/releases';
  const ledger = await json(await request(endpoint, { headers }));
  if (!Array.isArray(ledger.items)) throw new Error('非法平台发行账本');
  if (
    ledger.items.some(
      (item) =>
        !Number.isSafeInteger(item.manifest?.sequence) ||
        item.manifest.sequence >= envelope.sequence,
    )
  )
    throw new Error(
      '发行序号必须大于平台全部历史序号（包括下架版本、其他渠道）',
    );
  await json(
    await request(endpoint, {
      method: 'POST',
      headers: {
        ...headers,
        'Content-Type': 'application/octet-stream',
        'Content-Length': String(envelope.sizeBytes),
        'X-Desktop-Manifest': encoded,
      },
      body: createReadStream(options.archive),
      duplex: 'half',
    }),
  );
  const latest = await json(
    await request(
      `${portal}/api/resource/v1/desktop/latest?channel=${encodeURIComponent(envelope.channel)}`,
    ),
  );
  verifyRelease(latest, {
    publicKey,
    currentVersion: envelope.rollbackFrom ?? envelope.minimumVersion,
    channel: envelope.channel,
  });
  if (canonical(latest) !== canonical(envelope))
    throw new Error(
      '发布后 latest 与本次清单不一致；发布可能已成功，请先核对管理台',
    );
  const download = await request(
    `${portal}/api/resource/v1/desktop/packages/${envelope.sequence}`,
  );
  let bytes = 0;
  const hash = createHash('sha256');
  for await (const chunk of download.body) {
    bytes += chunk.length;
    if (bytes > envelope.sizeBytes) throw new Error('发布后下载摘要或长度不符');
    hash.update(chunk);
  }
  if (bytes !== envelope.sizeBytes || hash.digest('hex') !== envelope.sha256)
    throw new Error('发布后下载摘要或长度不符；发布可能已成功，请检查平台');
  return {
    sequence: envelope.sequence,
    version: envelope.version,
    channel: envelope.channel,
    sha256: envelope.sha256,
    verifiedBytes: bytes,
  };
}
