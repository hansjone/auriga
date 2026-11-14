import { createHash } from 'node:crypto';
import { Buffer } from 'node:buffer';
import { open, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { pathToFileURL } from 'node:url';
import { platformConfig } from '../../clients/dsh-plugin/src/local/config.js';
import { parseStrictJson } from '../../clients/dsh-plugin/src/network/trust/jcs.js';
import {
  positiveVersion,
  publicKey,
} from '../../clients/dsh-plugin/src/network/trust/keyset.js';
import { openV2TrustStore as openTrustStore } from '../../clients/dsh-plugin/src/network/trust/v2-store.js';
import {
  detach,
  digest,
  structure,
  verifyControl,
} from '../../clients/dsh-plugin/src/network/trust/v2.js';
import { jsonLimits } from '../../clients/dsh-plugin/src/network/trust/v2-schema.js';
import { createControlInstallation } from '../../clients/dsh-plugin/src/network/trust/installation-control.js';

const usage =
  '用法：node scripts/network/export-installation.mjs [--control|--import-control] <manifest.json> [新输出文件.json]';
const fail = (message) => {
  throw new Error(message);
};
const exact = (value, keys) =>
  value &&
  typeof value === 'object' &&
  !Array.isArray(value) &&
  Object.keys(value).sort().join(',') === [...keys].sort().join(',');
const uuid = (value) =>
  typeof value === 'string' &&
  /^[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}$/.test(value);
const kid = (value) =>
  typeof value === 'string' && /^[A-Za-z0-9._-]{1,128}$/.test(value);

// Explicit local public inputs only; bound reads also reject private DER/PEM
// before reading key bytes. Ed25519 canonical SPKI DER is exactly 44 bytes.
async function readLocal(file, limit, exactSize = false) {
  if (typeof file !== 'string' || !file.trim()) fail('缺少本地材料文件路径');
  const handle = await open(file, 'r');
  try {
    const stat = await handle.stat();
    if (
      !stat.isFile() ||
      stat.size > limit ||
      (exactSize && stat.size !== limit)
    )
      fail('本地材料文件类型或大小无效');
    const bytes = Buffer.alloc(limit + 1);
    const { bytesRead } = await handle.read(bytes, 0, bytes.length, 0);
    if (bytesRead > limit || (exactSize && bytesRead !== limit))
      fail('本地材料文件大小无效');
    return bytes.subarray(0, bytesRead);
  } finally {
    await handle.close();
  }
}

/** Local consistency checks only. Neither unsigned registration provenance nor
 * the independent fingerprint channel can be authenticated by this helper.
 * No network calls, filesystem trust cache, private keys or DSH profile access.
 */
export async function exportInstallation(manifestFile, now = Date.now) {
  const manifest = parseStrictJson(await readLocal(manifestFile, 65536));
  if (
    !exact(manifest, [
      'connect',
      'expectedIdentity',
      'registrationFile',
      'minimumVersion',
      'roots',
      'signedKeysetFile',
      'announcementFile',
      'gateway',
      'wssUrl',
    ])
  )
    fail('manifest 字段无效');
  const local = (file) => {
    if (typeof file !== 'string' || !file.trim()) fail('缺少本地材料文件路径');
    return path.resolve(path.dirname(path.resolve(manifestFile)), file);
  };
  const json = async (file) =>
    detach(await readLocal(local(file), jsonLimits.maxBytes));
  const spki = async (file) => {
    const encoded = (await readLocal(local(file), 44, true)).toString(
      'base64url',
    );
    publicKey(encoded);
    return encoded;
  };
  const expected = manifest.expectedIdentity;
  if (
    !exact(expected, ['tenant', 'device']) ||
    !uuid(expected.tenant) ||
    !uuid(expected.device)
  )
    fail('可信运维核对的 tenant/device 无效');
  const registration = await json(manifest.registrationFile);
  if (
    !exact(registration, [
      'tenant',
      'realm',
      'node',
      'device',
      'systemAddress',
      'created',
    ]) ||
    registration.tenant !== expected.tenant ||
    registration.device !== expected.device ||
    !positiveVersion(registration.realm) ||
    !positiveVersion(registration.node) ||
    typeof registration.created !== 'boolean' ||
    registration.systemAddress !==
      `aur1:${BigInt(registration.realm).toString(16)}.${BigInt(registration.node).toString(16)}.0`
  )
    fail('register 与已核对身份或权威地址不一致');
  if (!positiveVersion(manifest.minimumVersion))
    fail('必须提供受信最低版本检查点');
  if (
    !Array.isArray(manifest.roots) ||
    manifest.roots.length < 1 ||
    manifest.roots.length > 16
  )
    fail('必须提供 1–16 个离线根');
  const roots = [];
  for (const root of manifest.roots) {
    if (
      !exact(root, ['kid', 'publicKeyFile', 'fingerprintFile']) ||
      !kid(root.kid)
    )
      fail('离线根字段无效');
    const encoded = await spki(root.publicKeyFile);
    const fingerprint = (await readLocal(local(root.fingerprintFile), 128))
      .toString('utf8')
      .trim();
    if (
      !/^sha256:[0-9a-f]{64}$/.test(fingerprint) ||
      fingerprint !==
        'sha256:' +
          createHash('sha256')
            .update(Buffer.from(encoded, 'base64url'))
            .digest('hex')
    )
      fail('根公钥与独立确认的指纹不一致');
    roots.push({ kid: root.kid, public_key_spki: encoded });
  }
  if (
    !manifest.connect ||
    Object.hasOwn(manifest.connect, 'network') ||
    Object.hasOwn(manifest.connect, 'allowLocalHttp') ||
    typeof manifest.connect.platformOrigin !== 'string' ||
    !/^https:\/\/[^/?#@\s]+\/?$/.test(manifest.connect.platformOrigin)
  )
    fail('必须提供明确 HTTPS 平台配置');
  const connect = platformConfig({
    ...manifest.connect,
    network: {
      realm: registration.realm,
      wssUrl: manifest.wssUrl,
      roots,
    },
  });
  // Cache exists only inside this call; no persistent high-water mark is reset.
  let snapshot;
  const trust = await openTrustStore({
    cache: {
      load: async () => undefined,
      commit: async (value) => {
        snapshot = value;
      },
    },
    roots: connect.network.roots,
    realm: registration.realm,
    tenant_id: registration.tenant,
    now,
    requireAnnouncement: true,
  });
  await trust.update(await json(manifest.signedKeysetFile));
  await trust.announce(await json(manifest.announcementFile));
  trust.assertUsable();
  if (BigInt(trust.version) < BigInt(manifest.minimumVersion))
    fail('根签清单低于受信最低版本');
  const gateway = manifest.gateway;
  if (!exact(gateway, ['kid', 'publicKeyFile']) || !kid(gateway.kid))
    fail('gateway 公钥配置无效');
  const checked = trust.key(gateway.kid, 'protocol_response', trust.version);
  if (
    snapshot.publication.keyset.payload.keys.find(
      (key) => key.kid === gateway.kid,
    )?.state !== 'active' ||
    checked.publicKey
      .export({ format: 'der', type: 'spki' })
      .toString('base64url') !== (await spki(gateway.publicKeyFile))
  )
    fail('gateway 当前协议公钥与根签 active 条目不一致');
  return { connect };
}

/** Export only a root-verified public control package. The installation file is
 * selected locally by the operator; the package never supplies its own roots. */
export async function exportControlPackage(manifestFile) {
  const manifest = detach(await readLocal(manifestFile, 65536));
  if (
    !exact(manifest, [
      'installationFile',
      'controlPackageFile',
      'expectedIdentity',
    ]) ||
    !exact(manifest.expectedIdentity, ['tenant', 'realm', 'target'])
  )
    fail('控制导出 manifest 字段无效');
  const local = (file) =>
    path.resolve(path.dirname(path.resolve(manifestFile)), file);
  const installation = detach(
    await readLocal(local(manifest.installationFile), 65536),
  );
  const config = platformConfig(installation.connect);
  const expected = manifest.expectedIdentity;
  if (
    !config.network ||
    config.network.realm !== expected.realm ||
    !positiveVersion(expected.realm) ||
    typeof expected.tenant !== 'string' ||
    !expected.tenant ||
    typeof expected.target !== 'string' ||
    !expected.target
  )
    fail('控制安装身份无效');
  const pack = structure(
    'ControlPackage',
    detach(
      await readLocal(local(manifest.controlPackageFile), jsonLimits.maxBytes),
    ),
  );
  if (
    pack.tenant_id !== expected.tenant ||
    pack.realm !== expected.realm ||
    pack.target_id !== expected.target ||
    digest(pack.control) !== pack.control_digest ||
    Buffer.from(pack.challenge, 'base64url').length < 32 ||
    Buffer.from(pack.challenge, 'base64url').toString('base64url') !==
      pack.challenge
  )
    fail('控制包绑定无效');
  verifyControl(pack.control, {
    tenant_id: expected.tenant,
    realm: expected.realm,
    roots: new Map(
      config.network.roots.map((root) => [root.kid, root.public_key_spki]),
    ),
  });
  return pack;
}

/** Offline owner command: explicit local manifests, same pin/key loader and
 * single-writer trust cache as the settings flow. No messaging connection. */
export async function importControlPackage(manifestFile) {
  const manifest = detach(await readLocal(manifestFile, 65536));
  if (
    !exact(manifest, ['controlManifestFile', 'networkDirectory']) ||
    typeof manifest.controlManifestFile !== 'string' ||
    !manifest.controlManifestFile.trim() ||
    typeof manifest.networkDirectory !== 'string' ||
    !manifest.networkDirectory.trim()
  )
    fail('控制导入 manifest 字段无效');
  const local = (file) =>
    path.resolve(path.dirname(path.resolve(manifestFile)), file);
  const controlFile = local(manifest.controlManifestFile);
  const pack = await exportControlPackage(controlFile);
  const controlManifest = detach(await readLocal(controlFile, 65536));
  const installationFile = path.resolve(
    path.dirname(controlFile),
    controlManifest.installationFile,
  );
  const installation = detach(await readLocal(installationFile, 65536));
  const config = platformConfig(installation.connect);
  const identity = controlManifest.expectedIdentity;
  return createControlInstallation({
    config,
    directory: local(manifest.networkDirectory),
    getScope: () => ({
      tenant_id: identity.tenant,
      realm: identity.realm,
      target_id: identity.target,
    }),
  }).importPackage(pack);
}

async function main(args) {
  if (args.length === 1 && args[0] === '--help') {
    process.stdout.write(usage + '\n');
    return;
  }
  const controlOnly = args[0] === '--control';
  const importing = args[0] === '--import-control';
  if (controlOnly || importing) args = args.slice(1);
  if (
    args.length < 1 ||
    args.length > 2 ||
    args.some((value) => value.startsWith('--'))
  )
    fail(usage);
  const result =
    JSON.stringify(
      await (importing
        ? importControlPackage(args[0])
        : controlOnly
          ? exportControlPackage(args[0])
          : exportInstallation(args[0])),
      null,
      2,
    ) + '\n';
  if (args[1]) await writeFile(args[1], result, { flag: 'wx', mode: 0o600 });
  else process.stdout.write(result);
}

if (
  process.argv[1] &&
  import.meta.url === pathToFileURL(path.resolve(process.argv[1])).href
) {
  main(process.argv.slice(2)).catch(() => {
    // Never echo input JSON, tokens, key bytes, paths or native parser errors.
    process.stderr.write(
      '操作失败：请核对公开材料、独立安装身份、有效期与新输出文件路径；可用 --help 查看用法。\n',
    );
    process.exitCode = 1;
  });
}
