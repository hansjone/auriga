import { parseArgs } from 'node:util';
import { existsSync, readFileSync, writeFileSync, rmSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { startOfflineBuild, buildWorker } from './build.mjs';
import { generateDesktopKeys, assertPrivateKey } from './keys.mjs';
import { publish } from './publish.mjs';
import { physical } from './inputs.mjs';
import {
  reviewRelease,
  signRelease,
} from '../../clients/installer/scripts/sign-release.mjs';
import { parse as parseVersion } from '../../clients/installer/runtime/semver-lite.mjs';

const HELP = `现场客户端发行（Windows PowerShell 5.1: .\\auriga.cmd client ...）
  client keygen --out <新密钥目录>
  client build --offline --devkit <Windows套件目录> --devkit-sha256 <独立批准清单摘要> --release-public-key <公钥.pem> --resource-trust-json <resource-roots.json> --version <SemVer> --sequence <递增整数> --out <新输出目录>
  client sign --dir <构建输出目录> --review-only
  client sign --dir <构建输出目录> --key-file <私钥.pem> --confirm-sha256 <审阅摘要>
  client publish --dir <构建输出目录> --public-key <现场公钥.pem> --admin-origin <管理入口origin> --portal-origin <门户origin>
发布会话由 AURIGA_CLIENT_ADMIN_AUTH 环境变量提供 JSON {cookie,csrf}，先在正常管理台完成近期身份验证。`;
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
    archive: join(dir, `AurigaDSH-Setup-${body.version}.exe`),
    siteOffline: true,
  };
  const output = join(dir, 'desktop-release.json');
  const checkpoint = join(dir, 'desktop-release.review.json');
  if (existsSync(output)) throw new Error('已签清单已存在，禁止覆盖');
  const review = reviewRelease(options).summary;
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
  if (argv[0] === 'client') argv = argv.slice(1);
  if (!argv.length || argv.includes('--help')) {
    console.log(HELP);
    return;
  }
  const command = argv[0];
  const allowed = {
    keygen: { out: 'string' },
    build: Object.fromEntries(
      [
        'devkit',
        'devkit-sha256',
        'out',
        'release-public-key',
        'resource-trust-json',
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
      ]
        .map((name) => [name, 'string'])
        .concat([['offline', 'boolean']]),
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
  if (!allowed[command])
    throw new Error('未知 client 命令，运行 client --help');
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
  if (command === 'build') return startOfflineBuild(o);
  if (command === 'sign') return signCommand(o);
  for (const name of ['dir', 'public-key', 'admin-origin', 'portal-origin'])
    if (!o[name]) throw new Error(`缺少 --${name}`);
  const dir = resolve(o.dir);
  const manifest = join(dir, 'desktop-release.json');
  const envelope = JSON.parse(readFileSync(manifest));
  return publish({
    manifest,
    archive: join(dir, `AurigaDSH-Setup-${envelope.version}.exe`),
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
    console.error(`客户端发行失败：${error.message}`);
    process.exitCode = 1;
  }
}
