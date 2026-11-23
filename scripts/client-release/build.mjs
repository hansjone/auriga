/** Sequential, local-only Windows build, using exactly the existing CI recipes. */
import { spawnSync } from 'node:child_process';
import { Buffer } from 'node:buffer';
import {
  cpSync,
  copyFileSync,
  existsSync,
  realpathSync,
  mkdirSync,
  readFileSync,
  writeFileSync,
  rmSync,
} from 'node:fs';
import {
  basename,
  delimiter,
  dirname,
  join,
  relative,
  isAbsolute,
  resolve,
  sep,
} from 'node:path';
import { ROOT, BUILD_NODE, digest, verifyInputs, physical } from './inputs.mjs';
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
import {
  sourceIdentity,
  copyBuildSource,
  assertBuildCopyMatches,
} from './source.mjs';
import { clientPlatform } from './platform.mjs';
const inputTrust = (o) => ({
  verifyKey: o['verify-key'],
  trustFingerprint: o['trust-fingerprint'],
});
// 联网派生的现场套件（--online）与现场缓存套件（--site-kit，auriga.exe client build
// 从 deps.cache 取用）没有发行签名，由派生时算出 / 缓存写入时记录的清单摘要校验；
// 源码身份仍按 inputTrust 验签。
const kitTrust = (o) =>
  o['site-derived'] || o['site-kit'] ? {} : inputTrust(o);
