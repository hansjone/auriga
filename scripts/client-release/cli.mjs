import { parseArgs } from 'node:util';
import { existsSync, readFileSync, writeFileSync, rmSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { buildWorker } from './build.mjs';
import { startBuild } from './online.mjs';
import { generateDesktopKeys, assertPrivateKey } from './keys.mjs';
import { publish } from './publish.mjs';
import { physical } from './inputs.mjs';
import { cleanSource } from './source.mjs';
import { clientPlatform } from './platform.mjs';
import { safeDiagnostic } from './diagnostics.mjs';
import {
  reviewRelease,
  signRelease,
} from '../../clients/installer/scripts/sign-release.mjs';
import { parse as parseVersion } from '../../clients/installer/runtime/semver-lite.mjs';

const HELP = `独立客户端发行（auriga-client.cmd / Node cli.mjs）
  auriga-client keygen --out <新密钥目录>
  auriga-client build --offline --devkit <Windows套件目录> --verify-key <独立批准发行公钥> --release-public-key <公钥.pem> --resource-trust-json <resource-roots.json> --version <SemVer> --sequence <递增整数> --out <新输出目录> [--temp <临时目录>]
  auriga-client build --online --devkit <发行签名套件目录> (--verify-key <公钥> | --trust-fingerprint <指纹>) --site-devkit <新现场套件目录> [--base-pnpm-lock <原始web/pnpm-lock.yaml>] ...其余参数同 --offline
    --online：现场依赖锁（前端 pnpm、dsh-plugin 的 npm）与发行套件不一致时，联网只补变化的依赖，
    把基础套件（只读）派生为 --site-devkit，再按 --offline 同样的断网方式构建。Node/Python/DSH/Java 版本变化仍需维护方出新套件。
    联网步骤透传已设置的 HTTPS_PROXY/HTTP_PROXY/NO_PROXY 与 npm_config_registry；与 --offline 互斥。
  auriga-client build --offline --site-kit --devkit <现场缓存套件> --devkit-sha256 <缓存写入时记录的清单摘要> (--verify-key <公钥> | --trust-fingerprint <指纹>) ...其余参数同 --offline
    --site-kit：套件来自现场可写缓存（auriga deps fetch/import --client），无发行签名，按 --devkit-sha256 校验；
    --verify-key/--trust-fingerprint 此时只用于核验签名源码。通常由 auriga.exe client build 自动传入。
  auriga-client clean-source <源码目录> [--include-ui]
    构建不再向源码树写入；本命令清理旧版本构建留下的 node_modules、dist、.pnpm-store（--include-ui 另含 clients/dsh-plugin/ui），支持超长路径，不删其他文件。
  auriga-client sign --dir <构建输出目录> --review-only
  auriga-client sign --dir <构建输出目录> --key-file <私钥.pem> --confirm-sha256 <审阅摘要>
  auriga-client publish --dir <构建输出目录> --public-key <现场公钥.pem> --admin-origin <管理入口origin> --portal-origin <门户origin>
发布会话由 AURIGA_CLIENT_ADMIN_AUTH 环境变量提供 JSON {cookie,csrf}，先在正常管理台完成近期身份验证。`;
const KEY_HELP = `
公钥用途：
  --verify-key（别名 --devkit-verify-key）：独立批准的套件/源码发行验签公钥；套件自带 release-public.pem 不能自证信任。
  --release-public-key（别名 --site-release-public-key）：站点桌面发行公钥，将写入安装包；与套件验签公钥分开。
  --trust-fingerprint：从独立可信渠道取得的套件/源码发行公钥 SPKI SHA-256，不能从待验套件自取后当作批准值。
  --uac-config-dir <目录>：可选，包含 adapter.json 与 provider.json；实际写入包内 payload/auriga/uac/。未提供则不含本地 UAC 配置。`;

export function normalizeBuildAliases(options) {
  const normalized = { ...options };
  for (const [alias, name] of [
    ['devkit-verify-key', 'verify-key'],
    ['site-release-public-key', 'release-public-key'],
  ]) {
    if (normalized[alias] !== undefined) {
      if (normalized[name] !== undefined)
        throw new Error(`--${alias} 与 --${name} 互斥，请仅提供一个`);
      normalized[name] = normalized[alias];
      delete normalized[alias];
    }
  }
  return normalized;
}

export function formatCliError(action, error, argv = [], env = process.env) {
  const privatePaths = [];
  for (let index = 0; index < argv.length; index++) {
    if (argv[index] === '--key-file') privatePaths.push(argv[++index]);
    else if (argv[index].startsWith('--key-file='))
      privatePaths.push(argv[index].slice('--key-file='.length));
  }
  const causes = [];
  for (
    let current = error;
    current && causes.length < 4;
    current = current.cause
  ) {
    causes.push(
      safeDiagnostic(current.message ?? current, {
        privatePaths,
        env,
        limit: 6000,
      }),
    );
  }
  const summary =
    action === 'sign' ? '客户端发行审阅或签名失败' : '客户端发行失败';
  return `${summary}：${causes.join('；原因：')}\n下一步：${action === 'sign' ? '按上述原因核对制品、构建记录、私钥权限和确认摘要；修复后重新审阅再签名。' : '按上述原因修复参数或环境；发布失败时先核对管理台发行账本，再决定是否重试。'}`;
}
export function parseAdminAuth(raw) {
  try {
    return JSON.parse(raw ?? 'null');
  } catch {
    throw new Error(
      'AURIGA_CLIENT_ADMIN_AUTH 必须是有效 JSON；会话内容不记录到日志',
    );
  }
}
export function signCommand(o) {
  if (!o.dir) throw new Error('缺少 --dir');
  const dir = resolve(o.dir);
  physical(dir);
  const body = JSON.parse(
    readFileSync(join(dir, 'desktop-release.unsigned.json')),
  );
  parseVersion(body.version);
  const options = {
    manifest: join(dir, 'desktop-release.unsigned.json'),
    buildRecord: join(dir, 'desktop-build.json'),
    archive: join(dir, clientPlatform(body.platform).setupName(body.version)),
    siteOffline: true,
  };
  const output = join(dir, 'desktop-release.json');
  const checkpoint = join(dir, 'desktop-release.review.json');
  if (existsSync(output)) throw new Error('已签清单已存在，禁止覆盖');
  const review = reviewRelease(options).summary;
  // 派生套件（build --online）构建的输出目录带 site-dependencies.json：
  // 只在审阅输出里提示，不改签名格式和 desktop-build.json。
  const siteFile = join(dir, 'site-dependencies.json');
  if (existsSync(siteFile)) {
    const site = JSON.parse(readFileSync(siteFile, 'utf8'));
    review.siteDependencies = {
      count: Array.isArray(site.added) ? site.added.length : null,
      removedCount: site.removedCount ?? null,
      warnings: site.warnings ?? [],
      notice: `含现场联网依赖：${Array.isArray(site.added) ? site.added.length : '未知'} 项（见 site-dependencies.json）。输入摘要来自现场派生套件，不在维护方的发行签名之内，签名前请人工审阅该清单。`,
    };
  }
  // 构建时系统工具按现场基线（deps baseline --client）放行的，在审阅输出里提示。
  const evidenceFile = join(dir, 'offline-evidence.json');
  if (existsSync(evidenceFile)) {
    const tools = JSON.parse(readFileSync(evidenceFile, 'utf8')).systemTools;
    if (tools?.source === 'baseline')
      review.systemToolsBaseline = {
        ...tools,
        notice: `系统工具摘要按现场基线放行（${tools.windowsVersion ?? '未知 Windows 版本'}，登记于 ${tools.registeredAt ?? '未知时间'}），与维护方发行清单不同，签名前请确认本机 Windows 更新属预期。`,
      };
  }
  if (o['review-only']) {
    writeFileSync(
      checkpoint,
      JSON.stringify({ reviewSha256: review.reviewSha256 }) + '\n',
    );
    return review;
  }
  if (!o['key-file'] || !o['confirm-sha256'])
    throw new Error('必须先审阅，再指定 --key-file 和 --confirm-sha256');
  if (JSON.parse(readFileSync(checkpoint)).reviewSha256 !== review.reviewSha256)
    throw new Error('制品或记录发生变化，必须重新审阅');
  assertPrivateKey(o['key-file']);
  const signed = signRelease({
    ...options,
    keyFile: o['key-file'],
    confirmSha256: o['confirm-sha256'],
  });
  writeFileSync(output, JSON.stringify(signed, null, 2) + '\n', { flag: 'wx' });
  rmSync(checkpoint);
  return {
    signed: true,
    version: signed.version,
    sequence: signed.sequence,
    sha256: signed.sha256,
  };
}
export async function main(argv) {
  if (argv[0] === '_build-worker' && argv.length === 2)
    return buildWorker(argv[1]);
  if (!argv.length || argv.includes('--help')) {
    console.log(HELP + KEY_HELP);
    return;
  }
  const command = argv[0];
  const allowed = {
    keygen: { out: 'string' },
    build: Object.fromEntries(
      [
        'devkit',
        'target',
        'devkit-sha256',
        'verify-key',
        'devkit-verify-key',
        'trust-fingerprint',
        'out',
        'release-public-key',
        'site-release-public-key',
        'resource-trust-json',
        'uac-config-dir',
        'version',
        'sequence',
        'minimum-version',
        'channel',
        'rollout-percentage',
        'rollback-from',
        'not-after',
        'notes',
        'platform-origin',
        'platform-access-mode',
        'confirm-platform-origin',
        'site-devkit',
        'base-pnpm-lock',
        'temp',
      ]
        .map((name) => [name, 'string'])
        .concat([
          ['offline', 'boolean'],
          ['online', 'boolean'],
          ['site-kit', 'boolean'],
        ]),
    ),
    sign: {
      dir: 'string',
      'key-file': 'string',
      'confirm-sha256': 'string',
      'review-only': 'boolean',
    },
    publish: {
      dir: 'string',
      'public-key': 'string',
      'admin-origin': 'string',
      'portal-origin': 'string',
      'confirm-http-origin': 'string',
      'confirm-portal-http-origin': 'string',
    },
  };
  if (command === 'clean-source') {
    const { values, positionals } = parseArgs({
      args: argv.slice(1),
      options: { 'include-ui': { type: 'boolean' } },
      allowPositionals: true,
    });
    if (positionals.length !== 1)
      throw new Error(
        '用法：auriga-client clean-source <源码目录> [--include-ui]',
      );
    return cleanSource(positionals[0], { includeUi: values['include-ui'] });
  }
  if (!allowed[command])
    throw new Error('未知客户端命令，运行 auriga-client --help');
  const { values: o } = parseArgs({
    args: argv.slice(1),
    options: Object.fromEntries(
      Object.entries(allowed[command]).map(([name, type]) => [name, { type }]),
    ),
  });
  if (command === 'keygen') {
    if (!o.out) throw new Error('缺少 --out');
    return generateDesktopKeys(o.out);
  }
  if (command === 'build') return startBuild(normalizeBuildAliases(o));
  if (command === 'sign') return signCommand(o);
  for (const name of ['dir', 'public-key', 'admin-origin', 'portal-origin'])
    if (!o[name]) throw new Error(`缺少 --${name}`);
  const dir = resolve(o.dir);
  const manifest = join(dir, 'desktop-release.json');
  const envelope = JSON.parse(readFileSync(manifest));
  return publish({
    manifest,
    buildRecord: join(dir, 'desktop-build.json'),
    archive: join(
      dir,
      clientPlatform(envelope.platform).setupName(envelope.version),
    ),
    publicKey: o['public-key'],
    adminOrigin: o['admin-origin'],
    portalOrigin: o['portal-origin'],
    confirmHttpOrigin: o['confirm-http-origin'],
    confirmPortalHttpOrigin: o['confirm-portal-http-origin'],
    auth: parseAdminAuth(process.env.AURIGA_CLIENT_ADMIN_AUTH),
  });
}
if (
  process.argv[1] &&
  resolve(process.argv[1]) === fileURLToPath(import.meta.url)
) {
  try {
    const result = await main(process.argv.slice(2));
    if (result) console.log(JSON.stringify(result, null, 2));
  } catch (error) {
    const action = process.argv[2];
    console.error(formatCliError(action, error, process.argv.slice(2)));
    process.exitCode = 1;
  }
}
