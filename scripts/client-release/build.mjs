/** Sequential, local-only Windows build, using exactly the existing CI recipes. */
import { spawnSync } from 'node:child_process';
import { Buffer } from 'node:buffer';
import {
  cpSync,
  copyFileSync,
  realpathSync,
  mkdirSync,
  readFileSync,
  writeFileSync,
} from 'node:fs';
import {
  basename,
  dirname,
  join,
  relative,
  isAbsolute,
  resolve,
  sep,
} from 'node:path';
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
// 联网派生的现场套件没有发行签名，由派生时刚算出的清单摘要校验；
// 源码身份仍按 inputTrust 验签。
const kitTrust = (o) => (o['site-derived'] ? {} : inputTrust(o));
/** 路径 child 是否等于 parent 或位于其内（Windows 不区分大小写）。 */
export function canonicalPath(path) {
  // 对已存在的最近祖先取真实路径（展开 8.3 短名、junction），再接回不存在的部分。
  const rest = [];
  let current = resolve(path);
  for (;;) {
    try {
      const real = join(realpathSync.native(current), ...rest.reverse());
      return process.platform === 'win32' ? real.toLowerCase() : real;
    } catch {
      const parent = dirname(current);
      if (parent === current) {
        const plain = resolve(path);
        return process.platform === 'win32' ? plain.toLowerCase() : plain;
      }
      rest.push(basename(current));
      current = parent;
    }
  }
}
export function isInside(child, parent) {
  const rel = relative(canonicalPath(parent), canonicalPath(child));
  return (
    rel === '' ||
    (rel !== '..' && !rel.startsWith('..' + sep) && !isAbsolute(rel))
  );
}
export function validateBuildOptions(o) {
  if (o.online === true && o.offline === true)
    throw new Error('--online 与 --offline 互斥，只能二选一');
  if (o.online !== true && o.offline !== true)
    throw new Error(
      '现场构建必须显式指定 --offline（套件锁与源码一致）或 --online（联网补依赖）',
    );
  if (o.online === true) {
    if (!o['verify-key'] && !o['trust-fingerprint'])
      throw new Error(
        '--online 的基础套件必须是发行签名的，需要 --verify-key 或 --trust-fingerprint',
      );
    if (o['devkit-sha256'])
      throw new Error(
        '--online 不接受 --devkit-sha256，基础套件必须验发行签名',
      );
    if (typeof o['site-devkit'] !== 'string' || !o['site-devkit'])
      throw new Error(
        '缺少 --site-devkit（用于存放联网派生出的现场套件的新目录）',
      );
  } else if (o['site-devkit'])
    throw new Error('--site-devkit 只能与 --online 同用');
  if (o.online !== true && o['base-pnpm-lock'])
    throw new Error('--base-pnpm-lock 只能与 --online 同用');
  for (const key of [
    'devkit',
    'out',
    'release-public-key',
    'resource-trust-json',
    'version',
    'sequence',
  ])
    if (typeof o[key] !== 'string' || !o[key]) throw new Error(`缺少 --${key}`);
  if (o.online === true) {
    const site = resolve(o['site-devkit']);
    const base = resolve(o.devkit);
    const out = resolve(o.out);
    if (
      isInside(site, base) ||
      isInside(base, site) ||
      isInside(site, out) ||
      isInside(out, site)
    )
      throw new Error(
        '--site-devkit 必须是全新目录，且不能与基础套件或 --out 互相包含',
      );
    if (isInside(out, base)) throw new Error('构建输出不得位于只读 devkit 内');
  }
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
export function resolveBuildPaths(options) {
  options = { ...options };
  for (const key of [
    'devkit',
    'site-devkit',
    'base-pnpm-lock',
    'out',
    'release-public-key',
    'resource-trust-json',
    'verify-key',
  ])
    if (options[key]) options[key] = resolve(options[key]);
  return options;
}
export function startOfflineBuild(options, hooks = {}) {
  options = resolveBuildPaths(options);
  if (options.online === true) throw new Error('--online 请走联网派生入口');
  validateBuildOptions(options);
  sourceIdentity(ROOT, inputTrust(options), { forBuild: true });
  const kit = verifyInputs(
    options.devkit,
    options['devkit-sha256'],
    ROOT,
    inputTrust(options),
  );
  launchOfflineBuild(options, kit, hooks);
}
/**
 * 已校验的套件 kit 上的断网构建：--offline 与 --online 派生套件共用，
 * 输出目录、环境过滤、网络守卫完全一致。
 * hooks.spawn 供测试注入；hooks.siteDependencies 是要复制进输出目录的审阅清单路径。
 */
export function launchOfflineBuild(options, kit, hooks = {}) {
  const spawn = hooks.spawn ?? spawnSync;
  const out = resolve(options.out);
  physical(dirname(out));
  if (
    out.toLowerCase().startsWith(kit.directory.toLowerCase() + '\\') ||
    out === kit.directory
  )
    throw new Error('构建输出不得位于只读 devkit 内');
  mkdirSync(out); // No overwrite/reuse of old release or review material.
  const optionsFile = join(out, 'site-options.json');
  if (hooks.siteDependencies)
    copyFileSync(hooks.siteDependencies, join(out, 'site-dependencies.json'));
  // 工作进程只需要“用这个已校验套件做断网构建”；联网相关选项不进入工作进程。
  const worker = { ...options };
  delete worker.online;
  delete worker['site-devkit'];
  delete worker['base-pnpm-lock'];
  writeFileSync(
    optionsFile,
    JSON.stringify({
      ...worker,
      offline: true,
      out,
      devkit: kit.directory,
      ...(options.online === true
        ? { 'site-derived': true, 'devkit-sha256': kit.manifestSha256 }
        : {}),
    }),
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
  const result = spawn(
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
  return { out };
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
  const kit = verifyInputs(o.devkit, o['devkit-sha256'], ROOT, kitTrust(o));
  const source = sourceIdentity(ROOT, inputTrust(o), { forBuild: true });
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