// 现场系统工具基线（auriga deps baseline --client 写在 deps.cache 下）。
const toolsBaselineOption = () =>
  process.env.AURIGA_CLIENT_TOOLS_BASELINE || null;
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
  if (o['site-kit'] === true) {
    if (o.online === true) throw new Error('--site-kit 只能与 --offline 同用');
    if (!/^[a-f0-9]{64}$/.test(o['devkit-sha256'] ?? ''))
      throw new Error(
        '--site-kit 需要 --devkit-sha256（现场缓存写入时记录的 windows-client.json 摘要）',
      );
  }
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
    'temp',
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
  const kit = verifyInputs(options.devkit, options['devkit-sha256'], ROOT, {
    ...kitTrust(options),
    toolsBaseline: toolsBaselineOption(),
  });
  launchOfflineBuild(options, kit, hooks);
}
/**
 * 已校验的套件 kit 上的断网构建：--offline 与 --online 派生套件共用，
 * 输出目录、环境过滤、网络守卫完全一致。
 * hooks.spawn 供测试注入；hooks.siteDependencies 是要复制进输出目录的审阅清单路径；
 * hooks.root 是源码目录（默认本脚本所在源码树）。
 * 源码树只读：把源码（不含 node_modules/.git 等）复制到 <out>/build-src，工作进程在副本里
 * 安装依赖并构建，结束后（成功或失败）删除副本。源码身份仍按用户给的源码目录校验。
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
  const customTemp =
    typeof options.temp === 'string' && options.temp.length > 0;
  const tempDir = customTemp ? resolve(options.temp) : join(out, 'tmp');
  if (customTemp) {
    physical(dirname(tempDir));
    if (isInside(tempDir, kit.directory))
      throw new Error('临时目录不得位于只读 devkit 内');
  }
  const sourceRoot = resolve(hooks.root ?? ROOT);
  mkdirSync(out); // No overwrite/reuse of old release or review material.
  mkdirSync(tempDir, { recursive: true });
  const buildRoot = join(out, 'build-src');
  const optionsFile = join(out, 'site-options.json');
  if (hooks.siteDependencies)
    copyFileSync(hooks.siteDependencies, join(out, 'site-dependencies.json'));
  // 工作进程只需要“用这个已校验套件做断网构建”；联网相关选项不进入工作进程。
  const worker = { ...options };
  delete worker.online;
  delete worker['site-devkit'];
  delete worker['base-pnpm-lock'];
  delete worker.temp;
  writeFileSync(
    optionsFile,
    JSON.stringify({
      ...worker,
      'source-root': sourceRoot,
      offline: true,
      out,
      devkit: kit.directory,
      ...(options.online === true || options['site-kit'] === true
        ? { 'site-derived': true, 'devkit-sha256': kit.manifestSha256 }
        : {}),
    }),
  );
  const env = Object.fromEntries(
    Object.entries(process.env).filter(
      ([name]) =>
        !/proxy/i.test(name) &&
        !/^npm_config_/i.test(name) &&
        !/^(temp|tmp)$/i.test(name),
    ),
  );
  Object.assign(env, {
    NODE_OPTIONS: `--max-old-space-size=1024 --require="${join(buildRoot, 'scripts/client-release/offline-guard.cjs').replaceAll('\\', '/')}"`,
    AURIGA_CLIENT_NETWORK_LOG: join(out, 'network-denials.log'),
    TEMP: tempDir,
    TMP: tempDir,
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
  let result;
  try {
    copyBuildSource(sourceRoot, buildRoot);
    result = spawn(
      join(kit.directory, clientPlatform().buildNode),
      [
        join(buildRoot, 'scripts/client-release/cli.mjs'),
        '_build-worker',
        optionsFile,
      ],
      { cwd: buildRoot, env, stdio: 'inherit', windowsHide: true },
    );
  } finally {
    // 副本里有 pnpm 装出的超长路径 node_modules，用 Node 的 rmSync 删除。
    try {
      rmSync(buildRoot, {
        recursive: true,
        force: true,
        maxRetries: 3,
        retryDelay: 200,
      });
    } catch (error) {
      console.warn(
        `警告：无法删除临时构建目录 ${buildRoot}（${error.message}）；请稍后用 Node 删除：node -e "require('fs').rmSync(process.argv[1],{recursive:true,force:true})" "${buildRoot}"`,
      );
    }
    if (!customTemp) {
      try {
        rmSync(tempDir, {
          recursive: true,
          force: true,
          maxRetries: 3,
          retryDelay: 200,
        });
      } catch (error) {
        console.warn(
          `警告：无法删除构建临时目录 ${tempDir}（${error.message}）`,
        );
      }
    }
  }
  if (result.status !== 0)
    throw new Error(
      `Windows 离线构建失败（退出码 ${result.status}），保留输出目录供排查`,
    );
  return { out };
}
export function systemToolsEvidence(match) {
  if (!match) return null;
  if (match.source !== 'baseline') return { source: match.source };
  const baseline = match.baseline;
  return {
    source: 'baseline',
    baselinePath: toolsBaselineOption(),
    windowsVersion: baseline.windowsVersion ?? null,
    registeredAt: baseline.registeredAt ?? null,
    toolsSha256: digest(canonical(baseline.tools)),
  };
}
export function collectPluginModulePaths(
  root,
  kitDirectory,
  env = process.env,
) {
  const configured = (env.AURIGA_CLIENT_PLUGIN_MODULES ?? '')
    .split(delimiter)
    .map((item) => item.trim())
    .filter(Boolean)
    .map((item) => resolve(item));
  const candidates = [
    join(root, 'clients/dsh-plugin/node_modules'),
    kitDirectory ? join(kitDirectory, 'plugin-deps/node_modules') : null,
    join(root, 'web/node_modules'),
    join(root, 'node_modules'),
  ].filter((dir) => dir && existsSync(dir));
  const seen = new Set();
  const result = [];
  for (const dir of [...configured, ...candidates]) {
    const key = process.platform === 'win32' ? dir.toLowerCase() : dir;
    if (seen.has(key)) continue;
    seen.add(key);
    result.push(dir);
  }
  return result;
}
export function ensureWorkspaceEsbuildPublicHoist(root) {
  const file = join(root, 'pnpm-workspace.yaml');
  if (!existsSync(file)) return false;
  const text = readFileSync(file, 'utf8');
  if (/^\s*publicHoistPattern\s*:/m.test(text)) return false;
  const eol = text.includes('\r\n') ? '\r\n' : '\n';
  const suffix = `${text.endsWith('\n') ? '' : eol}publicHoistPattern:${eol}  - '*'${eol}`;
  writeFileSync(file, text + suffix, 'utf8');
  return true;
}
export async function buildWorker(optionsFile, hooks = {}) {
  const root = hooks.root ?? ROOT;
  if (
    process.version !== `v${BUILD_NODE}` ||
    !process.env.AURIGA_CLIENT_NETWORK_LOG ||
    !process.env.NODE_OPTIONS?.includes('offline-guard.cjs')
  )
    throw new Error('必须从受控离线入口构建');
  const o = JSON.parse(readFileSync(optionsFile, 'utf8'));
  const policy = validateBuildOptions(o);
  const kit = verifyInputs(o.devkit, o['devkit-sha256'], root, {
    ...kitTrust(o),
    toolsBaseline: toolsBaselineOption(),
    ...(hooks.checkTools === false ? { checkTools: false } : {}),
  });
  // ROOT 是 <out>/build-src 里的副本；源码身份与“构建期间未变化”都按用户的源码目录。
  const sourceRoot = o['source-root'] ?? root;
  const source = sourceIdentity(sourceRoot, inputTrust(o), { forBuild: true });
  // 复制与校验之间源码可能被改动：副本必须与刚校验的源码身份一致，才允许装依赖和构建。
  if (o['source-root'])
    assertBuildCopyMatches(root, sourceRoot, inputTrust(o), source);
  // pnpm 11 的项目级 publicHoistPattern 配置位于 pnpm-workspace.yaml（CLI --config.public-hoist-pattern[] 不再生效）。
  // 在已通过 verifyInputs 与 assertBuildCopyMatches 校验的构建副本里注入，不改动原源码或套件锁摘要。
  if (o['source-root']) ensureWorkspaceEsbuildPublicHoist(root);
  const pnpm = join(kit.directory, 'build-pnpm/node_modules/pnpm/bin/pnpm.cjs');
  const store = join(o.out, 'pnpm-store');
  cpSync(join(kit.directory, 'pnpm-store'), store, { recursive: true });
  const run = (args, env = process.env) => {
    const result = spawnSync(process.execPath, args, {
      cwd: root,
      env,
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
  const pluginModules = collectPluginModulePaths(root, kit.directory);
  const brokerEnv = pluginModules.length
    ? {
        ...process.env,
        AURIGA_CLIENT_PLUGIN_MODULES: pluginModules.join(delimiter),
      }
    : process.env;
  const broker = join(o.out, 'broker');
  run([join(root, 'clients/shell-auth/build.mjs'), broker], brokerEnv);
  const pluginVersion = JSON.parse(
    readFileSync(join(root, 'clients/dsh-plugin/package.json')),
  ).version;
  const pluginOut = join(o.out, 'plugin');
  run([
    join(root, 'scripts/plugin/package.mjs'),
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
    sourceIdentity(sourceRoot, inputTrust(o)).sourceTreeSha256 !==
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
        nativeTools:
          kit.toolsMatch?.source === 'baseline'
            ? kit.toolsMatch.baseline.tools
            : kit.tools,
        // 收据记录系统工具依据：发行清单，或现场登记的基线（路径、Windows 版本、登记时间、摘要）。
        systemTools: systemToolsEvidence(kit.toolsMatch),
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
