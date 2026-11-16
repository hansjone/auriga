/** Sequential, local-only Windows build, using exactly the existing CI recipes. */
import { spawnSync } from 'node:child_process';
import { Buffer } from 'node:buffer';
import { cpSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { ROOT, BUILD_NODE, verifyInputs, physical } from './inputs.mjs';
import {
  canonical,
  RELEASE_KID,
  verifyRelease,
} from '../../clients/installer/runtime/release.mjs';
import { sign, generateKeyPairSync } from 'node:crypto';
import { parse as parseVersion } from '../../clients/installer/runtime/semver-lite.mjs';
import {
  validateResourceRoots,
  build,
} from '../../clients/installer/scripts/build.mjs';
import { makeRelease } from '../../clients/installer/scripts/make-release.mjs';
import { fingerprint } from './keys.mjs';

export { sourceIdentity } from './source.mjs';
import { sourceIdentity } from './source.mjs';
import { clientPlatform } from './platform.mjs';
const inputTrust = (o) => ({
  verifyKey: o['verify-key'],
  trustFingerprint: o['trust-fingerprint'],
});
export function validateBuildOptions(o) {
  if (o.offline !== true) throw new Error('现场构建必须显式指定 --offline');
  for (const key of [
    'devkit',
    'out',
    'release-public-key',
    'resource-trust-json',
    'version',
    'sequence',
  ])
    if (typeof o[key] !== 'string' || !o[key]) throw new Error(`缺少 --${key}`);
  parseVersion(o.version);
  const target = clientPlatform(o.target);
  const unsigned = {
    schema: 2,
    product: 'Auriga Desktop',
    version: o.version,
    packageId: 'validation',
    channel: o.channel ?? 'stable',
    platform: target.target,
    sizeBytes: 1,
    sha256: '0'.repeat(64),
    minimumVersion: o['minimum-version'] ?? '0.0.0',
    publishedAt: new Date(Date.now() - 1000).toISOString(),
    notAfter:
      o['not-after'] ?? new Date(Date.now() + 30 * 86400000).toISOString(),
    sequence: Number(o.sequence),
    notes: o.notes ?? '',
    components: {
      dshVersion: '0.0.0',
      nodeVersion: '0.0.0',
      pluginVersion: '0.0.0',
    },
    rolloutPercentage: Number(o['rollout-percentage'] ?? 100),
    rollbackFrom: o['rollback-from'] ?? null,
  };
  // Validate policy with the production verifier before expensive construction.
  const pair = generateKeyPairSync('ed25519');
  verifyRelease(
    {
      ...unsigned,
      signature: {
        alg: 'Ed25519',
        kid: RELEASE_KID,
        value: sign(
          null,
          Buffer.from(canonical(unsigned)),
          pair.privateKey,
        ).toString('base64url'),
      },
    },
    {
      publicKey: pair.publicKey,
      channel: unsigned.channel,
      currentVersion: unsigned.rollbackFrom ?? unsigned.minimumVersion,
    },
  );
  physical(o['release-public-key']);
  physical(o['resource-trust-json']);
  fingerprint(readFileSync(o['release-public-key']));
  validateResourceRoots(
    JSON.parse(readFileSync(o['resource-trust-json'], 'utf8')),
  );
  return unsigned;
}
export function startOfflineBuild(options) {
  options = { ...options };
  for (const key of [
    'devkit',
    'out',
    'release-public-key',
    'resource-trust-json',
    'verify-key',
  ])
    if (options[key]) options[key] = resolve(options[key]);
  validateBuildOptions(options);
  const kit = verifyInputs(
    options.devkit,
    options['devkit-sha256'],
    ROOT,
    inputTrust(options),
  );
  const out = resolve(options.out);
  physical(dirname(out));
  if (
    out.toLowerCase().startsWith(kit.directory.toLowerCase() + '\\') ||
    out === kit.directory
  )
    throw new Error('构建输出不得位于只读 devkit 内');
  mkdirSync(out); // No overwrite/reuse of old release or review material.
  const optionsFile = join(out, 'site-options.json');
  writeFileSync(
    optionsFile,
    JSON.stringify({ ...options, out, devkit: kit.directory }),
  );
  const env = Object.fromEntries(
    Object.entries(process.env).filter(
      ([name]) => !/proxy/i.test(name) && !/^npm_config_/i.test(name),
    ),
  );
  Object.assign(env, {
    NODE_OPTIONS: `--max-old-space-size=1024 --require="${join(ROOT, 'scripts/client-release/offline-guard.cjs').replaceAll('\\', '/')}"`,
    AURIGA_CLIENT_NETWORK_LOG: join(out, 'network-denials.log'),
    npm_config_offline: 'true',
    npm_config_ignore_scripts: 'true',
    npm_config_audit: 'false',
    npm_config_fund: 'false',
    npm_config_update_notifier: 'false',
    npm_config_cache: join(out, 'npm-cache'),
    CI: 'true',
    RELEASE_BUILD: 'true',
    GITHUB_REF_NAME: '',
    GITHUB_EVENT_NAME: '',
    GITHUB_REF: '',
    DESKTOP_SOURCE_ON_MAIN: '',
  });
  writeFileSync(env.AURIGA_CLIENT_NETWORK_LOG, '');
  const result = spawnSync(
    join(kit.directory, clientPlatform().buildNode),
    [
      join(ROOT, 'scripts/client-release/cli.mjs'),
      '_build-worker',
      optionsFile,
    ],
    { cwd: ROOT, env, stdio: 'inherit', windowsHide: true },
  );
  if (result.status !== 0)
    throw new Error(
      `Windows 离线构建失败（退出码 ${result.status}），保留输出目录供排查`,
    );
}
export async function buildWorker(optionsFile) {
  if (
    process.version !== `v${BUILD_NODE}` ||
    !process.env.AURIGA_CLIENT_NETWORK_LOG ||
    !process.env.NODE_OPTIONS?.includes('offline-guard.cjs')
  )
    throw new Error('必须从受控离线入口构建');
  const o = JSON.parse(readFileSync(optionsFile, 'utf8'));
  const policy = validateBuildOptions(o);
  const kit = verifyInputs(o.devkit, o['devkit-sha256'], ROOT, inputTrust(o));
  const source = sourceIdentity(ROOT, inputTrust(o));
  const pnpm = join(kit.directory, 'build-pnpm/node_modules/pnpm/bin/pnpm.cjs');
  const store = join(o.out, 'pnpm-store');
  cpSync(join(kit.directory, 'pnpm-store'), store, { recursive: true });
  const run = (args) => {
    const result = spawnSync(process.execPath, args, {
      cwd: ROOT,
      stdio: 'inherit',
      windowsHide: true,
    });
    if (result.status !== 0)
      throw new Error(`离线构建步骤失败（${result.status}）：${args[0]}`);
  };
  run([
    pnpm,
    'install',
    '--offline',
    '--frozen-lockfile',
    '--ignore-scripts',
    '--store-dir',
    store,
  ]);
  const broker = join(o.out, 'broker');
  run([join(ROOT, 'clients/shell-auth/build.mjs'), broker]);
  const pluginVersion = JSON.parse(
    readFileSync(join(ROOT, 'clients/dsh-plugin/package.json')),
  ).version;
  const pluginOut = join(o.out, 'plugin');
  run([
    join(ROOT, 'scripts/plugin/package.mjs'),
    '--version',
    pluginVersion,
    '--broker-artifact',
    broker,
    '--out',
    pluginOut,
  ]);
  const result = await build({
    offline: true,
    'offline-inputs': kit.directory,
    'offline-inputs-sha256': kit.manifestSha256,
    'broker-artifact': broker,
    'plugin-tgz': join(pluginOut, `auriga-dsh-${pluginVersion}.tgz`),
    out: o.out,
    exe: true,
    'desktop-version': o.version,
    'release-public-key': o['release-public-key'],
    'resource-trust-json': o['resource-trust-json'],
    ...(o['platform-origin']
      ? {
          'platform-origin': o['platform-origin'],
          'platform-access-mode': o['platform-access-mode'],
          'confirm-platform-origin': o['confirm-platform-origin'],
        }
      : {}),
  });
  if (
    sourceIdentity(ROOT, inputTrust(o)).sourceTreeSha256 !==
    source.sourceTreeSha256
  )
    throw new Error('源码在构建期间发生变化，请从新目录重建');
  if (readFileSync(process.env.AURIGA_CLIENT_NETWORK_LOG, 'utf8'))
    throw new Error('离线构建检测到被拒绝的联网尝试');
  const record = makeRelease({
    payload: join(result.stage, 'payload'),
    archive: result.exe,
    exe: result.exe,
    output: o.out,
    sequence: policy.sequence,
    minimumVersion: policy.minimumVersion,
    notAfter: policy.notAfter,
    notes: policy.notes,
    channel: policy.channel,
    rolloutPercentage: policy.rolloutPercentage,
    rollbackFrom: policy.rollbackFrom,
    sourceInfo: source,
    siteOffline: {
      schema: 1,
      inputsSha256: kit.manifestSha256,
      lockSha256: kit.lockSha256,
      sourceTreeSha256: source.sourceTreeSha256,
      dirty: source.dirty,
      networkPolicy: 'node-deny-local-native-tools',
    },
  });
  writeFileSync(
    join(o.out, 'offline-evidence.json'),
    JSON.stringify(
      {
        schema: 1,
        node: process.version,
        proxies: false,
        nodeNetworkGuard: true,
        deniedRequests: 0,
        nativeTools: kit.tools,
        inputsSha256: kit.manifestSha256,
        sourceCommit: source.sourceCommit,
        sourceTreeSha256: source.sourceTreeSha256,
        sourceType: source.sourceType,
        siteChanges: source.siteChanges,
        exeSha256: record.exeSha256,
      },
      null,
      2,
    ) + '\n',
  );
  console.log(
    JSON.stringify(
      {
        exe: result.exe,
        zip: result.zip,
        record: join(o.out, 'desktop-build.json'),
        unsignedManifest: join(o.out, 'desktop-release.unsigned.json'),
      },
      null,
      2,
    ),
  );
}
